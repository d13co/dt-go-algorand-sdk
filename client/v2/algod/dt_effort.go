package algod

// This file is part of dt-go-algorand-sdk: the X-Algod-Effort header.

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// EffortHeader is the response header in which a time-travel node with
// EnableEffortHeader reports the work a request did: simulate, query,
// versions, deltas, and reads with a round (round=<latest> included), but
// not the asset holders listing, which counts nothing. Read it with
// common.WithResponseInfo and ParseEffort. The node sends it whenever the
// option is on (it is meant to run behind a proxy that strips it), also on
// errors after the work started, but never on answers given before any work
// (503s, most 400s) nor on reads without a round.
const EffortHeader = "X-Algod-Effort"

// Effort is the work a request did, from the X-Algod-Effort header.
type Effort struct {
	// Opcodes is the app and logicsig budget consumed (simulate, query).
	Opcodes int64
	// Lookups is the ledger objects read.
	Lookups int64
	// History is the lookups the time-travel history answered.
	History int64
	// Scanned is the entries read by scans (versions, listings).
	Scanned int64
	// ResponseBytes is the size of the answer before compression; 0 for
	// reads at a round, which are counted by Lookups.
	ResponseBytes int64
	// EvalMicroseconds is the time spent serving the request.
	EvalMicroseconds int64
}

var effortValue = regexp.MustCompile(`^-?\d{1,15}$`)

// ParseEffort parses the X-Algod-Effort header of a response, an RFC 9651
// dictionary of integers such as
// "opcodes=700, lookups=3, history=2, scanned=0, response-bytes=512, eval-us=840".
// Unknown keys are ignored. It returns false when the header is absent, or a
// known key is missing or not an integer.
func ParseEffort(header http.Header) (Effort, bool) {
	value := header.Get(EffortHeader)
	if strings.TrimSpace(value) == "" {
		return Effort{}, false
	}
	var e Effort
	fields := map[string]*int64{
		"opcodes":        &e.Opcodes,
		"lookups":        &e.Lookups,
		"history":        &e.History,
		"scanned":        &e.Scanned,
		"response-bytes": &e.ResponseBytes,
		"eval-us":        &e.EvalMicroseconds,
	}
	seen := make(map[string]bool, len(fields))
	for _, member := range strings.Split(value, ",") {
		// Parameters (";name=value") are not used by the node: drop them.
		item, _, _ := strings.Cut(member, ";")
		name, raw, _ := strings.Cut(item, "=")
		name = strings.TrimSpace(name)
		field, known := fields[name]
		if !known {
			continue
		}
		raw = strings.TrimSpace(raw)
		if !effortValue.MatchString(raw) {
			return Effort{}, false
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Effort{}, false
		}
		*field = n
		seen[name] = true
	}
	if len(seen) != len(fields) {
		return Effort{}, false
	}
	return e, true
}
