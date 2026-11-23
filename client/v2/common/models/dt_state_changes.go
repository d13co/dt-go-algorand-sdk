package models

import (
	"bytes"
	"slices"
)

// This file is part of dt-go-algorand-sdk: the response of GET
// /v2/deltas/{round}/txn/state-changes of a time-travel algod. Unlike the
// generated models, values the node may leave out where zero means something
// else are pointers: a nil Before is a value that did not exist, a nil After
// one that was removed.

// TxnStateChangesResponse lists the app state changes of each transaction of
// a round that changed any, and with include, what each group changed besides,
// the programs' budget, the state each transaction read, and one group's
// execution traces.
type TxnStateChangesResponse struct {
	// Budget lists each program run, top-level and inner, in the order they
	// began, a group's logic sigs first, with include=budget.
	Budget []ProgramBudget `json:"budget,omitempty"`

	// FeeSink is the round's fee sink, with any of include=accounts, assets or
	// apps.
	FeeSink string `json:"fee-sink,omitempty"`

	// Groups lists one entry per top-level group of the round, in block order,
	// with any of include=accounts, assets or apps: only the groups of the
	// transactions Txid and Txids ask for, when set. A transaction not in a
	// group is a group of one.
	Groups []GroupStateChanges `json:"groups,omitempty"`

	// Programs lists each program the traces ran, with include=trace.
	Programs []ProgramDisassembly `json:"programs,omitempty"`

	// Reads lists each transaction that read app state, with include=reads.
	Reads []TxnStateReads `json:"reads,omitempty"`

	// Round is the round.
	Round uint64 `json:"round"`

	// Traces lists each top-level transaction of the traced transaction's
	// group, in group order, with include=trace.
	Traces []TxnExecTrace `json:"traces,omitempty"`

	// Txns lists the transactions that changed app state, top-level and inner,
	// in execution order.
	Txns []TxnStateChanges `json:"txns"`
}

// TxnStateChanges is the app state a transaction changed: the net over it and
// its inner transactions. A key that ends as it began is left out.
type TxnStateChanges struct {
	// Changes lists the changes, in the order the transaction first changed
	// each key.
	Changes []AppStateChange `json:"changes"`

	// Path is the transaction's index in the block's payset, then its index
	// among its parent's inner transactions at each level (across all its
	// parent's submits).
	Path []uint64 `json:"path"`

	// Txid is the transaction ID, for a top-level transaction only.
	Txid string `json:"txid,omitempty"`
}

// AppStateChange is a change of one global, local state or box key.
type AppStateChange struct {
	// Account is the account of a local state.
	Account string `json:"account,omitempty"`

	// After is the value after the transaction; nil when the key was deleted,
	// its local state cleared, or its application deleted (its boxes stay).
	After *StateChangeValue `json:"after,omitempty"`

	// AppId is the application.
	AppId uint64 `json:"app-id"`

	// AppStateType is "g" for global state, "l" for local state or "b" for a
	// box.
	AppStateType string `json:"app-state-type"`

	// Before is the value before the transaction; nil when the key did not
	// exist.
	Before *StateChangeValue `json:"before,omitempty"`

	// Key is the state key, or the box name.
	Key []byte `json:"key"`
}

// StateChangeValue is a TEAL value: Type 1 is bytes, 2 is uint. A box's value
// is bytes.
type StateChangeValue struct {
	// Bytes is the bytes value.
	Bytes []byte `json:"bytes"`

	// Type is 1 for bytes, 2 for uint.
	Type uint64 `json:"type"`

	// Uint is the uint value.
	Uint uint64 `json:"uint"`
}

