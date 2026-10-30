package algod

// This file is part of go-dtalgorand-sdk: the errors of the time-travel
// methods, and helpers that classify any algod error.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
)

// ErrDTUnsupported matches (with errors.Is) every DTUnsupportedError.
var ErrDTUnsupported = errors.New("the node does not support this time-travel request")

// DTUnsupportedError says that a node cannot answer a time-travel request:
// it is stock algod or a fork build without the feature, or a fork node with
// the feature off. It is returned when the node:
//   - answered a read at a round for another round (stock algod ignores
//     round and answers for the latest), or answered 404 to such a read and is
//     not a time-travel node;
//   - refused past rounds ("requires EnableTimeTravel", "given round is no
//     longer available");
//   - has no versions, holders or query endpoint, or has it disabled;
//   - ignored rounds or include=keys on a versions request.
type DTUnsupportedError struct {
	// Op names the request, such as "GET /v2/accounts/{address}".
	Op string
	// Reason says what the node did.
	Reason string
	// RequestedRound is the round asked for, if the request had one.
	RequestedRound *uint64
	// ReturnedRound is the round the node answered for, if it reported one.
	ReturnedRound *uint64
	// Cause is the node's error response, if any.
	Cause error
}

func (e *DTUnsupportedError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Reason, ErrDTUnsupported)
}

// Is reports whether target is ErrDTUnsupported.
func (e *DTUnsupportedError) Is(target error) bool { return target == ErrDTUnsupported }

// Unwrap returns the node's error response, if any.
func (e *DTUnsupportedError) Unwrap() error { return e.Cause }

func unsupported(op, reason string, cause error) *DTUnsupportedError {
	return &DTUnsupportedError{Op: op, Reason: reason, Cause: cause}
}

// dtCall runs a request with a context that captures the response's status
// and headers, reusing the caller's ResponseInfo if it passed one, and wraps
// an error response in a common.HTTPError.
func dtCall(ctx context.Context, request func(ctx context.Context) error) error {
	info := common.ResponseInfoFrom(ctx)
	if info == nil {
		info = &common.ResponseInfo{}
		ctx = common.WithResponseInfo(ctx, info)
	}
	return common.WrapHTTPError(request(ctx), info)
}

// The upstream client formats error responses as "HTTP <code>: <body>".
var statusPrefix = regexp.MustCompile(`^HTTP (\d{3}): `)

// StatusCode returns the HTTP status of an algod error response, from a
// common.HTTPError or from the "HTTP <code>: " message the client gives every
// error response. It returns false for other errors, such as network ones.
func StatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode, true
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if m := statusPrefix.FindStringSubmatch(e.Error()); m != nil {
			code, _ := strconv.Atoi(m[1])
			return code, true
		}
	}
	return 0, false
}

// hasStatus reports whether err is an error response with this status whose
// message matches re (if not nil).
func hasStatus(err error, status int, re *regexp.Regexp) bool {
	code, ok := StatusCode(err)
	return ok && code == status && (re == nil || re.MatchString(err.Error()))
}

// The node's error messages the helpers match, named by their server
// constants (go-algorand dtalgorand): errBusy (request_limits.go),
// errOperationNotAvailableDuringCatchup, timetravel.ErrNoBoxIndex,
// timetravel.ErrNoHoldersIndex, ledger.ErrHistoryUntrusted,
// ledger.ErrRoundTooLarge ("too many boxes at one round" in earlier builds),
// ledger.ErrTooManyVersionObjects, simulation.ErrQueryResponseTooLarge,
// ledger.ErrOnlineStakeUnavailable, and the handlers' own messages.
var (
	reNodeBusy         = regexp.MustCompile(`node is busy`)
	reCatchup          = regexp.MustCompile(`not available during catchup`)
	reHistory          = regexp.MustCompile(`box index is incomplete|asset holders index is not complete|history is not served`)
	reRoundTooLarge    = regexp.MustCompile(`one round of boxes is too large|too many boxes at one round|too many objects to merge`)
	reQueryTooLarge    = regexp.MustCompile(`query response too large`)
	reOnlineStake      = regexp.MustCompile(`does not record this online stake`)
	rePastRound        = regexp.MustCompile(`requires? EnableTimeTravel|given round is no longer available`)
	reVersionsMisparse = regexp.MustCompile(`Invalid format for parameter (?:asset-id|application-id): .*parsing \\?"[^"\\]*/versions\\?": invalid syntax`)
	reVersionsOff      = regexp.MustCompile(`versions require EnableTimeTravel`)
	reKeysUnknown      = regexp.MustCompile(`invalid include value: keys`)
	reHoldersMisparse  = regexp.MustCompile(`Invalid format for parameter asset-id: .*parsing \\?"[^"\\]*/holders\\?": invalid syntax`)
	reHoldersOff       = regexp.MustCompile(`asset holders require EnableTimeTravel and TimeTravelHoldersIndex|asset holders need a time travel store`)
	reAssetMissing     = regexp.MustCompile(`asset does not exist`)
	reQueryDisabled    = regexp.MustCompile(`query is disabled on this node`)
)

