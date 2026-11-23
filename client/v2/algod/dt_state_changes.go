package algod

// This file is part of dt-go-algorand-sdk: the app state changes of each
// transaction of a round (GET /v2/deltas/{round}/txn/state-changes), and the
// checks for it and simulate's state-change-app-id.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

// StateChangesInclude is a value of GetTxnStateChangesForRound's Include: what
// else to list besides the app state changes.
type StateChangesInclude string

const (
	// IncludeAccounts lists each group's account changes (Groups, Accounts).
	IncludeAccounts StateChangesInclude = "accounts"
	// IncludeAssets lists each group's asset holding and params changes
	// (Groups, Holdings and Assets).
	IncludeAssets StateChangesInclude = "assets"
	// IncludeApps lists each group's app params and local state changes other
	// than state keys (Groups, Apps and LocalStates).
	IncludeApps StateChangesInclude = "apps"
	// IncludeBudget lists each program run's opcode budget (Budget).
	IncludeBudget StateChangesInclude = "budget"
	// IncludeReads lists the app state each transaction read (Reads).
	IncludeReads StateChangesInclude = "reads"
	// IncludeTrace lists the execution traces of one transaction's group and
	// the programs they ran (Traces, Programs). It needs exactly one
	// transaction (Txid, or Txids of one) and a node with EnableDeveloperAPI.
	IncludeTrace StateChangesInclude = "trace"
)

// StateChangesIncludes lists every StateChangesInclude, in the order they are
// sent.
var StateChangesIncludes = []StateChangesInclude{IncludeAccounts, IncludeAssets, IncludeApps, IncludeBudget, IncludeReads, IncludeTrace}

// groupIncludes are the values that list Groups.
var groupIncludes = []StateChangesInclude{IncludeAccounts, IncludeAssets, IncludeApps}

type getTxnStateChangesParams struct {
	App     uint64 `url:"app,omitempty"`
	Format  string `url:"format,omitempty"`
	Include string `url:"include,omitempty"`
	Txid    string `url:"txid,omitempty"`
	Txids   string `url:"txids,omitempty"`
}

// GetTxnStateChangesForRound lists the app state changes of each transaction
// of a round that changed any, top-level and inner, in execution order:
// globals, local states and boxes, each with its app and its value before and
// after the transaction. With Include it also lists what each group changed
// besides, each program run's budget, the app state each transaction read,
// and one group's execution traces.
//
// It needs a time-travel node (EnableTimeTravel) of dt-algorand dt6 or
// later: others, stock algod included, give a *DTUnsupportedError.
// SupportsStateChanges tells beforehand. The node re-evaluates the round's
// block for each request, so the filters shrink the answer, not the work. A
// round whose block is not stored, round 0, a round not reached yet, or a txid
// not in the round is the node's 404, returned as is. An answer over the
// node's StateChangesLimits.MaxResponseBytes is a 400 (IsStateChangesTooLarge):
// narrow it with App or Txids, or include less. Busy re-evaluation slots give
// a 503 (IsNodeBusy), the time limit a 504 (IsTimeLimitExceeded), and a
// re-evaluation that does not reproduce the round a 500.
type GetTxnStateChangesForRound struct {
	c *Client

	round uint64

	p getTxnStateChangesParams

	include []StateChangesInclude

	err error
}

// GetTxnStateChangesForRound lists the app state changes of each transaction
// of a round.
func (c *Client) GetTxnStateChangesForRound(round uint64) *GetTxnStateChangesForRound {
	return &GetTxnStateChangesForRound{c: c, round: round, p: getTxnStateChangesParams{Format: "msgpack"}}
}

// App lists only the changes of this application (0: every application). It
// narrows Txns only, not Groups, Budget, Reads or Traces.
func (s *GetTxnStateChangesForRound) App(appID uint64) *GetTxnStateChangesForRound {
	s.p.App = appID
	return s
}

// Txid lists only this top-level transaction and its inner transactions. It
// combines with Txids.
func (s *GetTxnStateChangesForRound) Txid(txid string) *GetTxnStateChangesForRound {
	if s.check(checkTxid(txid)) {
		s.p.Txid = txid
	}
	return s
}

// Txids lists only these top-level transactions and their inner
// transactions, replacing any list set before; pass all of a group's in one
// call. They are sent as one comma-separated parameter, as the node requires.
// An empty list or a malformed ID makes Do fail without sending.
func (s *GetTxnStateChangesForRound) Txids(txids []string) *GetTxnStateChangesForRound {
	if len(txids) == 0 {
		s.check(errors.New("txids needs at least one transaction ID"))
		return s
	}
	for _, txid := range txids {
		if !s.check(checkTxid(txid)) {
			return s
		}
	}
	s.p.Txids = strings.Join(txids, ",")
	return s
}