// GroupStateChanges is what a top-level group changed other than app state,
// each value before and after the group. Only values that differ are listed,
// sorted by address, then ID. A list not asked for with include is empty.
type GroupStateChanges struct {
	// Accounts lists the accounts that changed, with include=accounts.
	Accounts []AccountStateChange `json:"accounts,omitempty"`

	// Apps lists the applications whose params changed, other than their
	// global state (in Txns), with include=apps.
	Apps []AppParamsChange `json:"apps,omitempty"`

	// Assets lists the assets whose params changed, with include=assets.
	Assets []AssetParamsChange `json:"assets,omitempty"`

	// Count is the number of top-level transactions of the group.
	Count uint64 `json:"count"`

	// First is the payset index of the group's first transaction: its
	// transactions are at First to First+Count-1, the Path[0] of their Txns
	// entries.
	First uint64 `json:"first"`

	// GroupId is the group's ID; nil for a lone transaction.
	GroupId []byte `json:"group-id,omitempty"`

	// Holdings lists the asset holdings that changed, with include=assets.
	Holdings []AssetHoldingChange `json:"holdings,omitempty"`

	// LocalStates lists the local states that changed other than their
	// key-values (in Txns), with include=apps.
	LocalStates []LocalStateChange `json:"local-states,omitempty"`

	// Txids lists the group's top-level transactions, in payset order.
	Txids []string `json:"txids"`
}

// AccountStateChange is a change of an account's own fields.
type AccountStateChange struct {
	// Address is the account.
	Address string `json:"address"`

	// After is the account after the group; nil when the group closed it.
	After *AccountState `json:"after,omitempty"`

	// Before is the account before the group; nil when it did not exist.
	Before *AccountState `json:"before,omitempty"`
}

// AccountState is an account's own fields, under Account's names: without
// the round, pending rewards or the lists of resources.
type AccountState struct {
	// Amount is the balance in microalgos as recorded: with the rewards earned
	// until the account was last updated, none pending since.
	Amount uint64 `json:"amount"`

	// AppsTotalExtraPages is the extra program pages of the applications it
	// created.
	AppsTotalExtraPages uint64 `json:"apps-total-extra-pages,omitempty"`

	// AppsTotalSchema is the sum of the local and global schemas of its
	// applications.
	AppsTotalSchema ApplicationStateSchema `json:"apps-total-schema,omitempty"`

	// AuthAddr is the address against which signing is checked, if rekeyed.
	AuthAddr string `json:"auth-addr,omitempty"`

	// IncentiveEligible is whether it can receive block incentives.
	IncentiveEligible bool `json:"incentive-eligible,omitempty"`

	// LastHeartbeat is the round of its last heartbeat.
	LastHeartbeat uint64 `json:"last-heartbeat,omitempty"`

	// LastProposed is the round it last proposed a block.
	LastProposed uint64 `json:"last-proposed,omitempty"`

	// MinBalance is its minimum balance in microalgos.
	MinBalance uint64 `json:"min-balance"`

	// Participation holds its participation keys, if registered online.
	Participation AccountParticipation `json:"participation,omitempty"`

	// RewardBase is used in the rewards computation.
	RewardBase uint64 `json:"reward-base,omitempty"`

	// Rewards is the rewards it received, without pending rewards.
	Rewards uint64 `json:"rewards,omitempty"`

	// Status is "Offline", "Online" or "Not Participating".
	Status string `json:"status"`

	// TotalAppsOptedIn is the number of applications it opted into.
	TotalAppsOptedIn uint64 `json:"total-apps-opted-in"`

	// TotalAssetsOptedIn is the number of assets it opted into.
	TotalAssetsOptedIn uint64 `json:"total-assets-opted-in"`

	// TotalBoxBytes is the bytes of the boxes of the applications it created.
	TotalBoxBytes uint64 `json:"total-box-bytes,omitempty"`

	// TotalBoxes is the number of boxes of the applications it created.
	TotalBoxes uint64 `json:"total-boxes,omitempty"`

	// TotalCreatedApps is the number of applications it created.
	TotalCreatedApps uint64 `json:"total-created-apps"`

	// TotalCreatedAssets is the number of assets it created.
	TotalCreatedAssets uint64 `json:"total-created-assets"`
}

// AssetHoldingChange is a change of an account's holding of an asset.
type AssetHoldingChange struct {
	// Address is the account.
	Address string `json:"address"`

	// After is the holding after the group; nil when the account opted out.
	After *AssetHolding `json:"after,omitempty"`

	// AssetId is the asset.
	AssetId uint64 `json:"asset-id"`

	// Before is the holding before the group; nil when the account was not
	// opted in.
	Before *AssetHolding `json:"before,omitempty"`
}

