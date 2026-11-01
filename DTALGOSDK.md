# go-dtalgorand-sdk

go-dtalgorand-sdk is go-algorand-sdk for [dt-algorand](https://github.com/d13co/dt-algorand/tree/dtalgorand), the go-algorand fork whose time-travel ledger (`EnableTimeTravel: true`) keeps the state of every round since genesis. It is the Go counterpart of the JS SDK [dt-algosdk](https://www.npmjs.com/package/dt-algosdk) ([source](https://github.com/d13co/dt-js-algorand-sdk/tree/dtalgorand)).

It is a drop-in replacement. It keeps the module path `github.com/algorand/go-algorand-sdk/v2`, and every upstream method, type, wire format and error stays as it is. The fork only adds methods, types and helpers, in `dt_*.go` files next to upstream's.

It targets dt-algorand dt4 (`v5.0.5-dt4`) and later, as dt-algosdk does: older builds, which lack the capabilities endpoint, some versions options or the `X-Algod-TT-Round` header, are not supported. Reads at a round tell stock algod by its answers; to choose a code path beforehand, use [`DTFeatures`](#capabilities-and-detecting-the-node).

- [Installing](#installing)
- [Reads at past rounds](#reads-at-past-rounds)
- [Versions](#versions)
- [Asset holders](#asset-holders)
- [Query](#query)
- [Capabilities and detecting the node](#capabilities-and-detecting-the-node)
- [Simulate](#simulate)
- [Errors](#errors)
- [Response headers and effort](#response-headers-and-effort)
- [Stock algod](#stock-algod)
- [Server behaviour changes](#server-behaviour-changes)
- [Versions of this module](#versions-of-this-module)
- [Developing](#developing)

## Installing

Replace the module in your `go.mod`, keeping your imports:

```sh
go mod edit -replace=github.com/algorand/go-algorand-sdk/v2=github.com/d13co/go-dtalgorand-sdk/v2@v2.14.0-dt.4.1.0.0
go mod tidy
```

That leaves `go.mod` with your `require github.com/algorand/go-algorand-sdk/v2 ...` and a line `replace github.com/algorand/go-algorand-sdk/v2 => github.com/d13co/go-dtalgorand-sdk/v2 v2.14.0-dt.4.1.0.0`. Your code and every dependency that imports go-algorand-sdk then build against the fork.

What to know about `replace`:

- **It is required.** `v2.14.0-dt.*` versions are prereleases, which sort below `v2.14.0`, so a plain `require` never selects them.
- **It works in the main module only.** A library that calls the fork's methods forces every program that uses it to add the same `replace`.
- **`go install tool@version` refuses modules whose `go.mod` has a `replace`.** Build tools that use the fork from a clone, or ship binaries.
- **It overrides the version the other dependencies ask for.** A dependency that needs go-algorand-sdk APIs newer than the fork's base won't compile until the fork merges that release.
- **Pin a version.** A branch works for trying out unreleased changes: `go mod edit -replace=github.com/algorand/go-algorand-sdk/v2=github.com/d13co/go-dtalgorand-sdk/v2@dtalgorand && go mod tidy` resolves it to a pseudo-version in `go.mod`.
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
- Results are limited by `QueryLimits.MaxResponseBytes`, where each log counts `QueryLogOverheadBytes` extra. Split a request that gets `IsQueryResponseTooLarge`.
- Stock algod (404), or a node with queries disabled, gives a `*DTUnsupportedError`.

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
- Both return errors other than "not supported" (network, 401, a holders index still being built), and neither caches: keep the answers.
- Whether a node sends the effort header shows only on real requests.

## Simulate

The fork's simulate takes several transaction groups per request: put them in upstream's `SimulateRequest.TxnGroups`. They run in sequence in one block, up to `CapabilitiesResponse.Simulate.MaxTxnGroups`. `Round` reaches back to genesis on a time-travel node.

The fork's other simulate options (`rounds`, `parallel-groups`, the validity window modes, `protocol`) are not in this SDK. The fork plans to move them to `/v2/query`.

## Errors

| Helper | True for |
|---|---|
| `errors.Is(err, algod.ErrDTUnsupported)` | a node without the feature (see each section) |
| `IsNodeBusy` | 503 "the node is busy" (simulate, query) or "every slot for re-evaluating old rounds is in use" (deltas): retry after `RetryAfter`, or on another node |
| `IsCatchingUp` | 503 during catchpoint catchup |
| `IsHistoryUnavailable` | 503 for history the node can't serve now: box index or holders index incomplete, or history not served |
| `IsTimeLimitExceeded` | 504: the request ran past the node's time limit; split it |
| `IsRoundTooLarge` | 400: a versions request over the node's object limits (an account with too many resources; with `Rounds`, an app with too many boxes, or one round of boxes too large) |
| `IsQueryResponseTooLarge` | 400: query results over the node's limit |
| `IsOnlineStakeNotRecorded` | 404: a supply read at a round whose online stake the history lacks |
| `StatusCode(err)` | the HTTP status of any algod error response |
| `RetryAfter(err)` | the `Retry-After` header of an error from a fork method |

- The helpers work on errors from every method. They read the status from a `*common.ResponseError`, or else from the `common.HTTPError` (or `BadRequest`, `InvalidToken`, `NotFound`, `InternalError`) that upstream returns for every error response.
- Errors of the fork's methods are `*common.ResponseError` values with the response's status and headers. They wrap upstream's error, so `errors.As` still finds a `common.NotFound`, and their message is upstream's.
- `RetryAfter` needs those headers, so it returns false for upstream methods. For those, read the header with `WithResponseInfo` (below).

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

- A node with `EnableEffortHeader` sends `X-Algod-Effort` (`algod.EffortHeader`) with the work a request did. It sends it on simulate, query, versions, deltas and reads with a round (`round=<latest>` included), but not on the asset holders listing, on reads without a round, or on answers given before any work (503s, most 400s). `ResponseBytes` is 0 for reads at a round. It is meant for a proxy to read and strip.
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
| `Capabilities` | `DTUnsupportedError` (404) | always served |
| `DTFeatures` | zero value | `Supported`, with `TimeTravel` false without `EnableTimeTravel` |
| Multi-group simulate | 400 "expected 1 transaction group" | — |

## Server behaviour changes

Some upstream endpoints answer differently on a dt-algorand node, whatever the SDK:

- `GET /v2/ledger/supply` at a round can answer 404 when the history didn't record that round's online stake (`IsOnlineStakeNotRecorded`).
- `GET /v2/deltas/{round}` rebuilds old rounds, and can answer 503 with `Retry-After` when busy, or 504 at the time limit.
- `GET /v2/deltas/{round}` answers 500 when rebuilding an old round fails, where upstream answers 404.
- `POST /v2/participation/generate/{address}` answers 400 on MainNet, TestNet and BetaNet unless the node sets `DangerouslyEnableParticipation`: the fork is not audited for running validators.
- Simulate and query requests share a node-wide concurrency limit (`SimulateParallelism`), and delta rebuilds have two slots of their own: a request that can't start within 250 ms gets a 503 with `Retry-After: 1` (`IsNodeBusy`).

## Versions of this module

Versions are `v<go-algorand-sdk version>-dt.<dt-algorand release>.<x.y.z>`, as for dt-algosdk (`3.9.0-dt.4.1.0.0`). For example, `v2.14.0-dt.4.1.0.0` is built on go-algorand-sdk v2.14.0, covers the API of dt-algorand dt4 (`v5.0.5-dt4`), and is this SDK's release 1.0.0. `x.y.z` is this SDK's own version: bump `x` for breaking changes, `y` for features and `z` for fixes. It never resets when the go-algorand-sdk version or the dt-algorand release changes: after `v2.14.0-dt.4.1.0.0`, a fix is `v2.14.0-dt.4.1.0.1`, and moving to dt5 with new features is `v2.14.0-dt.5.1.1.0`.

The types in `types` and `protocol` are exported from dt-algorand with go-algorand's `scripts/export_sdk_types.py`, so they match the node's. They can lack fields that upstream added for protocols dt-algorand doesn't have yet: v2.14.0-dt drops four consensus parameters only upstream's vFuture sets.

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

- **Fork code** goes in `dt_*.go` files. Besides the types exported from dt-algorand, the only upstream code the fork edits is a hook in `client/v2/common/common.go` `submitFormRaw`, for `ResponseInfo`. `./scripts/dt-guard.sh` checks this, and that every upstream workflow only runs in upstream's repository. The fork's CI is `.github/workflows/dtsdk.yml`.
- **Unit tests:** `go test ./client/...`. The fork's are in `client/v2/algod/dt_test.go` and `client/v2/common/dt_response_test.go`, against stub servers.
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