// Include sets what else to list, in any order, replacing the list set
// before; repeats are ignored. An empty list or an unknown value makes Do fail
// without sending.
func (s *GetTxnStateChangesForRound) Include(include ...StateChangesInclude) *GetTxnStateChangesForRound {
	if len(include) == 0 {
		s.check(errors.New("include needs at least one value"))
		return s
	}
	for _, v := range include {
		if !slices.Contains(StateChangesIncludes, v) {
			s.check(fmt.Errorf("not a state changes include: %q", v))
			return s
		}
	}
	s.include = nil
	for _, v := range StateChangesIncludes {
		if slices.Contains(include, v) {
			s.include = append(s.include, v)
		}
	}
	names := make([]string, len(s.include))
	for i, v := range s.include {
		names[i] = string(v)
	}
	s.p.Include = strings.Join(names, ",")
	return s
}

// Full adds accounts, assets and apps to the list Include set: everything
// each group changed.
func (s *GetTxnStateChangesForRound) Full() *GetTxnStateChangesForRound {
	return s.Include(append(slices.Clone(s.include), groupIncludes...)...)
}

// Trace adds trace to the list Include set, and sets Txid to txid: the
// execution traces of that transaction's group. It needs a node with
// EnableDeveloperAPI.
func (s *GetTxnStateChangesForRound) Trace(txid string) *GetTxnStateChangesForRound {
	return s.Txid(txid).Include(append(slices.Clone(s.include), IncludeTrace)...)
}

// check records the first error of the setters, which Do returns.
func (s *GetTxnStateChangesForRound) check(err error) bool {
	if err != nil && s.err == nil {
		s.err = fmt.Errorf("%s: %w", stateChangesOp, err)
	}
	return err == nil
}

func (s *GetTxnStateChangesForRound) has(v StateChangesInclude) bool {
	return slices.Contains(s.include, v)
}

