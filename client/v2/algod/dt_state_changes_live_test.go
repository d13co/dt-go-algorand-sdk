package algod

// Live tests of the state changes of a round, against a dt-algorand dt6 node:
//
//	DT_ALGOD_SERVER=https://mainnet-api-dt.d13.co go test -run LiveStateChanges -v ./client/v2/algod/

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveStateChangesJSON gets the node's JSON answer to a state changes request.
func liveStateChangesJSON(t *testing.T, round uint64, query url.Values) models.TxnStateChangesResponse {
	t.Helper()
	u := strings.TrimRight(os.Getenv("DT_ALGOD_SERVER"), "/") + fmt.Sprintf("/v2/deltas/%d/txn/state-changes?%s", round, query.Encode())
	req, err := http.NewRequest(http.MethodGet, u, nil)
	require.NoError(t, err)
	req.Header.Set("X-Algo-API-Token", os.Getenv("DT_ALGOD_TOKEN"))
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	var out models.TxnStateChangesResponse
	require.NoError(t, json.LenientDecode(body, &out))
	return out
}

func TestLiveStateChanges(t *testing.T) {
	c := liveClient(t, "DT_ALGOD_SERVER", "DT_ALGOD_TOKEN", true)
	ctx := context.Background()
	if ok, err := c.SupportsStateChanges(ctx); err != nil || !ok {
		t.Skipf("the node does not list state changes (%v)", err)
	}
	status, err := c.Status().Do(ctx)
	require.NoError(t, err)
	round := status.LastRound - 5

	t.Run("every group, the same in msgpack and JSON", func(t *testing.T) {
		if ok, _ := c.SupportsStateChangesInclude(ctx, IncludeAccounts, IncludeAssets, IncludeApps); !ok {
			t.Skip("no group values")
		}
		res, err := c.GetTxnStateChangesForRound(round).Full().Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, round, res.Round)
		assert.NotEmpty(t, res.FeeSink)
		assert.Equal(t, liveStateChangesJSON(t, round, url.Values{"include": {"accounts,assets,apps"}}), res)
		next := uint64(0)
		for _, g := range res.Groups {
			assert.Equal(t, next, g.First, "groups cover the payset in order")
			assert.EqualValues(t, len(g.Txids), g.Count)
			next = g.First + g.Count
			for _, txn := range res.TxnsOf(&g) {
				assert.True(t, txn.Path[0] >= g.First && txn.Path[0] < g.First+g.Count)
			}
		}
		for _, txn := range res.Txns {
			assert.Equal(t, txn.Txid != "", len(txn.Path) == 1)
			for _, ch := range txn.Changes {
				assert.Contains(t, []string{"g", "l", "b"}, ch.AppStateType)
				assert.Equal(t, ch.AppStateType == "l", ch.Account != "")
				assert.False(t, ch.Before == nil && ch.After == nil)
			}
		}
		if len(res.Groups) > 0 {
			g, ok := res.GroupOf(res.Groups[0].Txids[0])
			require.True(t, ok)
			one, err := c.GetTxnStateChangesForRound(round).Txids(g.Txids).Full().Do(ctx)
			require.NoError(t, err)
			require.Len(t, one.Groups, 1)
			assert.Equal(t, *g, one.Groups[0])
		}
	})

	t.Run("budget and reads, the same in msgpack and JSON", func(t *testing.T) {
		if ok, _ := c.SupportsStateChangesInclude(ctx, IncludeBudget, IncludeReads); !ok {
			t.Skip("no budget or reads")
		}
		res, err := c.GetTxnStateChangesForRound(round).Include(IncludeBudget, IncludeReads).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, liveStateChangesJSON(t, round, url.Values{"include": {"budget,reads"}}), res)
		for _, b := range res.Budget {
			assert.Contains(t, []string{"approval", "clear", "logic-sig"}, b.Kind)
			assert.Equal(t, b.Txid != "", len(b.Path) == 1)
		}
		for _, txn := range res.Reads {
			for _, r := range txn.Reads {
				if r.AppStateType == "b" || !r.Exists {
					assert.Nil(t, r.Value)
				}
				if r.AppStateType != "b" {
					assert.Zero(t, r.Length)
				}
			}
		}
	})

	t.Run("a trace, every step on a line of its program", func(t *testing.T) {
		if ok, _ := c.SupportsStateChangesInclude(ctx, IncludeTrace, IncludeBudget); !ok {
			t.Skip("no traces")
		}
		// A recent app call, with inner app calls if one is near.
		var traced uint64
		var txid string
		for r := round; r > round-35 && txid == ""; r-- {
			res, err := c.GetTxnStateChangesForRound(r).Include(IncludeBudget).Do(ctx)
			require.NoError(t, err)
			for _, b := range res.Budget {
				if len(b.Path) > 1 {
					for _, top := range res.Budget {
						if top.Txid != "" && top.Path[0] == b.Path[0] {
							traced, txid = r, top.Txid
						}
					}
				}
			}
			if txid == "" && r == round-34 {
				for _, b := range res.Budget {
					if b.Txid != "" {
						traced, txid = r, b.Txid
					}
				}
			}
		}
		if txid == "" {
			t.Skip("no app call near the tip")
		}
		res, err := c.GetTxnStateChangesForRound(traced).Include(IncludeBudget, IncludeReads).Trace(txid).Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, liveStateChangesJSON(t, traced, url.Values{"txid": {txid}, "include": {"budget,reads,trace"}}), res)
		assert.NotEmpty(t, res.BudgetOf(txid))
		steps := 0
		var walk func(tr *models.SimulationTransactionExecTrace)
		walk = func(tr *models.SimulationTransactionExecTrace) {
			for _, p := range []struct {
				hash  []byte
				steps []models.SimulationOpcodeTraceUnit
			}{{tr.ApprovalProgramHash, tr.ApprovalProgramTrace}, {tr.ClearStateProgramHash, tr.ClearStateProgramTrace}, {tr.LogicSigHash, tr.LogicSigTrace}} {
				for _, step := range p.steps {
					steps++
					_, ok := res.ProgramLine(p.hash, step.Pc)
					assert.True(t, ok, "pc %d", step.Pc)
					for _, op := range step.StateChanges {
						assert.NotZero(t, op.AppId, "traces always have app-id")
					}
				}
			}
			for i := range tr.InnerTrace {
				walk(&tr.InnerTrace[i])
			}
		}
		found := false
		for i := range res.Traces {
			found = found || res.Traces[i].Txid == txid
			walk(&res.Traces[i].ExecTrace)
		}
		assert.True(t, found)
		assert.Positive(t, steps)
	})

	t.Run("a round not reached yet is the node's 404", func(t *testing.T) {
		_, err := c.GetTxnStateChangesForRound(status.LastRound + 1_000_000).Do(ctx)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrDTUnsupported)
		code, _ := StatusCode(err)
		assert.Equal(t, http.StatusNotFound, code)
	})
}
