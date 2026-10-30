# go-dtalgorand-sdk

## go-algorand-sdk for time-travel algod

This is go-dtalgorand-sdk, a fork of [go-algorand-sdk](https://github.com/algorand/go-algorand-sdk) v2.14.0 for a time-travel algod: [dt-algorand](https://github.com/d13co/dt-algorand/tree/dtalgorand), the go-algorand fork run with `EnableTimeTravel: true`, which keeps the state of every round since genesis. It is a drop-in replacement: it keeps the module path `github.com/algorand/go-algorand-sdk/v2`, everything in go-algorand-sdk behaves the same, and the additions are new methods. Its transaction, block and consensus types are exported from dt-algorand, so they match the node's: they lack a few consensus parameters that only upstream's future protocol sets. The main ones, all on the algod client:

- **Reads at past rounds**: `.Round(r)` on account, asset, application, box and supply reads, and `GetApplicationBoxesAt`.
- **History**: `...Versions` methods list the rounds at which an account, asset, application, box, or any of an app's boxes changed, with paging iterators and values; `Rounds(...)` gives each object as of a set of rounds, `Keys()` the state keys each version changed, and `Key()` / `Prefix()` the history of one state key or box map.
- **Asset holders**: `GetAssetHolders` lists every holder of an asset at a round.
- **Query**: `Query` runs read-only TEAL calls (one program or deployed app, many calls) at several rounds per request through `POST /v2/query`.
- **Limits and detection**: `SimulateLimits`, `DTFeatures` to tell a dt-algorand node that serves past rounds from stock algod, `SupportsAssetHolders`, and a `DTUnsupportedError` when a node has a feature off, so code can fall back.
- **Errors and headers**: `IsNodeBusy`, `RetryAfter`, `IsTimeLimitExceeded`, `IsHistoryUnavailable` and more; `common.WithResponseInfo` and `ParseEffort` for response headers such as `X-Algod-Effort`.

See **[DTALGOSDK.md](DTALGOSDK.md)** for the details and examples. Source and issues: [d13co/go-dtalgorand-sdk](https://github.com/d13co/go-dtalgorand-sdk) (branch `dtalgorand`).

### Using go-dtalgorand-sdk in place of go-algorand-sdk

Keep your imports, and replace the module in your `go.mod` (pin an exact `v*-dt.*` version):

```sh
go mod edit -replace=github.com/algorand/go-algorand-sdk/v2=github.com/d13co/go-dtalgorand-sdk/v2@v2.14.0-dt.3.0.0.1
go mod tidy
```

```go
client, _ := algod.MakeClient("http://localhost:8080", token)
acct, err := client.AccountInformation(addr).Round(50_000_000).Do(ctx)
for v, err := range client.ApplicationVersions(appID).Keys().All(ctx) {
	// ...
}
```

The rest of this README is go-algorand-sdk's.

---

# go-algorand-sdk

[![Go Report Card](https://goreportcard.com/badge/github.com/algorand/go-algorand-sdk)](https://goreportcard.com/report/github.com/algorand/go-algorand-sdk/v2)
[![GoDoc](https://godoc.org/github.com/algorand/go-algorand-sdk?status.svg)](https://godoc.org/github.com/algorand/go-algorand-sdk/v2)

The Algorand golang SDK provides:

- HTTP clients for the algod (agreement) and kmd (key management) APIs
- Standalone functionality for interacting with the Algorand protocol, including transaction signing, message encoding, etc.

> [!NOTE]
> Support for falcon signing and verification is gated under the `falcon` tag since it requires using the [algorand/falcon](https://github.com/algorand/falcon) cgo library. An SDK built without `falcon` can still use `transaction.SignTransaction` with a `transaction.PQAccountTransactionSigner`, or a logicsig delegated via its `SignDelegationTo` method. Creating a `transaction.PQAccountTransactionSigner` requires an implementation of the `PQSigner` interface.
>
> Two further notes on post-quantum support without the `falcon` tag: `crypto.VerifyLogicSig` only checks the delegating address of a PQ-delegated LogicSig, since verifying the delegation signature itself needs the falcon library; and `transaction.PQEmptyTransactionSigner` produces a PQ envelope with no signature bytes, which lets `algod` charge the post-quantum fee surcharge when simulating with `allowEmptySignatures` without the cost of generating a real signature.

## Documentation

Full documentation is available [on pkg.go.dev](https://pkg.go.dev/github.com/algorand/go-algorand-sdk/v2). You can also self-host the documentation by running `godoc -http=:8099` and visiting `http://localhost:8099/pkg/github.com/algorand/go-algorand-sdk/v2` in your web browser.

Additional developer documentation and examples can be found on [developer.algorand.org](https://developer.algorand.org/docs/sdks/go/)

## Package Overview

In `client/`, the `kmd` packages provide HTTP clients for the Key Management Daemon. It is responsible for managing spending key material, signing transactions, and managing wallets.
In `client/v2` the `algod` package contains a client for the Algorand protocol daemon HTTP API. You can use it to check the status of the blockchain, read a block, look at transactions, or submit a signed transaction.
In `client/v2` the `indexer` package contains a client for the Algorand Indexer API. You can use it to query historical transactions or make queries about the current state of the chain.

`transaction` package contains Transaction building utility functions.

`types` contains the data structures you'll use when interacting with the network, including addresses, transactions, multisig signatures, etc. 

`encoding` contains the `json` and `msgpack` packages, which can be used to serialize messages for the algod/kmd APIs and the network.

`mnemonic` contains support for turning 32-byte keys into checksummed, human-readable mnemonics (and going from mnemonics back to keys).

## SDK Development

Run tests with `make docker-test`. To set up the sandbox-based test harness without standing up the go-algorand docker image use `make harness`.

We use golangci-lint to run linters on our codebase. Please run `make lint` before you submit a PR to make sure it conforms to linter standards.

We use cucumber testing for all of our SDKs, including this one. Please refer to [algorand-sdk-testing](https://github.com/algorand/algorand-sdk-testing#readme) for guidance and existing tests that you may need to update. Depending on the type of update you with to contribute, you may also need to have corresponding updates in the other SDKs (Java, JS, and Python). Feel welcome to ask for collaboration on that front. 

## Quick Start

To download the SDK, open a terminal and use the `go get` command.

```sh
go get -u github.com/algorand/go-algorand-sdk/...
```