// txids returns the distinct transactions Txid and Txids asked for.
func (s *GetTxnStateChangesForRound) txids() []string {
	var ids []string
	if s.p.Txid != "" {
		ids = append(ids, s.p.Txid)
	}
	if s.p.Txids != "" {
		ids = append(ids, strings.Split(s.p.Txids, ",")...)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

const stateChangesOp = "GET /v2/deltas/{round}/txn/state-changes"

// Do performs the HTTP request, in msgpack. The answer's round must be the
// round asked for (the route sends no X-Algod-TT-Round: the round is in the
// path). An answer without the fee sink when group values were asked for, or
// without traces when trace was, comes from a node that ignored Include and
// is a *DTUnsupportedError. An ignored budget or reads can't be told from an
// empty list: check SupportsStateChangesInclude first.
func (s *GetTxnStateChangesForRound) Do(ctx context.Context, headers ...*common.Header) (response models.TxnStateChangesResponse, err error) {
	if s.err != nil {
		return response, s.err
	}
	if s.has(IncludeTrace) {
		if n := len(s.txids()); n != 1 {
			return response, fmt.Errorf("%s: trace needs exactly one transaction, with Txid or Txids, not %d", stateChangesOp, n)
		}
	}
	err = dtCall(ctx, func(ctx context.Context) error {
		return s.c.getMsgpack(ctx, &response, fmt.Sprintf("/v2/deltas/%d/txn/state-changes", s.round), s.p, headers)
	})
	if err != nil {
		return response, s.classify(err)
	}
	if response.Round != s.round {
		return response, fmt.Errorf("%s: the state changes are for round %d, not the round asked for, %d", stateChangesOp, response.Round, s.round)
	}
	// The node always sends the fee sink with the group values, and with
	// trace, a trace of every transaction of the traced one's group (a txid
	// not in the round is a 404): an answer without them ignored include.
	if (slices.ContainsFunc(s.include, func(v StateChangesInclude) bool { return slices.Contains(groupIncludes, v) }) && response.FeeSink == "") ||
		(s.has(IncludeTrace) && len(response.Traces) == 0) {
		return response, s.unsupportedInclude("it ignored", nil)
	}
	return response, nil
}

func (s *GetTxnStateChangesForRound) unsupportedInclude(what string, cause error) *DTUnsupportedError {
	e := unsupported(stateChangesOp, fmt.Sprintf("the node cannot add to the state changes: %s include=%s", what, s.p.Include), cause)
	e.RequestedRound = &s.round
	return e
}

// classify turns the answers of nodes without the endpoint, or without an
// include value, into *DTUnsupportedError.
func (s *GetTxnStateChangesForRound) classify(err error) error {
	reason := ""
	switch {
	case len(s.include) > 0 && hasStatus(err, http.StatusBadRequest, reIncludeRejected):
		return s.unsupportedInclude("it rejected", err)
	case hasStatus(err, http.StatusNotFound, reNoHistory):
		reason = "its ledger has no time travel history"
	case hasStatus(err, http.StatusNotFound, reStateDelta):
		// The handler's own 404: the round or a txid is not there.
		return err
	case hasStatus(err, http.StatusNotFound, nil):
		// The router's: time travel off, stock algod, or a node from before dt6.
		reason = "it does not serve the endpoint: time travel is off (EnableTimeTravel), or it is older than dt6"
	default:
		return err
	}
	e := unsupported(stateChangesOp, "the node cannot list state changes: "+reason, err)
	e.RequestedRound = &s.round
	return e
}

// The node's messages: errFailedParsingIncludeOption, errTraceNeedsDeveloperAPI,
// errFailedRetrievingStateDelta, errStateChangesTooLarge, and the ledger's
// without time travel.
var (
	reIncludeRejected = regexp.MustCompile(`include takes |include=trace needs node config EnableDeveloperAPI`)
	reNoHistory       = regexp.MustCompile(`has no time travel history|need the time travel history`)
	reStateDelta      = regexp.MustCompile(`failed retrieving State Delta`)
	reStateChangesBig = regexp.MustCompile(`take more than MaxAPIStateChangesResponseBytes`)
	reTxid            = regexp.MustCompile(`^[A-Z2-7]{52}$`)
)

func checkTxid(txid string) error {
	if !reTxid.MatchString(txid) {
		return fmt.Errorf("not a transaction ID: %q", txid)
	}
	return nil
}

// IsStateChangesTooLarge reports whether err is GetTxnStateChangesForRound's
// 400 for an answer over the node's StateChangesLimits.MaxResponseBytes (or an
// evaluation that logged more than four times that). Narrow the request with
// App or fewer Txids, or include less.
func IsStateChangesTooLarge(err error) bool {
	return hasStatus(err, http.StatusBadRequest, reStateChangesBig)
}

// SupportsStateChanges reports whether the node lists the state changes of a
// round (GetTxnStateChangesForRound): a dt-algorand node of dt6 or later with
// time travel, whose capabilities have StateChanges. Errors other than "not
// supported" (network, 401) are returned. It does not cache: keep the answer.
func (c *Client) SupportsStateChanges(ctx context.Context) (bool, error) {
	limits, err := c.stateChangesLimits(ctx)
	return limits != nil, err
}

// SupportsStateChangesInclude reports whether the node takes each of these
// Include values of GetTxnStateChangesForRound, as its capabilities'
// StateChanges.Include lists them: trace only with EnableDeveloperAPI. With
// no values, it reports whether the node takes Include at all. Errors other
// than "not supported" are returned. It does not cache: keep the answer.
func (c *Client) SupportsStateChangesInclude(ctx context.Context, include ...StateChangesInclude) (bool, error) {
	limits, err := c.stateChangesLimits(ctx)
	if limits == nil {
		return false, err
	}
	if len(include) == 0 {
		return len(limits.Include) > 0, nil
	}
	for _, v := range include {
		if !slices.Contains(limits.Include, string(v)) {
			return false, nil
		}
	}
	return true, nil
}

func (c *Client) stateChangesLimits(ctx context.Context) (*models.StateChangesLimits, error) {
	features, err := c.DTFeatures(ctx)
	if err != nil || !features.TimeTravel || features.Capabilities == nil {
		return nil, err
	}
	return features.Capabilities.StateChanges, nil
}

// SupportsStateChangeAppId reports whether the node takes simulate's
// SimulateTraceConfig.StateChangeAppId: a dt-algorand node of dt6 or later
// with the developer API (EnableDeveloperAPI), which lists
// "state-change-app-id" in its capabilities' Simulate.ExecTraceConfig. Stock
// algod and older nodes answer 400 to it. Errors other than "not supported"
// are returned. It does not cache: keep the answer.
func (c *Client) SupportsStateChangeAppId(ctx context.Context) (bool, error) {
	features, err := c.DTFeatures(ctx)
	if err != nil || features.Capabilities == nil {
		return false, err
	}
	caps := features.Capabilities
	return caps.DeveloperApi && slices.Contains(caps.Simulate.ExecTraceConfig, "state-change-app-id"), nil
}