// IsNodeBusy reports whether err is the node's 503 "the node is busy", from
// its limit on concurrent simulate, query and delta requests. Retry after a
// moment (RetryAfter), or on another node. Other 503s mean something else
// (IsCatchingUp, IsHistoryUnavailable).
func IsNodeBusy(err error) bool { return hasStatus(err, http.StatusServiceUnavailable, reNodeBusy) }

// IsCatchingUp reports whether err is a 503 from a node in catchpoint catchup,
// which can't serve the request until the catchup ends.
func IsCatchingUp(err error) bool { return hasStatus(err, http.StatusServiceUnavailable, reCatchup) }

// IsHistoryUnavailable reports whether err is a 503 saying the node can't
// serve that history now, which retrying soon won't fix: the box index is
// incomplete (boxes versions before it), the asset holders index is not
// complete (still being built), or the time-travel history is not served
// (the node is still catching it up). Fall back to reads at a round, another
// node or an indexer.
func IsHistoryUnavailable(err error) bool {
	return hasStatus(err, http.StatusServiceUnavailable, reHistory)
}

// IsTimeLimitExceeded reports whether err is the node's 504: the request ran
// longer than the node allows (SimulateLimitsResponse.TimeLimitSeconds).
// Retrying the same request fails again: split it instead.
func IsTimeLimitExceeded(err error) bool { return hasStatus(err, http.StatusGatewayTimeout, nil) }

// IsRoundTooLarge reports whether err is a versions request over the node's
// object limits: an account with more resources than the node merges ("too
// many objects to merge", in range mode too: use Exclude or the per-resource
// versions endpoints), or with Rounds, one round of an application's boxes
// too large for a page ("one round of boxes is too large": use Prefix, or
// GetApplicationBoxesAt at that round).
func IsRoundTooLarge(err error) bool {
	return hasStatus(err, http.StatusBadRequest, reRoundTooLarge)
}

// IsQueryResponseTooLarge reports whether err is a query whose results are
// over the node's QueryLimits.MaxResponseBytes. Split the request (fewer calls
// or rounds) and retry.
func IsQueryResponseTooLarge(err error) bool {
	return hasStatus(err, http.StatusBadRequest, reQueryTooLarge)
}

// IsOnlineStakeNotRecorded reports whether err is the 404 a supply read at a
// round gets when the node's history did not record that round's online stake.
func IsOnlineStakeNotRecorded(err error) bool {
	return hasStatus(err, http.StatusNotFound, reOnlineStake)
}

// RetryAfter returns how long an error response's Retry-After header says to
// wait, given in seconds or as an HTTP date (0 if already past). It needs the
// headers: errors of the time-travel methods carry them (common.HTTPError);
// for upstream methods it returns false.
func RetryAfter(err error) (time.Duration, bool) {
	var httpErr *common.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Header == nil {
		return 0, false
	}
	value := strings.TrimSpace(httpErr.Header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	wait := time.Until(at)
	if wait < 0 {
		wait = 0
	}
	return wait.Round(time.Second), true
}

// isPastRoundUnsupported reports whether a read at a round was refused
// because the node can't read past rounds.
func isPastRoundUnsupported(err error) bool {
	return hasStatus(err, http.StatusBadRequest, rePastRound)
}
