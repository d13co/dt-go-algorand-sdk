package algod

// Tests against live MainNet nodes, skipped unless their URLs are set:
//
//	DT_ALGOD_SERVER=http://127.0.0.1:8081 go test -run Live ./client/v2/algod/
//
// DT_ALGOD_SERVER is a time-travel node (EnableTimeTravel), DT_ALGOD_TOKEN its
// token if it needs one. DT_STOCK_ALGOD_SERVER (and DT_STOCK_ALGOD_TOKEN) is
// a stock algod on any network, for the tests of how the SDK detects one.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MainNet objects with history.
const (
	liveUSDC        = 31566704
	liveUSDCReserve = "2UEQTE5QDNXPI7M3TU44G6SYKLFWLPQO7EBZM7K7MHMQQMFI4QJPLHQFHM"
	liveXGov        = 3147789458
	liveXGovCreator = "I7OP7WFSK57IFDHJA6DM5TJC2IFY4M3XSBV4R4PVOV4YWF7K57BZFUVQ5E"
	liveNFD         = 760937186
	// A round before the xGov app existed, after USDC was created.
	livePast = 50_000_000
)

func liveClient(t *testing.T, env, tokenEnv string, mainnet bool) *Client {
	t.Helper()
	server := os.Getenv(env)
	if server == "" {
		t.Skipf("%s is not set", env)
	}
	c, err := MakeClient(server, os.Getenv(tokenEnv))
	require.NoError(t, err)
	params, err := c.SuggestedParams().Do(context.Background())
	require.NoError(t, err)
	if mainnet && params.GenesisID != "mainnet-v1.0" {
		t.Skipf("%s is not a MainNet node (%s)", env, params.GenesisID)
	}
	return c
}

