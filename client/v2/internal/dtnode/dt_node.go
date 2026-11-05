// Package dtnode holds what the algod error helpers and the retry policy in
// common both need to read a dt-algorand node's answers. It is part of
// dt-go-algorand-sdk.
package dtnode

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The node's error messages, named by their server constants (go-algorand
// dtalgorand): errBusy (request_limits.go), errReevalBusy
// (ledger.ErrReevalBusy), timetravel.ErrNoBoxIndex,
// timetravel.ErrNoHoldersIndex and ledger.ErrHistoryUntrusted.
var (
	// NodeBusy matches the 503s of a node out of capacity for simulate,
	// query, versions and deltas requests.
	NodeBusy = regexp.MustCompile(`node is busy|every slot for re-evaluating old rounds is in use`)
	// History matches the 503s for history the node can't serve now.
	History = regexp.MustCompile(`box index is incomplete|asset holders index is not complete|history is not served`)
)

// ParseRetryAfter reads a Retry-After value, in seconds or as an HTTP date (0
// if already past at now, else rounded up to the second), as dt-algosdk
// does. A huge number of seconds, or a date centuries ahead, gives the longest
// Duration.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.ParseUint(value, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		if secs > math.MaxInt64/uint64(time.Second) {
			return math.MaxInt64, true
		}
		return time.Duration(secs) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	wait := at.Sub(now)
	if wait <= 0 {
		return 0, true
	}
	secs := math.Ceil(wait.Seconds())
	if secs >= float64(math.MaxInt64/int64(time.Second)) {
		return math.MaxInt64, true
	}
	return time.Duration(secs) * time.Second, true
}