// AssetParamsChange is a change of an asset's params.
type AssetParamsChange struct {
	// After is the params after the group; nil when the group destroyed it.
	After *AssetParams `json:"after,omitempty"`

	// AssetId is the asset.
	AssetId uint64 `json:"asset-id"`

	// Before is the params before the group; nil when the group created it.
	Before *AssetParams `json:"before,omitempty"`
}

// AppParamsChange is a change of an application's params other than its global
// state: programs, schemas, extra pages or version.
type AppParamsChange struct {
	// After is the params after the group, without GlobalState; nil when the
	// group deleted it.
	After *ApplicationParams `json:"after,omitempty"`

	// AppId is the application.
	AppId uint64 `json:"app-id"`

	// Before is the params before the group, without GlobalState; nil when the
	// group created it.
	Before *ApplicationParams `json:"before,omitempty"`
}

// LocalStateChange is an opt-in or opt-out of an application, or another
// change of a local state than its key-values.
type LocalStateChange struct {
	// Address is the account.
	Address string `json:"address"`

	// After is the local state after the group, Id and Schema without KeyValue;
	// nil when the account opted out or cleared it.
	After *ApplicationLocalState `json:"after,omitempty"`

	// AppId is the application.
	AppId uint64 `json:"app-id"`

	// Before is the local state before the group, Id and Schema without
	// KeyValue; nil when the account was not opted in.
	Before *ApplicationLocalState `json:"before,omitempty"`
}

// ProgramBudget is one program run: its cost, and the budget it could still
// spend when it began and ended.
type ProgramBudget struct {
	// AppId is the application of an app program (the new one for a create).
	AppId uint64 `json:"app-id,omitempty"`

	// BudgetAfter is the budget left when it ended: the group's pool where
	// budgets are pooled. Negative for a clear state program that ran out.
	BudgetAfter int64 `json:"budget-after"`

	// BudgetBefore is the budget left when it began. An inner app call adds to
	// the pool while its caller runs, and takes from it what it spends beyond
	// that, so BudgetBefore-BudgetAfter can differ from Cost either way.
	BudgetBefore int64 `json:"budget-before"`

	// Cost is the program's own cost, without its inner transactions'
	// programs, which have entries of their own.
	Cost uint64 `json:"cost"`

	// Failed is set for a clear state program that rejected or failed (the
	// only failed program a block can keep), and for every program run inside
	// it, whose inner transactions the block drops.
	Failed bool `json:"failed,omitempty"`

	// Kind is "approval", "clear" or "logic-sig".
	Kind string `json:"kind"`

	// Path is the transaction's path, as in TxnStateChanges.
	Path []uint64 `json:"path"`

	// Txid is the transaction ID, for a top-level transaction only.
	Txid string `json:"txid,omitempty"`
}

// TxnStateReads is the app state a transaction read.
type TxnStateReads struct {
	// Path is the transaction's path, as in TxnStateChanges.
	Path []uint64 `json:"path"`

	// Reads lists the keys it read, once each, first read first.
	Reads []AppStateRead `json:"reads"`

	// Txid is the transaction ID, for a top-level transaction only.
	Txid string `json:"txid,omitempty"`
}

// AppStateRead is a global, local state or box key a transaction read with
// app_global_get(_ex), app_local_get(_ex), box_get, box_extract or box_len. A
// key the transaction wrote before reading it shows the value it wrote.
type AppStateRead struct {
	// Account is the account of a local state.
	Account string `json:"account,omitempty"`

	// AppId is the application.
	AppId uint64 `json:"app-id"`

	// AppStateType is "g" for global state, "l" for local state or "b" for a
	// box.
	AppStateType string `json:"app-state-type"`

	// Exists is whether the key (the box, for a box) existed.
	Exists bool `json:"exists"`

	// Key is the state key, or the box name.
	Key []byte `json:"key"`

	// Length is a box's length in bytes; 0 for a box that did not exist, and
	// for global and local state. The node leaves out a 0 length, so an empty
	// box and a missing one differ only by Exists.
	Length uint64 `json:"length,omitempty"`

	// Value is the global or local value read, when it existed.
	Value *StateChangeValue `json:"value,omitempty"`
}

