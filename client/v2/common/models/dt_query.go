package models

// This file is part of dt-go-algorand-sdk: the models of the asset holders,
// query and capabilities endpoints of a time-travel algod.

// AssetHoldersResponse is a page of the accounts that hold an asset.
type AssetHoldersResponse struct {
	// Holders lists the holders, in address order. Accounts opted in with a
	// zero balance are listed.
	Holders []AssetHolder `json:"holders"`

	// NextToken is the token for the next page, absent on the last one. A page
	// can be short or empty and still have one.
	NextToken string `json:"next-token,omitempty"`

	// Round is the round the page was read at. Read the next pages at it.
	Round uint64 `json:"round"`
}

// AssetHolder is an account's holding of an asset.
type AssetHolder struct {
	// Address is the account's address.
	Address string `json:"address"`

	// Amount is the balance, in base units.
	Amount uint64 `json:"amount"`

	// IsFrozen is whether the holding is frozen.
	IsFrozen bool `json:"is-frozen"`
}

// QueryRequest is a POST /v2/query request: read-only calls of a program, or
// of an existing application, at one or more rounds. Set exactly one of
// Program and AppId.
type QueryRequest struct {
	// AppId calls this existing application.
	AppId uint64 `json:"app-id,omitempty"`

	// Budget is the opcode budget of each call, at most the node's
	// QueryLimits.MaxCallBudget; 0 for that maximum.
	Budget uint64 `json:"budget,omitempty"`

	// Calls lists the calls, at most QueryLimits.MaxCalls, and at most
	// QueryLimits.MaxTotalCalls divided by len(Rounds) (1 when empty). Each
	// runs alone at each round, on the state the round left: the writes of one
	// call are not seen by the others.
	Calls []QueryCall `json:"calls"`

	// ClearProgram is the clear state program of Program; optional.
	ClearProgram []byte `json:"clear-program,omitempty"`

	// GlobalSchema is Program's global state schema; optional.
	GlobalSchema *ApplicationStateSchema `json:"global-schema,omitempty"`

	// LocalSchema is Program's local state schema; optional.
	LocalSchema *ApplicationStateSchema `json:"local-schema,omitempty"`

	// Program is a compiled approval program to call, as if it were created
	// just before the calls.
	Program []byte `json:"program,omitempty"`

	// Protocol is the consensus protocol to evaluate with: "latest" (the
	// default), "round" (the protocol the block after each round really ran),
	// a version string or "vNN", or "future" with the developer API.
	Protocol string `json:"protocol,omitempty"`

	// Rounds lists the rounds to run the calls at, as of the end of each round;
	// 0 is the latest round. Empty: the latest round. Duplicates count toward
	// QueryLimits.MaxRounds and MaxTotalCalls.
	Rounds []uint64 `json:"rounds,omitempty"`

	// Sender is the calls' sender; the queried round's fee sink if empty.
	Sender string `json:"sender,omitempty"`
}

// QueryCall is one call of a QueryRequest.
type QueryCall struct {
	// Accounts lists the accounts the call may read.
	Accounts []string `json:"accounts,omitempty"`

	// Args lists the application arguments.
	Args [][]byte `json:"args,omitempty"`

	// Boxes lists the boxes the call may read; app 0 is the called app.
	Boxes []BoxReference `json:"boxes,omitempty"`

	// ForeignApps lists the applications the call may read.
	ForeignApps []uint64 `json:"foreign-apps,omitempty"`

	// ForeignAssets lists the assets the call may read.
	ForeignAssets []uint64 `json:"foreign-assets,omitempty"`
}

// QueryResponse holds the results of a query, one per requested round.
type QueryResponse struct {
	// LastRound is the node's latest round when the query ran.
	LastRound uint64 `json:"last-round"`

	// Rounds lists the results, one per requested round, in request order.
	Rounds []QueryRoundResult `json:"rounds"`
}

// QueryRoundResult holds the calls' results at one round.
type QueryRoundResult struct {
	// Calls lists the calls' results, absent when Error is set.
	Calls []QueryCallResult `json:"calls,omitempty"`

	// Error says why the calls could not run at this round.
	Error string `json:"error,omitempty"`

	// Protocol says which protocol the calls ran with. It is present when the
	// protocol differed from the one the block after the round really ran,
	// or when the request named a protocol other than "round".
	Protocol *QueryProtocolOverride `json:"protocol,omitempty"`

	// Round is the round.
	Round uint64 `json:"round"`
}

// QueryCallResult is the result of one call.
type QueryCallResult struct {
	// Cost is the opcodes the call used.
	Cost uint64 `json:"cost"`

	// Error says why the call failed; the logs are those before the failure.
	Error string `json:"error,omitempty"`

	// Logs lists the call's logs.
	Logs [][]byte `json:"logs"`
}

