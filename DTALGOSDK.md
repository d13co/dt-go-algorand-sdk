# dt-go-algorand-sdk

dt-go-algorand-sdk is go-algorand-sdk for [dt-algorand](https://github.com/d13co/dt-algorand/tree/dtalgorand), the go-algorand fork whose time-travel ledger (`EnableTimeTravel: true`) keeps the state of every round since genesis. It is the Go counterpart of the JS SDK [dt-algosdk](https://www.npmjs.com/package/dt-algosdk) ([source](https://github.com/d13co/dt-js-algorand-sdk/tree/dtalgorand)).

It is a drop-in replacement. It keeps the module path `github.com/algorand/go-algorand-sdk/v2`, and every upstream method, type, wire format and error stays as it is. The fork only adds methods, types and helpers, in `dt_*.go` files next to upstream's, plus two fields of upstream's generated simulate models.

It targets dt-algorand dt4 (`v5.0.5-dt4`) and later, as dt-algosdk does: older builds, which lack the capabilities endpoint, some versions options or the `X-Algod-TT-Round` header, are not supported. The state changes of a round and simulate's `StateChangeAppId` need dt6 (`v5.1.1-dt6`). Reads at a round tell stock algod by its answers; to choose a code path beforehand, use [`DTFeatures`](#capabilities-and-detecting-the-node).

- [Installing](#installing)
- [Reads at past rounds](#reads-at-past-rounds)
- [Versions](#versions)
- [Asset holders](#asset-holders)
- [Query](#query)
- [State changes of a round](#state-changes-of-a-round)
- [Capabilities and detecting the node](#capabilities-and-detecting-the-node)
- [Simulate](#simulate)
- [Errors](#errors)
- [Retries](#retries)
- [Response headers and effort](#response-headers-and-effort)
- [Stock algod](#stock-algod)
- [Server behaviour changes](#server-behaviour-changes)
- [Versions of this module](#versions-of-this-module)
- [Developing](#developing)

## Installing

Replace the module in your `go.mod`, keeping your imports:

```sh
go mod edit -replace=github.com/algorand/go-algorand-sdk/v2=github.com/d13co/dt-go-algorand-sdk/v2@v2.14.0-dt.5.1.1.0
go mod tidy
```

That leaves `go.mod` with your `require github.com/algorand/go-algorand-sdk/v2 ...` and a line `replace github.com/algorand/go-algorand-sdk/v2 => github.com/d13co/dt-go-algorand-sdk/v2 v2.14.0-dt.5.1.1.0`. Your code and every dependency that imports go-algorand-sdk then build against the fork.

What to know about `replace`:

- **It is required.** `v2.14.0-dt.*` versions are prereleases, which sort below `v2.14.0`, so a plain `require` never selects them.
- **It works in the main module only.** A library that calls the fork's methods forces every program that uses it to add the same `replace`.
- **`go install tool@version` refuses modules whose `go.mod` has a `replace`.** Build tools that use the fork from a clone, or ship binaries.
- **It overrides the version the other dependencies ask for.** A dependency that needs go-algorand-sdk APIs newer than the fork's base won't compile until the fork merges that release.
- **Pin a version.** A branch works for trying out unreleased changes: `go mod edit -replace=github.com/algorand/go-algorand-sdk/v2=github.com/d13co/dt-go-algorand-sdk/v2@dtalgorand && go mod tidy` resolves it to a pseudo-version in `go.mod`.
- The fork adds no dependencies to go-algorand-sdk's.

## Reads at past rounds

`Round(r)` on these algod requests reads the state as of the end of round `r` (0 is the genesis): `AccountInformation`, `AccountAssetInformation`, `AccountAssetsInformation`, `AccountApplicationsInformation`, `AccountApplicationInformation`, `GetApplicationBoxByName`, `Supply`, `GetAssetByID` and `GetApplicationByID`. `GetApplicationBoxesAt(appID, round)` lists an app's boxes at a round.

```go
acct, err := client.AccountInformation(addr).Exclude([]string{"all"}).Round(50_000_000).Do(ctx)
box, err := client.GetApplicationBoxByName(appID, name).Round(r).Do(ctx)
supply, err := client.Supply().Round(r).Do(ctx)
page, err := client.GetApplicationBoxesAt(appID, r).Limit(100).Do(ctx)
```

`Round` returns a request whose type ends in `At` (`*AccountInformationAt`), with the same setters. Setters on the original request still apply until `Do`, and the `At` request's setters change the original's parameters, so don't set them on one base request from several goroutines.

The node marks every answer it gives as of the round with an `X-Algod-TT-Round` header holding that round, errors after it accepted the round included, so a 404 means "not found at that round". The SDK checks it on every read at a round:

- The header holds the round: the answer stands.
- The header holds another round: a plain error, as the node is broken.
- No header on a 200 or a 404: a `*DTUnsupportedError` with `RequestedRound`. Stock algod ignores `round` and answers for its latest round without the header, as do dt-algorand builds before dt4.
- No header on another error: the error as is. The node doesn't mark malformed requests or its refusals of the round.

A node without time travel refuses past rounds ("requires EnableTimeTravel", "given round is no longer available"), which gives a `*DTUnsupportedError`.

`GetApplicationBoxesAt` is the exception: stock algod answers it for recent rounds, without the header. A 200 without the header is checked by the round its body reports, and a 404 without it is returned as is.

Upstream's `GetApplicationBoxes(appID).Round(r)` is unchanged. It drops round 0; use `GetApplicationBoxesAt` instead.

## Versions

The versions endpoints list the rounds at which an object's stored value changed, or with `Rounds`, the object as of each of a list of rounds. They need `EnableTimeTravel`.

| Method | Versions of | Also takes |
|---|---|---|
| `AccountVersions(addr)` | everything `/v2/accounts/{addr}` returns | `Exclude`, `Keys` |
| `AccountAssetVersions(addr, assetID)` | a holding, and the asset's params if the account created it | |
| `AccountApplicationVersions(addr, appID)` | a local state, and the app's params if the account created it | `Keys`, `Key` (local key) |
| `AssetVersions(assetID)` | an asset's params | |
| `ApplicationVersions(appID)` | an app's params, global state included | `Keys`, `Key` (global key) |
| `ApplicationBoxVersions(appID, name)` | a box | |
| `ApplicationBoxesVersions(appID)` | every box of an app | `Prefix` |

All of them take `MinRound`, `MaxRound`, `Rounds`, `Desc`, `Limit`, `Next` and `Values`. The setters in "Also takes" are on every one of them, but other endpoints answer 400 to `Keys`, `Key` and `Prefix`, and ignore `Exclude`. They are one generic type, `VersionsQuery[R, V]`, with an alias per endpoint (`*algod.AccountVersions`).

```go
// One page.
page, err := client.AssetVersions(assetID).Desc().Limit(10).Do(ctx)

// Every page, or every version, following next tokens.
for page, err := range client.ApplicationBoxesVersions(appID).Prefix([]byte("v|")).Pages(ctx) { ... }
for v, err := range client.ApplicationBoxVersions(appID, name).Values().All(ctx) { ... }

// The state keys each version changed; nil ChangedKeys means not listed.
for v, err := range client.ApplicationVersions(appID).Keys().MinRound(from).All(ctx) { ... }

// The history of one global state key.
for v, err := range client.ApplicationVersions(appID).Key([]byte("total")).Values().All(ctx) { ... }

// The asset as of three rounds: one entry per round, with ChangedRound.
page, err := client.AssetVersions(assetID).Rounds([]uint64{1_000, 30_000_000, 50_000_000}).Do(ctx)
```

- `Pages` and `All` stop at the first error and yield it. They return an error rather than loop when a node answers with the token it was sent.
- Pages can be short, and with `Key` or `Prefix` empty, and still have a next token. Only a missing token ends the listing.
- `Rounds` sorts and deduplicates the rounds (at most 10,000 distinct ones). It can't be combined with `MinRound`, `MaxRound` or `Key`. A very long list makes a long URL, which some proxies refuse with 414.
- Keys, names and prefixes are bytes; the SDK sends them as `b64:` arguments. An empty key is a valid key.
- In the models, values the node may leave out are pointers: `Base`, `Value`, `ChangedKeys`, `ChangedRound`, `KeyValue`, and the object of each version.

A node without time travel, stock algod included, gives a `*DTUnsupportedError`: the routes are registered only with `EnableTimeTravel`. Such a node answers 404, or 400 for the routes whose paths end in an asset or application ID, which its router hands to the plain lookup.

## Asset holders

```go
for h, err := range client.GetAssetHolders(assetID).Round(r).All(ctx) {
	// h.Address, h.Amount, h.IsFrozen
}
```

This lists the accounts that hold an asset at a round, in address order, including zero balances. It needs `TimeTravelHoldersIndex`.

- Pages hold at most `Limit` holders (default 1,000, at most 10,000). They can be short or empty while more remain.
- `Pages` and `All` read every page at the first page's round when `Round` isn't set.
- With a round, the answer is checked by its `X-Algod-TT-Round` header, as for [reads at past rounds](#reads-at-past-rounds).
- A node with time travel or the index off, stock algod included, gives a `*DTUnsupportedError`. `SupportsAssetHolders(ctx)` tells beforehand.
- An index still being built gives a 503 (`IsHistoryUnavailable`).
- An asset that doesn't exist at the round gives a 404.

## Query

`Query` runs read-only calls of a program, or of an existing application, at one or more rounds (`POST /v2/query`). Each call runs alone on the state the round left, without signatures or fees, and returns its logs, cost and error.

```go
res, err := client.Query(models.QueryRequest{
	AppId:  appID,
	Calls:  []models.QueryCall{{Args: [][]byte{selector, arg}}},
	Rounds: []uint64{0, 30_000_000}, // 0: the latest round
}).Do(ctx)
for _, round := range res.Rounds {
	if round.Error != "" { ... }
	for _, call := range round.Calls { _ = call.Logs }
}
```

- The body is sent as JSON, so the node's decoding errors name the field at fault.
- A request body is at most `MaxQueryRequestBytes`.
- A request holds at most `QueryLimits.MaxCalls` calls and lists at most `MaxRounds` rounds, and its calls times its rounds (1 with no rounds) are at most `MaxTotalCalls`: 1,024 by default, so 1,024 calls at one round or 64 at 16 rounds. The client doesn't split requests: take `min(MaxRounds, MaxTotalCalls)` rounds per request, then `min(MaxCalls, MaxTotalCalls / rounds)` calls, with `rounds` that request's own count (the last one may be shorter). Duplicate rounds count, and `Capabilities` fills in `MaxCalls * MaxRounds` for nodes that don't report the total.
- `Protocol` runs each round under a later consensus protocol, as if an upgrade to it took effect right after the round: `latest` (the default), a version string or `vNN`, `round` for the protocol the block after the round really ran, or `future` with the developer API. Each round's `Protocol` (`*QueryProtocolOverride`) says how it applied: `Notes` lists how the two protocols differ, such as `semantics-changed` or data the chain never recorded under the old protocol, and each note's `Hits` lists the calls that ran into it. An unknown name, or `future` without the developer API, fails the request with 400; a protocol the round's next block can't upgrade to gives that round an `Error`, and that block's own protocol runs the round as it is (`Applied` false).
- Results are limited by `QueryLimits.MaxResponseBytes`, where each log counts `QueryLogOverheadBytes` extra. Split a request that gets `IsQueryResponseTooLarge`.
- Stock algod (404), or a node with queries disabled, gives a `*DTUnsupportedError`.

## State changes of a round

`GetTxnStateChangesForRound(round)` (`GET /v2/deltas/{round}/txn/state-changes`) lists the app state changes of each transaction of a round that changed any, top-level and inner, in execution order: globals, local states and boxes, each with its app and its value before and after the transaction. It needs a dt6 node with time travel; `SupportsStateChanges(ctx)` tells beforehand.

```go
res, err := client.GetTxnStateChangesForRound(50_000_000).Txids(groupTxids).Do(ctx)
for _, txn := range res.Txns {
	for _, c := range txn.Changes {
		fmt.Println(txn.Path, c.AppId, c.AppStateType, c.Key, c.Before, c.After)
	}
}
```

- `App(id)`: only that app's changes in `Txns` (0 sends no filter).
- `Txid(id)`, `Txids(ids)`: only these top-level transactions and their inner transactions. Pass all of a group's txids in one call: `Txids` is sent as one comma-separated parameter, as the node requires, and replaces any list set before. The two combine.
- `Include(...)`: also list what each group changed (`IncludeAccounts`, `IncludeAssets`, `IncludeApps`), each program run's budget (`IncludeBudget`), the app state each transaction read (`IncludeReads`), and one group's traces (`IncludeTrace`). `Include` replaces the list set before; `Full()` adds the three group values and `Trace(txid)` adds `trace` and sets `Txid`.
- An empty `Txids` or `Include`, a malformed txid or an unknown include value makes `Do` fail without sending. So does `trace` without exactly one distinct transaction.
- The request is msgpack. The response's `Round` is checked against the round asked for; the answer has no `X-Algod-TT-Round`, as the round is in the path.

A `TxnStateChanges` has `Path` (the index in the block's payset, then the index among the parent's inner transactions at each level, across all its submits), `Txid` (top-level transactions only) and `Changes`, in the order the transaction first changed each key. An `AppStateChange` has `AppId`, `AppStateType` (`g`, `l` or `b`), `Account` (for local state), `Key` (the state key or box name), and `Before` and `After`, `*StateChangeValue`s (`Type` 1 bytes or 2 uint). A nil `Before` means the key did not exist; a nil `After` that it was deleted, its local state cleared, or its app deleted (its boxes stay). A transaction's changes are the net over it and its inner transactions: a key that ends as it began is left out.

The node re-evaluates the round's block for each request, so the filters shrink the answer, not the work. Errors:

- a node without the endpoint (time travel off, stock algod, a node before dt6: the router's 404), or whose ledger has no time travel history: `*DTUnsupportedError`;
- an answer over the capabilities' `StateChanges.MaxResponseBytes` (default 16 MiB): a 400 for `IsStateChangesTooLarge`; narrow it with `App` or fewer `Txids`, or include less;
- a round whose block is not stored, round 0, a round not reached yet, a txid not in the round: the node's 404, returned as is;
- every re-evaluation slot busy: a 503 for `IsNodeBusy`; the time limit: a 504 for `IsTimeLimitExceeded`; a re-evaluation that does not reproduce the round: a 500.

### Everything a group changed

With `IncludeAccounts`, `IncludeAssets` or `IncludeApps` (`Full()` for all three), the answer also has `Groups`: one `GroupStateChanges` per top-level group of the round (only those of the transactions `Txid` and `Txids` ask for, when set), in block order (a transaction not in a group is a group of one), with everything else the group changed, each value before and after the group. App state stays per transaction, in `Txns`; the rest is per group, because the node applies a group, inner transactions included, as one.

| Include | Lists |
|---|---|
| `IncludeAccounts` | `Accounts`: `AccountStateChange` {`Address`, `Before`, `After`: `*AccountState`} |
| `IncludeAssets` | `Holdings`: `AssetHoldingChange` {`Address`, `AssetId`, `Before`, `After`: `*AssetHolding`}; `Assets`: `AssetParamsChange` {`AssetId`, `Before`, `After`: `*AssetParams`} |
| `IncludeApps` | `Apps`: `AppParamsChange` {`AppId`, `Before`, `After`: `*ApplicationParams`, without `GlobalState`}; `LocalStates`: `LocalStateChange` {`Address`, `AppId`, `Before`, `After`: `*ApplicationLocalState`, `Id` and `Schema` without `KeyValue`} |

- A `GroupStateChanges` also has `First` and `Count` (its transactions are at payset indexes `First` to `First+Count-1`, the `Path[0]` of their `Txns` entries), `GroupId` (nil for a lone transaction) and `Txids`, in payset order.
- Only values that differ are listed, sorted by address, then ID. A nil `Before` means it did not exist before the group (a new account, an opt-in, a created asset or app); a nil `After` that the group removed it (a closed account, an opt-out, a destroyed asset, a deleted app).
- `Before` is the state right before the group in the round, and `After` right after it.
- `AccountState` has `Account`'s own fields under the same names: no round, pending rewards or resource lists. `Amount` is as recorded: with the rewards earned until the account was last updated, none pending since, so `After.Amount - Before.Amount` can include rewards no transaction moved. `Status` is `Offline`, `Online` or `Not Participating`.
- `Apps` lists an app only when something other than its global state changed (programs, schemas, extra pages, version). Global state changes are in `Txns`.
- The fee sink is in a group's `Accounts` whenever the group paid fees, and `FeeSink` of the answer is the round's. Changes the block makes outside its transactions (the proposer payout, the rewards pool's) are in no group.
- `Txids` and `Txid` narrow `Groups` to the groups of those transactions; `App` narrows only `Txns`.

Helpers on the models: `res.GroupOf(txid)` (the group holding a top-level transaction), `res.TxnsOf(group)` (the group's `Txns` entries, top-level and inner), and `group.WithoutFeeSink(res.FeeSink)` (a copy without the fee sink in `Accounts`).

### Budget, reads and traces

| Include | Lists |
|---|---|
| `IncludeBudget` | `Budget`: `ProgramBudget` {`Path`, `Txid`, `Kind`, `AppId`, `Cost`, `BudgetBefore`, `BudgetAfter`, `Failed`}, each program run, top-level and inner, in the order they began, a group's logic sigs first |
| `IncludeReads` | `Reads`: `TxnStateReads` {`Path`, `Txid`, `Reads`: `AppStateRead` {`AppId`, `AppStateType`, `Account`, `Key`, `Exists`, `Value`, `Length`}}, each transaction that read app state, each key once, first read first |
| `IncludeTrace` | `Traces`: `TxnExecTrace` {`Txid`, `ExecTrace`: upstream's `SimulationTransactionExecTrace`}, each top-level transaction of the traced transaction's group, in group order; `Programs`: `ProgramDisassembly` {`Hash`, `Lines`: `DisassemblyLine` {`Pc`, `Text`}}, each program they ran |

- They cover the transactions `Txid` and `Txids` ask for, or all of the round's. `App` narrows only `Txns`.
- `ProgramBudget`: `Kind` is `approval`, `clear` or `logic-sig`; `AppId` is the app of an app program (the new app for a create); `Cost` is the program's own, without its inner transactions' programs. `BudgetBefore` and `BudgetAfter` are what the program could still spend when it began and ended: the group's pool where budgets are pooled, which an inner app call adds to while its caller runs and takes from for what it spends beyond that, so `BudgetBefore - BudgetAfter` can differ from `Cost` either way. `BudgetAfter` is negative for a clear state program that ran out of budget. `Failed` is set for a clear state program that rejected or failed, and for every program run inside it, whose inner transactions the block drops: so a failed entry can be any `Kind`.
- `AppStateRead`: `Exists` says whether the key (the box, for a box) existed; `Value` is the global or local value read, when it existed; `Length` is a box's length in bytes. The node leaves out a 0 length, so `Length` is 0 for a missing box, an empty box, and global and local state alike: tell them by `AppStateType` and `Exists`. A key the transaction wrote before reading it shows the value it wrote. Only the read opcodes count (`app_global_get(_ex)`, `app_local_get(_ex)`, `box_get`, `box_extract`, `box_len`).
- Traces are simulate's, with stack, scratch and state changes, each state change with its `AppId`. A transaction that ran no program has an empty trace. A trace's program hashes are the `Hash` of their program in `Programs`; a line's `Pc` is nil for a line without an opcode, such as `#pragma version` or a label.
- `IncludeTrace` needs exactly one transaction and a node with `EnableDeveloperAPI`. A trace is large (about 1.3 MB of JSON for a swap with a few inner calls; msgpack, as the client asks for it, is about a quarter of that) and counts toward `MaxResponseBytes`.

Helpers: `res.ProgramLine(hash, pc)` (the line of the program with that hash holding the opcode at `pc`), `res.BudgetOf(txid)` (a top-level transaction's own program runs: its logic sig, then its app program) and `res.BudgetOfPath(path)` (the same for any transaction).

```go
res, err := client.GetTxnStateChangesForRound(round).Include(algod.IncludeBudget).Trace(txid).Do(ctx)
for _, t := range res.Traces {
	if t.Txid != txid {
		continue
	}
	for _, step := range t.ExecTrace.ApprovalProgramTrace {
		line, _ := res.ProgramLine(t.ExecTrace.ApprovalProgramHash, step.Pc)
		fmt.Println(step.Pc, line.Text, step.StateChanges)
	}
}
for _, b := range res.BudgetOf(txid) {
	fmt.Println(b.Kind, b.Cost, b.BudgetAfter)
}
```

### Nodes without an include value

The client does not cache capabilities, so it doesn't check `Include` against them before sending. Check `SupportsStateChangesInclude(ctx, values...)` once and keep the answer. A node answers 400 to `trace` without `EnableDeveloperAPI`, which `Do` returns as a `*DTUnsupportedError`. An answer without `FeeSink` when group values were asked for, or without `Traces` when `trace` was, comes from a node that ignored `Include` (behind a load balancer, say): it is a `*DTUnsupportedError` too. Every dt6 node takes the other five values, so with `budget` and `reads` alone an empty list is an empty list.

## Capabilities and detecting the node

```go
f, err := client.DTFeatures(ctx)
switch {
case err != nil: // network, auth, ...
case !f.TimeTravel: // stock algod, or a fork without EnableTimeTravel: no reads at a round, versions or holders; queries and box listings at recent rounds only
case f.Capabilities != nil && f.Capabilities.Query != nil: // past rounds, and POST /v2/query
default: // past rounds, no queries
}
```

- `Capabilities()` returns what the node serves (`TimeTravel`, `DeveloperApi`) and its limits: `TimeLimitSeconds` for expensive requests, `Simulate` for simulate, and `Query` for `POST /v2/query`, nil when queries are disabled (`GET /v2/capabilities`).
- `DTFeatures` asks for those capabilities: stock algod has none (404). `Supported` says the node runs dt-algorand, `TimeTravel` that it serves past rounds.
- `SupportsAssetHolders` checks `TimeTravel`, then sends a holders request the node rejects, for its index when off, else for its limit.
- `SupportsStateChanges` checks `TimeTravel` and the capabilities' `StateChanges` (the limit of the state changes of a round: `MaxResponseBytes`, and `Include`, the values `Include` takes). `SupportsStateChangesInclude(ctx, values...)` checks `Include` for each value: `trace` is listed only with `EnableDeveloperAPI`.
- `SupportsStateChangeAppId` checks `DeveloperApi` and the capabilities' `Simulate.ExecTraceConfig`, the fork's exec-trace-config options the node takes: `state-change-app-id`.
- Both return errors other than "not supported" (network, 401, a holders index still being built), and neither caches: keep the answers.
- Whether a node sends the effort header shows only on real requests.

## Simulate

The fork's simulate takes several transaction groups per request: put them in upstream's `SimulateRequest.TxnGroups`. They run in sequence in one block, up to `CapabilitiesResponse.Simulate.MaxTxnGroups`. `Round` reaches back to genesis on a time-travel node.

`Round` after the node's latest round is a 400 from dt6 nodes (`round N is after the latest round L`), as in `Query`. dt5 nodes answered 500, which `RetryTransport` retried until it gave up.

With state change tracing and `StateChangeAppId` (`ExecTraceConfig: models.SimulateTraceConfig{Enable: true, StateChange: true, StateChangeAppId: true}`), each `ApplicationStateOperation` of a trace has `AppId`: the app whose state the opcode changed. It is the running app unless a foreign box opcode (`app_box_*`) changes another app's boxes. The option is opt-in because upstream's `goal` and `libgoal` refuse a response with the extra field. Only dt6 nodes with the developer API take it: stock algod and older nodes answer 400, so check `SupportsStateChangeAppId` first. Without it `AppId` is 0. `StateChangeAppId` and `AppId` are the only fields the fork adds to upstream's generated models (see [Developing](#developing)).

dt-algorand dt5 removed the fork's other simulate options (`rounds`, `parallel-groups`, the validity window modes, `protocol`), which this SDK never sent: use `Query` for calls at several rounds or under a later protocol. A node answers a simulate request that sets one of them with a 400 that names it. dt5 also keeps `allow-unnamed-resources` within the consensus limits. A dt4 node with `SimulateResourceLimitMultiplier` above 1 may report unnamed resources that a group you can submit can't hold, and this SDK no longer reads that factor: a dt4 node's `simulate.max-rounds` and `simulate.resource-limit-multiplier` capabilities are ignored.

## Errors

| Helper | True for |
|---|---|
| `errors.Is(err, algod.ErrDTUnsupported)` | a node without the feature (see each section) |
| `IsNodeBusy` | 503 "the node is busy" (simulate, query) or "every slot for re-evaluating old rounds is in use" (deltas): retry after `RetryAfter` (1 s without it), or on another node |
| `IsTooManyRequests` | 429: the node's limit on requests in flight (`RestConnectionsSoftLimit`, default 1024), or a proxy's rate limit. dt-algorand's has an empty body and `Retry-After: 1`; stock algod's has no `Retry-After`. Retry after `RetryAfter` (1 s without it), or on another node |
| `IsCatchingUp` | 503 during catchpoint catchup |
| `IsHistoryUnavailable` | 503 for history the node can't serve now: box index or holders index incomplete, or history not served |
| `IsTimeLimitExceeded` | 504: the request ran past the node's time limit; split it |
| `IsRoundTooLarge` | 400: a versions request over the node's object limits (an account with too many resources; with `Rounds`, an app with too many boxes, or one round of boxes too large) |
| `IsQueryResponseTooLarge` | 400: query results over the node's limit |
| `IsStateChangesTooLarge` | 400: state changes of a round over the node's `StateChanges.MaxResponseBytes` |
| `IsOnlineStakeNotRecorded` | 404: a supply read at a round whose online stake the history lacks |
| `StatusCode(err)` | the HTTP status of any algod error response |
| `RetryAfter(err)` | the `Retry-After` header of an error from a fork method |

- The helpers work on errors from every method. They read the status from a `*common.ResponseError`, or else from the `common.HTTPError` (or `BadRequest`, `InvalidToken`, `NotFound`, `InternalError`) that upstream returns for every error response.
- Errors of the fork's methods are `*common.ResponseError` values with the response's status and headers. They wrap upstream's error, so `errors.As` still finds a `common.NotFound`, and their message is upstream's.
- `RetryAfter` needs those headers, so it returns false for upstream methods. For those, read the header with `WithResponseInfo` (below).

## Retries

The clients don't retry, as in go-algorand-sdk. `common.RetryTransport` is an `http.RoundTripper` that retries by the policy below, the same as dt-algosdk's `withRetry` ([its Retries section](https://github.com/d13co/dt-js-algorand-sdk/blob/dtalgorand/DTALGOSDK.md#retries)). Pass it to `MakeClientWithTransport` (algod or indexer):

```go
client, err := algod.MakeClientWithTransport(address, token, nil, &common.RetryTransport{
	Policy: &common.RetryPolicy{
		// tell users while a request waits
		OnWait: func(w common.RetryWait) { log.Print(common.FormatRetryWait(w)) },
	},
})
```

`Base` is the transport that sends the requests (`http.DefaultTransport` if nil), and a nil `Policy` is the default one. A `RetryTransport` whose `Base` is another `RetryTransport` leaves the retries to the inner one, so requests are not retried twice over.

### The policy

A failed request (a status of 400 or more, or a transport error) is decided in this order:

| # | Error | Decision |
|---|---|---|
| 1 | the request's context is done | give up (`RetryAborted`) |
| 2 | 503 history unavailable (`IsHistoryUnavailable`) | give up (`RetryHistoryUnavailable`) |
| 3 | 504 from a request under the node's time limit (`common.IsTimeLimitedRequest`) | give up (`RetryTimeLimit`): split or narrow it |
| 4 | a status not in `RetryStatuses` (408, 429, 500, 502, 503, 504), or an error that is no network error | give up (`RetryNotRetryable`) |
| 5 | a transaction submit (`common.IsNonIdempotentRequest`) that the node may have taken | give up (`RetryNotIdempotent`) |
| 6 | `MaxTries` tries made (5) | give up (`RetryMaxTries`) |
| 7 | `Retry-After` above `MaxRetryAfter` (30 s) | give up (`RetryAfterTooLong`) |
| 8 | otherwise | retry: `RetryRateLimited` (429), `RetryNodeBusy` (busy 503), `RetryServerError`, `RetryNetwork` |

The requests under the node's time limit are `POST /v2/query`, `POST /v2/transactions/simulate`, `GET /v2/deltas/{round}` and `GET /v2/deltas/{round}/txn/state-changes`, and `GET` of a `/versions` or `/holders` listing under `/v2/` (not `GET /versions`, algod's version): a retry would repeat all the work and run into the limit again.

A transaction submit (`POST /v2/transactions` and `/v2/transactions/async`) is retried only when the node surely did not take it: a 429, a busy 503, or a connection refused or never made (a dial error: refused, DNS, unreachable network or host, dial timeout). After a lost answer (a reset connection, an EOF, a 502), a retry would get "transaction already in ledger" instead of the transaction ID: check for the transaction instead.

Network errors are dial and DNS errors, `*net.OpError`, EOFs, reset connections and broken pipes, net/http's dropped connections (a keep-alive connection the server closed, an HTTP/2 GOAWAY or stream reset), and timeouts (`net.Error` with `Timeout()`, such as `Dialer.Timeout` or `ResponseHeaderTimeout`). Certificate and TLS errors, a bad address or port, a bare context error, and errors of the client itself (a `*url.Error` is judged by its cause) are not.

The wait before a retry is the longest of:

- the backoff: 0 before the first retry, then 2, 4, 8 s, at most `MaxBackoff` (10 s);
- the node's `Retry-After`, read on 429s and 503s only, up to `MaxRetryAfter` (30 s); an HTTP date is rounded up to the second, as in dt-algosdk;
- `BusyMinDelay` (1 s) for a 429 or a busy 503 without `Retry-After` (or with `Retry-After: 0`), or a refused connection: above `RestConnectionsHardLimit` (2048) the node refuses connections.

The wait ends at once when the request's context is done, and the request returns the context's error. When it gives up, it returns the last try's response or error itself, so the client returns the error it would without retries, and the error helpers work on it as usual. Request bodies are replayed with `Request.GetBody`, which the clients set; a request with a body and no `GetBody` is not retried.

A transport only sees a response up to its headers: the client reads a successful response's body after `RoundTrip` returns. So a connection that fails while a 2xx body downloads (a large versions page or block) returns the error to the caller, where dt-algosdk, which reads the body inside its retries, retries it. Error bodies are read inside `RoundTrip` (up to 64 KiB, to decide) and handed back whole.

### Changing it

`common.RetryPolicy` fields left at their zero value take their defaults (`common.DefaultRetryPolicy()`). A negative duration means none, and a negative `MaxTries` means 1. This differs from dt-algosdk when porting a config: there `maxTries: 0` means 1 (no retries), here `MaxTries: 0` means the default 5, so write 1 to turn retries off. An empty `RetryStatuses` list retries no status in both.

| Field | Default | Meaning |
|---|---|---|
| `MaxTries` | 5 | tries in all, the first included; 1 turns retries off. Not even `Decide` retries past it |
| `MaxBackoff` | 10 s | the longest backoff |
| `MaxRetryAfter` | 30 s | the longest `Retry-After` honoured; a longer one gives up at once |
| `BusyMinDelay` | 1 s | the least wait for a 429 or a busy 503 without `Retry-After` (or with 0), or a refused connection |
| `Backoff(retry)` | `common.DefaultBackoff`: 0, 2, 4, 8 s, ... | the backoff before retry `retry` (from 1), before `MaxBackoff` caps it |
| `RetryStatuses` | 408, 429, 500, 502, 503, 504 | the statuses that may be retried; a non-nil empty list retries none |
| `Decide(err, rc, decision)` | | returns the decision to follow instead of the policy's own, within `MaxTries`; a panic in it (or in `Backoff`) reaches the caller |
| `OnWait(wait)` | | called before every retry; see below |
| `Sleep(ctx, d)` | a timer and `ctx.Done()` | how to wait |

`policy.Decision(err, rc)` gives the decision for one error: a `*common.ResponseError` or `common.HTTPError` (what the clients return), or a transport error. A nil policy is the default one.

### Telling users

`OnWait(wait)` is called before every retry, those with no wait included, with a `common.RetryWait`:

- `Reason`: `RetryRateLimited`, `RetryNodeBusy`, `RetryServerError` or `RetryNetwork`;
- `Delay`, and `Until`;
- `RetryAfter`, when the node sent `Retry-After` (`HasRetryAfter`);
- `Retry` (from 1) and `MaxRetries`;
- `Method`, `Path`, `Status` (0 if there was no response) and `Err`, a `*common.ResponseError` for an error response.

`common.FormatRetryWait(wait)` makes a message of it, such as "The node is rate limiting requests: retrying in 1 s (retry 1 of 4)", the same as dt-algosdk's. The wait is already in the transport's `Waits()` when `OnWait` runs, if it waits at all. A panic in `OnWait` is recovered and ignored, and it may run on several goroutines at once.

To poll instead, `Waits()` returns the requests waiting now, one each, soonest first (a retry with no wait is never in it), and `Waiting()` the one that waits longest. `CloseIdleConnections` reaches `Base`.

## Response headers and effort

`common.WithResponseInfo` makes any request fill in the response's status and headers, without changing what the request returns:

```go
var info common.ResponseInfo
ctx := common.WithResponseInfo(ctx, &info)
res, err := client.Query(req).Do(ctx)
if effort, ok := algod.ParseEffort(info.Header); ok {
	log.Printf("opcodes=%d lookups=%d", effort.Opcodes, effort.Lookups)
}
```

- A node with `EnableEffortHeader` sends `X-Algod-Effort` (`algod.EffortHeader`) with the work a request did. It sends it on simulate, query, versions, deltas, state changes and reads with a round (`round=<latest>` included), but not on the asset holders listing, on reads without a round, or on answers given before any work (503s, most 400s). `ResponseBytes` is 0 for reads at a round. It is meant for a proxy to read and strip.
- A time-travel node marks answers given as of a requested round with `X-Algod-TT-Round` (`algod.TTRoundHeader`), which `algod.ParseTTRound` reads. Reads at a round check it themselves.
- After a request that got no response, `ResponseInfo` is empty. `Pages` and `All` leave the last page's.
- Don't share one `ResponseInfo` between concurrent requests.

## Stock algod

What the fork's methods do against stock algod, and against dt-algorand with a feature off:

| Feature | Stock algod | dt-algorand with the feature off |
|---|---|---|
| `Round` reads | `DTUnsupportedError`: it answers for the latest round (a 404 too), without `X-Algod-TT-Round` | `DTUnsupportedError` ("requires EnableTimeTravel") |
| `GetApplicationBoxesAt` | served for recent rounds (checked by the body's round); `DTUnsupportedError` for rounds it no longer holds | same |
| Versions | `DTUnsupportedError` (404, or its router's 400 for `AssetVersions`, `AccountAssetVersions` and `AccountApplicationVersions`) | `DTUnsupportedError` (the same answers without `EnableTimeTravel`) |
| `GetAssetHolders` | `DTUnsupportedError` (its router's 400) | `DTUnsupportedError` (time travel or index off); 503 while the index is built |
| `Query` | `DTUnsupportedError` (404) | `DTUnsupportedError` (queries disabled) |
| `GetTxnStateChangesForRound` | `DTUnsupportedError` (404) | `DTUnsupportedError` (time travel off: 404); `trace` without the developer API: `DTUnsupportedError` |
| `StateChangeAppId` in simulate | 400 | 400: without the developer API, which every execution trace needs, or before dt6 |
| `Capabilities` | `DTUnsupportedError` (404) | always served |
| `DTFeatures` | zero value | `Supported`, with `TimeTravel` false without `EnableTimeTravel` |
| Multi-group simulate | 400 "expected 1 transaction group" | — |

## Server behaviour changes

Some upstream endpoints answer differently on a dt-algorand node, whatever the SDK:

- `GET /v2/ledger/supply` at a round can answer 404 when the history didn't record that round's online stake (`IsOnlineStakeNotRecorded`).
- `GET /v2/deltas/{round}` rebuilds old rounds, and can answer 503 with `Retry-After` when busy, or 504 at the time limit.
- `GET /v2/deltas/{round}` answers 500 when rebuilding an old round fails, where upstream answers 404.
- `POST /v2/participation/generate/{address}` answers 400 on MainNet, TestNet and BetaNet unless the node sets `DangerouslyEnableParticipation`: the fork is not audited for running validators.
- Above `RestConnectionsSoftLimit` (1024) requests in flight, the node answers 429 (`IsTooManyRequests`), with `Retry-After: 1` and CORS headers from `v5.0.5-dt5`; above `RestConnectionsHardLimit` (2048) it refuses connections. `RetryTransport` retries both.
- Simulate and query requests share a node-wide concurrency limit (`SimulateParallelism`), and delta rebuilds have two slots of their own: a request that can't start within 250 ms gets a 503 with `Retry-After: 1` (`IsNodeBusy`).

## Versions of this module

Versions are `v<go-algorand-sdk version>-dt.<dt-algorand release>.<x.y.z>`, as for dt-algosdk (`3.9.0-dt.4.1.0.0`). For example, `v2.14.0-dt.5.1.0.0`, the first release, is built on go-algorand-sdk v2.14.0, covers the API of dt-algorand dt5 (`v5.0.5-dt5`), and is this SDK's release 1.0.0. `x.y.z` is this SDK's own version: bump `x` for breaking changes, `y` for features and `z` for fixes. It never resets when the go-algorand-sdk version or the dt-algorand release changes: after `v2.14.0-dt.5.1.0.0`, a fix is `v2.14.0-dt.5.1.0.1`, and moving to dt6 with new features is `v2.14.0-dt.6.1.1.0`. dt-algosdk counts its own `x.y.z`, so the two can differ for the same dt-algorand release.

The types in `types` and `protocol` are exported from dt-algorand with go-algorand's `scripts/export_sdk_types.py`, so they match the node's. They can lack fields that upstream added for protocols dt-algorand doesn't have yet: v2.14.0-dt drops four consensus parameters only upstream's vFuture sets. dt6 (on go-algorand 5.1.1) renamed the post-quantum types (`PQSig` to `SaltedKeySig`, `PQScheme` to `SigScheme`, `PQAddressSalt` to `AddressSalt`, `SignedTxn.PQsig` to `SKsig`) with the same wire format: the fork keeps upstream's names, so that code written for go-algorand-sdk compiles, and takes only dt6's new consensus parameters. After the export, restore `types/signature.go` and `types/transaction.go` (`git checkout`); `make x-repo-types` then reports those names until upstream renames them.

### Releasing

1. On `dtalgorand`, with CI green, update the version in README.md and this file.
2. Tag the commit `v<version>` (signed) and push the tag to `d13co`.
3. CI checks the tag is on `dtalgorand`, runs the tests and creates the GitHub release. The Go module proxy serves the tag on first use.

Never move or delete a pushed tag: the module proxy and checksum database keep the first content they saw. Never push upstream's `v2.x` tags, or local tags such as `v2.11.1-fnet5`, to the fork either: a plain `v2.14.0` would become the fork path's `@latest`.

### Merging an upstream release

1. Fetch upstream's tag (`git fetch https://github.com/algorand/go-algorand-sdk tag v<X>`) and merge `v<X>` into `dtalgorand`. Where `types` or `protocol` conflict, take upstream's version: step 3 replaces them.
2. Set `.dt-base` to `v<X>`'s commit, so the guard compares against it.
3. Export dt-algorand's types: in a dt-algorand checkout, run `scripts/export_sdk_types.py` with its `SDK` path set to this checkout, then check them with `SDK_DIR=<this checkout> make x-repo-types`. Do this again whenever dt-algorand merges a go-algorand release that changes these types.
4. Run `./scripts/dt-guard.sh`, `make unit` and `make lint`. A name upstream adds that collides with a dt name fails to compile. Move `SDK_TESTING_COMMIT` in dtsdk.yml to algorand-sdk-testing's newest commit if its scenarios pass.
5. Bump the version's first part.

## Developing

- **Fork code** goes in `dt_*.go` files. Besides the types exported from dt-algorand, the only upstream code the fork edits is a hook in `client/v2/common/common.go` `submitFormRaw`, for `ResponseInfo`, and two fields of the generated models that dt-algorand's simulate traces add: `SimulateTraceConfig.StateChangeAppId` and `ApplicationStateOperation.AppId`. Keep them when regenerating the models. `./scripts/dt-guard.sh` checks this, and that every upstream workflow only runs in upstream's repository. The fork's CI is `.github/workflows/dtsdk.yml`.
- **Unit tests:** `go test ./client/...`. The fork's are in `client/v2/algod/dt_test.go`, `client/v2/algod/dt_state_changes_test.go` (with a msgpack answer encoded by dt-algorand's own types and codec), `client/v2/common/dt_response_test.go` and `client/v2/common/dt_retry_test.go`, against stub servers.
- **Cucumber unit tests:** fetch algorand-sdk-testing's features at the commit CI pins (`SDK_TESTING_COMMIT` in dtsdk.yml), then run `make unit`. Later commits can add scenarios the SDK doesn't implement yet. `test-harness.sh` ignores an `INSTALL_ONLY` set in the environment and starts a Docker sandbox:

  ```sh
  git clone https://github.com/algorand/algorand-sdk-testing test-harness
  git -C test-harness archive 04eea85 features | tar -x -C test/
  make unit
  ```

- **Live tests** against MainNet nodes, skipped unless the variables are set:

  ```sh
  DT_ALGOD_SERVER=http://127.0.0.1:8081 go test -run Live -v ./client/v2/algod/
  DT_STOCK_ALGOD_SERVER=http://127.0.0.1:4001 DT_STOCK_ALGOD_TOKEN=... go test -run LiveStock -v ./client/v2/algod/
  ```

  The stock node can be on any network: `goal network create` with a stock algod gives one.
