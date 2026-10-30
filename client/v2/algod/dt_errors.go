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
// it is stock algod, or a dt-algorand node with the feature off. It is
// returned when the node:
//   - answered a read at a round, or its 404, without marking it with the
//     round (X-Algod-TT-Round): stock algod ignores round and answers for its
//     latest round, and older dt-algorand builds don't send the header;
//   - refused past rounds ("requires EnableTimeTravel", "given round is no
//     longer available");
//   - has no versions or holders endpoints (time travel off, or stock
//     algod), or the holders index off;
//   - has no query endpoint (404) or queries disabled;
//   - has no capabilities endpoint (stock algod).
//
// Older dt-algorand builds are not supported.
type DTUnsupportedError struct {
	// Op names the request, such as "GET /v2/accounts/{address}".
	Op string
	// Reason says what the node did.
	Reason string
	// RequestedRound is the round asked for, if the request had one.
	RequestedRound *uint64
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
// an error response in a common.ResponseError.
func dtCall(ctx context.Context, request func(ctx context.Context) error) error {
	_, err := dtCallInfo(ctx, request)
	return err
}

// dtCallInfo is dtCall, also returning the response's status and headers.
func dtCallInfo(ctx context.Context, request func(ctx context.Context) error) (*common.ResponseInfo, error) {
	info := common.ResponseInfoFrom(ctx)
	if info == nil {
		info = &common.ResponseInfo{}
		ctx = common.WithResponseInfo(ctx, info)
	}
	return info, common.WrapResponseError(request(ctx), info)
}

// StatusCode returns the HTTP status of an algod error response: a
// common.ResponseError, or the common.HTTPError (or BadRequest, InvalidToken,
// NotFound, InternalError) the client returns for every error response. It
// returns false for other errors, such as network ones.
func StatusCode(err error) (int, bool) {
	var respErr *common.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode, true
	}
	var (
		plain    common.HTTPError
		bad      common.BadRequest
		token    common.InvalidToken
		notFound common.NotFound
		internal common.InternalError
	)
	switch {
	case errors.As(err, &plain):
		return plain.StatusCode, true
	case errors.As(err, &bad):
		return bad.StatusCode, true
	case errors.As(err, &token):
		return token.StatusCode, true
	case errors.As(err, &notFound):
		return notFound.StatusCode, true
	case errors.As(err, &internal):
		return internal.StatusCode, true
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
// errReevalBusy (ledger.ErrReevalBusy), ledger.ErrRoundTooLarge,
// ledger.ErrTooManyVersionObjects, simulation.ErrQueryResponseTooLarge,
// ledger.ErrOnlineStakeUnavailable, and the handlers' own messages.
var (
	reNodeBusy      = regexp.MustCompile(`node is busy|every slot for re-evaluating old rounds is in use`)
	reCatchup       = regexp.MustCompile(`not available during catchup`)
	reHistory       = regexp.MustCompile(`box index is incomplete|asset holders index is not complete|history is not served`)
	reRoundTooLarge = regexp.MustCompile(`one round of boxes is too large|too many objects to merge`)
	reQueryTooLarge = regexp.MustCompile(`query response too large`)
	reOnlineStake   = regexp.MustCompile(`does not record this online stake`)
	rePastRound     = regexp.MustCompile(`requires? EnableTimeTravel|given round is no longer available`)
	reHoldersOff    = regexp.MustCompile(`asset holders require EnableTimeTravel and TimeTravelHoldersIndex`)
	reAssetMissing  = regexp.MustCompile(`asset does not exist`)
	reQueryDisabled = regexp.MustCompile(`query is disabled on this node`)
	// Without the time-travel routes, the router hands /v2/assets/{id}/holders
	// and .../{id}/versions to the plain lookup, which can't parse "{id}/...".
	reRouteMissing = regexp.MustCompile(`Invalid format for parameter (asset-id|application-id)`)
)

// IsNodeBusy reports whether err is the node's 503 "the node is busy", from
// its limit on concurrent simulate and query requests, or "every slot for
// re-evaluating old rounds is in use", from its limit on rebuilding old
// deltas. Retry after a
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
// (it differs from the chain, or a replay found a problem that a reload
// clears). Fall back to reads at a round, another
// node or an indexer.
func IsHistoryUnavailable(err error) bool {
	return hasStatus(err, http.StatusServiceUnavailable, reHistory)
}

// IsTimeLimitExceeded reports whether err is the node's 504: the request ran
// longer than the node allows (CapabilitiesResponse.TimeLimitSeconds).
// Retrying the same request fails again: split it instead.
func IsTimeLimitExceeded(err error) bool { return hasStatus(err, http.StatusGatewayTimeout, nil) }

// IsRoundTooLarge reports whether err is a versions request over the node's
// object limits: an account with more resources than the node merges ("too
// many objects to merge", in range mode too: use Exclude or the per-resource
// versions endpoints), or with Rounds, an application with more boxes than
// the node merges ("too many objects to merge") or one round of its boxes
// too large for a page ("one round of boxes is too large"): use Prefix, or
// GetApplicationBoxesAt at that round.
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
// headers: errors of the time-travel methods carry them (common.ResponseError);
// for upstream methods it returns false.
func RetryAfter(err error) (time.Duration, bool) {
	var httpErr *common.ResponseError
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

// isRouteMissing reports whether err is the 400 a node without the
// time-travel routes gives a holders or versions request whose path ends in
// an asset or application ID: the router hands it to the plain lookup, which
// can't parse "{id}/holders". The SDK always sends a valid ID, so this 400
// means the route is missing.
func isRouteMissing(err error) bool {
	return hasStatus(err, http.StatusBadRequest, reRouteMissing)
}

// isPastRoundUnsupported reports whether a read at a round was refused
// because the node can't read past rounds.
func isPastRoundUnsupported(err error) bool {
	return hasStatus(err, http.StatusBadRequest, rePastRound)
}