// CapabilitiesResponse is what a dt-algorand node serves and its limits on
// expensive requests (GET /v2/capabilities).
type CapabilitiesResponse struct {
	// DeveloperApi is whether the node has the developer API
	// (EnableDeveloperAPI): simulate's exec-trace-config, and query's future
	// protocol.
	DeveloperApi bool `json:"developer-api"`

	// Query holds the limits of POST /v2/query; nil when queries are disabled
	// (MaxAPIQueryCalls is 0).
	Query *QueryLimits `json:"query,omitempty"`

	// Simulate holds the limits of simulate requests.
	Simulate SimulateLimits `json:"simulate"`

	// TimeLimitSeconds is how long an expensive request (simulate, query,
	// deltas, versions) may run before it fails with 504; 0 for no limit.
	TimeLimitSeconds uint64 `json:"time-limit-seconds"`

	// TimeTravel is whether the node serves past rounds since genesis
	// (EnableTimeTravel).
	TimeTravel bool `json:"time-travel"`
}

// SimulateLimits is a node's limits on simulate requests.
type SimulateLimits struct {
	// MaxExtraOpcodeBudget is the most extra-opcode-budget a simulate request
	// may ask for, per group.
	MaxExtraOpcodeBudget uint64 `json:"max-extra-opcode-budget"`

	// MaxTxnGroups is the most transaction groups a simulate request may hold.
	MaxTxnGroups uint64 `json:"max-txn-groups"`
}

// QueryLimits is a node's limits on POST /v2/query requests.
type QueryLimits struct {
	// MaxCallBudget is the most opcode budget a call may have.
	MaxCallBudget uint64 `json:"max-call-budget"`

	// MaxCallLogBytes is the most bytes each program of a call may log, in up
	// to 2048 logs; 0 for the protocol's limits (1024 bytes in 32 logs).
	MaxCallLogBytes uint64 `json:"max-call-log-bytes"`

	// MaxCallStateBytes is the most bytes of ledger state a call may read,
	// each object counted once; 0 for no limit. A call over it fails, and the
	// other calls run on.
	MaxCallStateBytes uint64 `json:"max-call-state-bytes"`

	// MaxCalls is the most calls a request may hold.
	MaxCalls uint64 `json:"max-calls"`

	// MaxResponseBytes is the most bytes of logs and errors a response may
	// hold, across its calls and rounds, each log counting 48 bytes more
	// (algod.QueryLogOverheadBytes); 0 for no limit. A request over it fails
	// with 400 (algod.IsQueryResponseTooLarge).
	MaxResponseBytes uint64 `json:"max-response-bytes"`

	// MaxRounds is the most rounds a request may list.
	MaxRounds uint64 `json:"max-rounds"`

	// MaxTotalCalls is the most calls times listed rounds a request may ask
	// for, duplicate rounds included and no rounds counting as 1; at most
	// MaxCalls * MaxRounds. Split requests by it as well as by MaxCalls and
	// MaxRounds. Nodes from before this limit don't report it;
	// Capabilities().Do (and so DTFeatures) then sets it to
	// MaxCalls * MaxRounds, which a plain json.Unmarshal of the body doesn't.
	MaxTotalCalls uint64 `json:"max-total-calls"`
}

// QueryProtocolOverride says which consensus protocol a query round ran
// with instead of the one the block after it really ran.
type QueryProtocolOverride struct {
	// Applied is whether the override was applied.
	Applied bool `json:"applied"`

	// From is the protocol the block after the queried round really ran.
	From string `json:"from"`

	// Notes lists the differences between the protocols that the calls may
	// have run into.
	Notes []QueryProtocolNote `json:"notes,omitempty"`

	// To is the protocol used.
	To string `json:"to"`
}

// QueryProtocolNote is a difference between two protocols.
type QueryProtocolNote struct {
	// Code names the difference, such as "semantics-changed".
	Code string `json:"code"`

	// Hits lists the calls and inner transactions that ran into it, at most
	// 16; absent for notes about the protocol change as a whole.
	Hits []QueryProtocolNoteHit `json:"hits,omitempty"`

	// Message describes it.
	Message string `json:"message"`
}

// QueryProtocolNoteHit is where a call ran into a protocol difference.
type QueryProtocolNoteHit struct {
	// Group is the index of the call in the request.
	Group int `json:"group"`

	// Path is the path of the transaction in the call's group: [0] for the
	// call, [0, i] for its inner transaction i, and so on.
	Path []int `json:"path"`
}