func TestLiveTimeTravel(t *testing.T) {
	c := liveClient(t, "DT_ALGOD_SERVER", "DT_ALGOD_TOKEN", true)
	ctx := context.Background()
	status, err := c.Status().Do(ctx)
	require.NoError(t, err)
	latest := status.LastRound

	t.Run("features", func(t *testing.T) {
		f, err := c.DTFeatures(ctx)
		require.NoError(t, err)
		require.True(t, f.Supported)
		require.True(t, f.TimeTravel)
		require.NotNil(t, f.Capabilities)
		assert.GreaterOrEqual(t, f.Capabilities.Simulate.MaxTxnGroups, uint64(1))
	})

	t.Run("reads at a round", func(t *testing.T) {
		acct, err := c.AccountInformation(liveXGovCreator).Exclude([]string{"all"}).Round(livePast + 3_000_000).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(livePast+3_000_000), acct.Round)

		before, err := c.AccountInformation(liveXGovCreator).Round(1000).Exclude([]string{"all"}).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(1000), before.Round)
		assert.Zero(t, before.Amount, "an account before it existed is empty")

		holding, err := c.AccountAssetInformation(liveUSDCReserve, liveUSDC).Round(livePast).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(livePast), holding.Round)
		assert.Equal(t, uint64(liveUSDC), holding.AssetHolding.AssetId)
		assert.Equal(t, liveUSDCReserve, holding.CreatedAsset.Creator, "the creator sees the params")

		assets, err := c.AccountAssetsInformation(liveUSDCReserve).Round(livePast).Limit(2).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(livePast), assets.Round)
		assert.NotEmpty(t, assets.AssetHoldings)

		at := latest - 10
		apps, err := c.AccountApplicationsInformation(liveXGovCreator).Round(at).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, at, apps.Round)
		app, err := c.AccountApplicationInformation(liveXGovCreator, liveXGov).Round(at).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, at, app.Round)
		assert.Equal(t, liveXGovCreator, app.CreatedApp.Creator, "the creator sees the params")

		supply, err := c.Supply().Round(livePast).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(livePast), supply.Current_round)

		var info common.ResponseInfo
		asset, err := c.GetAssetByID(liveUSDC).Round(livePast).Do(common.WithResponseInfo(ctx, &info))
		require.NoError(t, err)
		assert.Equal(t, uint64(liveUSDC), asset.Index)
		ttRound, ok := ParseTTRound(info.Header)
		assert.True(t, ok, "the node marks the answer")
		assert.Equal(t, uint64(livePast), ttRound)

		_, err = c.GetAssetByID(liveUSDC).Round(1000).Do(ctx)
		code, _ := StatusCode(err)
		assert.Equal(t, 404, code, "an asset before its creation")
		assert.NotErrorIs(t, err, ErrDTUnsupported)

		_, err = c.AccountInformation(liveXGovCreator).Round(latest + 100_000).Do(ctx)
		assert.Error(t, err, "a round after the latest")
		assert.NotErrorIs(t, err, ErrDTUnsupported)
	})

	t.Run("boxes", func(t *testing.T) {
		page, err := c.ApplicationBoxesVersions(liveNFD).MaxRound(latest - 100).Desc().Limit(1).Do(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, page.Versions)
		require.NotEmpty(t, page.Versions[0].Boxes)
		name := page.Versions[0].Boxes[0].Name

		var versions []models.BoxVersion
		for v, err := range c.ApplicationBoxVersions(liveNFD, name).Values().Desc().Limit(2).All(ctx) {
			require.NoError(t, err)
			versions = append(versions, v)
			if len(versions) == 3 {
				break
			}
		}
		require.NotEmpty(t, versions)
		for _, v := range versions {
			if v.Deleted {
				continue
			}
			box, err := c.GetApplicationBoxByName(liveNFD, name).Round(v.Round).Do(ctx)
			require.NoError(t, err)
			require.NotNil(t, v.Value)
			assert.True(t, bytes.Equal(*v.Value, box.Value), "the version's value is the box at its round")
		}

		at := latest - 100
		p1, err := c.GetApplicationBoxesAt(liveNFD, at).Limit(3).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, at, p1.Round)
		require.NotEmpty(t, p1.NextToken)
		p2, err := c.GetApplicationBoxesAt(liveNFD, at).Limit(3).Next(p1.NextToken).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, at, p2.Round)
	})

	t.Run("object versions", func(t *testing.T) {
		asset, err := c.AssetVersions(liveUSDC).Limit(3).Do(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, asset.Versions)

		app, err := c.ApplicationVersions(liveXGov).Values().Keys().Limit(3).Do(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, app.Versions)
		assert.NotNil(t, app.Versions[0].Application)

		res, err := c.AccountVersions(liveXGovCreator).Exclude("all").Values().MinRound(livePast).Limit(5).Do(ctx)
		require.NoError(t, err)
		for _, v := range res.Versions {
			if v.Base == nil || v.Base.Account == nil {
				continue
			}
			acct, err := c.AccountInformation(liveXGovCreator).Exclude([]string{"all"}).Round(v.Round).Do(ctx)
			require.NoError(t, err)
			assert.Equal(t, acct.Amount, v.Base.Account.Amount)
			break
		}

		asOf, err := c.AssetVersions(liveUSDC).Rounds([]uint64{1000, livePast, livePast}).Do(ctx)
		require.NoError(t, err)
		require.Len(t, asOf.Versions, 2)
		assert.True(t, asOf.Versions[0].Deleted, "USDC did not exist at round 1000")
		assert.False(t, asOf.Versions[1].Deleted)
		require.NotNil(t, asOf.Versions[1].ChangedRound)

		_, err = c.AccountAssetVersions(liveUSDCReserve, liveUSDC).Limit(3).Do(ctx)
		require.NoError(t, err)
		_, err = c.AccountApplicationVersions(liveXGovCreator, liveXGov).Keys().Limit(3).Do(ctx)
		require.NoError(t, err)
	})

	t.Run("query", func(t *testing.T) {
		f, err := c.DTFeatures(ctx)
		require.NoError(t, err)
		if f.Capabilities == nil || f.Capabilities.Query == nil {
			t.Skip("the node takes no queries")
		}
		// #pragma version 10; global Round; itob; log; txn ApplicationArgs 0; log; int 1
		program, err := base64.StdEncoding.DecodeString("CjIGFrA2GgCwgQE=")
		require.NoError(t, err)
		hi := []byte("hi")
		res, err := c.Query(models.QueryRequest{
			Program: program,
			Calls:   []models.QueryCall{{Args: [][]byte{hi}}, {}},
			Rounds:  []uint64{0, livePast, livePast},
		}).Do(ctx)
		require.NoError(t, err)
		require.Len(t, res.Rounds, 3)
		assert.Equal(t, uint64(livePast), res.Rounds[1].Round)
		for _, r := range res.Rounds {
			require.Empty(t, r.Error)
			require.Len(t, r.Calls, 2)
			ok, failed := r.Calls[0], r.Calls[1]
			// Calls run in the block after the queried round.
			want := binary.BigEndian.AppendUint64(nil, r.Round+1)
			require.Len(t, ok.Logs, 2)
			assert.Equal(t, want, ok.Logs[0])
			assert.Equal(t, hi, ok.Logs[1])
			assert.Empty(t, ok.Error)
			assert.NotZero(t, ok.Cost)
			assert.Contains(t, failed.Error, "ApplicationArgs")
		}
		assert.Equal(t, res.Rounds[1], res.Rounds[2])
		require.NotNil(t, res.Rounds[1].Protocol, "the default protocol is latest")
		assert.True(t, res.Rounds[1].Protocol.Applied)
	})

	t.Run("holders", func(t *testing.T) {
		if ok, err := c.SupportsAssetHolders(ctx); err != nil || !ok {
			t.Skipf("no holders index: %v", err)
		}
		var pages []models.AssetHoldersResponse
		for page, err := range c.GetAssetHolders(liveUSDC).Limit(2).Pages(ctx) {
			if errors.Is(err, ErrDTUnsupported) || IsHistoryUnavailable(err) {
				t.Skipf("no holders index: %v", err)
			}
			require.NoError(t, err)
			pages = append(pages, page)
			if len(pages) == 2 {
				break
			}
		}
		require.Len(t, pages, 2)
		assert.Equal(t, pages[0].Round, pages[1].Round, "pinned to the first page's round")
		assert.Less(t, pages[0].Holders[len(pages[0].Holders)-1].Address, pages[1].Holders[0].Address)
	})
}