// TxnExecTrace is a top-level transaction's execution trace, as simulate's
// with stack, scratch and state changes, each state change with its AppId.
// A transaction that ran no program has an empty trace.
type TxnExecTrace struct {
	// ExecTrace is the trace. Its program hashes are the Hash of their program
	// in TxnStateChangesResponse.Programs.
	ExecTrace SimulationTransactionExecTrace `json:"exec-trace"`

	// Txid is the transaction ID.
	Txid string `json:"txid"`
}

// ProgramDisassembly is a program a trace ran, disassembled.
type ProgramDisassembly struct {
	// Hash is the program's hash, as in the traces.
	Hash []byte `json:"hash"`

	// Lines lists the disassembly's lines.
	Lines []DisassemblyLine `json:"lines"`
}

// DisassemblyLine is a line of a disassembly.
type DisassemblyLine struct {
	// Pc is the program counter of the line's opcode; nil for a line without
	// one, such as #pragma version or a label.
	Pc *uint64 `json:"pc,omitempty"`

	// Text is the line.
	Text string `json:"text"`
}

// StateChangesLimits is a node's limit on GET
// /v2/deltas/{round}/txn/state-changes.
type StateChangesLimits struct {
	// Include lists the values the include parameter takes; empty from a node
	// that ignores include.
	Include []string `json:"include,omitempty"`

	// MaxResponseBytes is the most bytes an answer may take, encoded; a larger
	// one fails with 400 (IsStateChangesTooLarge). 0 means no limit.
	MaxResponseBytes uint64 `json:"max-response-bytes"`
}

// GroupOf returns the group holding a top-level transaction, and false if no
// group lists it (or the response has no Groups: ask with Include).
func (r *TxnStateChangesResponse) GroupOf(txid string) (*GroupStateChanges, bool) {
	for i := range r.Groups {
		if slices.Contains(r.Groups[i].Txids, txid) {
			return &r.Groups[i], true
		}
	}
	return nil, false
}

// TxnsOf returns the Txns entries of a group's transactions, top-level and
// inner, in execution order.
func (r *TxnStateChangesResponse) TxnsOf(g *GroupStateChanges) []TxnStateChanges {
	var txns []TxnStateChanges
	for _, t := range r.Txns {
		if len(t.Path) > 0 && t.Path[0] >= g.First && t.Path[0]-g.First < g.Count {
			txns = append(txns, t)
		}
	}
	return txns
}

// WithoutFeeSink returns a copy of the group without the fee sink in
// Accounts, which every group that paid fees lists. Pass the response's
// FeeSink.
func (g GroupStateChanges) WithoutFeeSink(feeSink string) GroupStateChanges {
	g.Accounts = slices.DeleteFunc(slices.Clone(g.Accounts), func(a AccountStateChange) bool { return a.Address == feeSink })
	return g
}

// BudgetOf returns a top-level transaction's own program runs: its logic
// sig, then its app program, not those of its inner transactions.
func (r *TxnStateChangesResponse) BudgetOf(txid string) []ProgramBudget {
	var runs []ProgramBudget
	for _, b := range r.Budget {
		if b.Txid == txid && len(b.Path) == 1 {
			runs = append(runs, b)
		}
	}
	return runs
}

// BudgetOfPath returns the program runs of the transaction at a path (see
// TxnStateChanges.Path), top-level or inner: its logic sig, then its app
// program, not those of its inner transactions.
func (r *TxnStateChangesResponse) BudgetOfPath(path []uint64) []ProgramBudget {
	var runs []ProgramBudget
	for _, b := range r.Budget {
		if slices.Equal(b.Path, path) {
			runs = append(runs, b)
		}
	}
	return runs
}

// ProgramLine returns the line of the program with this hash (a trace's
// ApprovalProgramHash, ClearStateProgramHash or LogicSigHash) that holds the
// opcode at pc, and false for an unknown program or a pc inside an opcode's
// immediates.
func (r *TxnStateChangesResponse) ProgramLine(hash []byte, pc uint64) (DisassemblyLine, bool) {
	for _, p := range r.Programs {
		if !bytes.Equal(p.Hash, hash) {
			continue
		}
		for _, l := range p.Lines {
			if l.Pc != nil && *l.Pc == pc {
				return l, true
			}
		}
		return DisassemblyLine{}, false
	}
	return DisassemblyLine{}, false
}