func TestLiveStockAlgod(t *testing.T) {
	c := liveClient(t, "DT_STOCK_ALGOD_SERVER", "DT_STOCK_ALGOD_TOKEN", false)
	ctx := context.Background()

	f, err := c.DTFeatures(ctx)
	require.NoError(t, err)
	assert.Equal(t, DTFeatures{}, f)
	ok, err := c.SupportsAssetHolders(ctx)
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = c.Capabilities().Do(ctx)
	assert.ErrorIs(t, err, ErrDTUnsupported)
	_, err = c.ApplicationBoxesVersions(liveNFD).Do(ctx)
	assert.ErrorIs(t, err, ErrDTUnsupported, "404")
	// Stock algod's router hands {id}/versions and {id}/holders to the plain
	// lookup, which answers 400: a plain error, as DTFeatures is the gate.
	_, err = c.AssetVersions(liveUSDC).Do(ctx)
	code, _ := StatusCode(err)
	assert.Equal(t, 400, code)
	_, err = c.GetAssetHolders(liveUSDC).Do(ctx)
	code, _ = StatusCode(err)
	assert.Equal(t, 400, code)
	_, err = c.Query(models.QueryRequest{AppId: liveXGov, Calls: []models.QueryCall{{}}}).Do(ctx)
	assert.ErrorIs(t, err, ErrDTUnsupported)

	// Stock algod ignores round, and doesn't mark its answers with it.
	status, err := c.Status().Do(ctx)
	require.NoError(t, err)
	_, err = c.Supply().Round(status.LastRound).Do(ctx)
	assert.ErrorIs(t, err, ErrDTUnsupported, "a 200 without X-Algod-TT-Round")
	_, err = c.GetAssetByID(1 << 62).Round(status.LastRound).Do(ctx)
	assert.ErrorIs(t, err, ErrDTUnsupported, "a 404 without X-Algod-TT-Round")
}
