package algod

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stub is an algod stand-in: handle answers each request, and the requests
// are recorded.
type stub struct {
	requests []*http.Request
	bodies   [][]byte
}

func newStub(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*Client, *stub) {
	t.Helper()
	s := &stub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.requests = append(s.requests, r)
		s.bodies = append(s.bodies, body)
		handle(w, r)
	}))
	t.Cleanup(server.Close)
	c, err := MakeClient(server.URL, "")
	require.NoError(t, err)
	return c, s
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// markRound marks the answer with the request's round, if it has one, as a
// time-travel node does.
func markRound(w http.ResponseWriter, r *http.Request) {
	if round := r.URL.Query().Get("round"); round != "" {
		w.Header().Set(TTRoundHeader, round)
	}
}

// echoRound answers every read with the round it was asked for.
func echoRound(w http.ResponseWriter, r *http.Request) {
	markRound(w, r)
	round := r.URL.Query().Get("round")
	reply(w, 200, fmt.Sprintf(`{"round":%s,"current_round":%s}`, round, round))
}

func TestRoundQueryStrings(t *testing.T) {
	c, s := newStub(t, echoRound)
	ctx := context.Background()
	cases := []struct {
		do    func() error
		path  string
		query string
	}{
		{func() error {
			_, err := c.AccountInformation("A").Round(0).Exclude([]string{"all"}).Do(ctx)
			return err
		},
			"/v2/accounts/A", "exclude=all&round=0"},
		{func() error { _, err := c.AccountAssetInformation("A", 7).Round(5).Do(ctx); return err },
			"/v2/accounts/A/assets/7", "round=5"},
		{func() error {
			_, err := c.AccountAssetsInformation("A").Round(5).Limit(10).Next("x").Do(ctx)
			return err
		},
			"/v2/accounts/A/assets", "limit=10&next=x&round=5"},
		{func() error {
			_, err := c.AccountApplicationsInformation("A").Round(5).Include([]string{"params"}).Limit(2).Next("y").Do(ctx)
			return err
		}, "/v2/accounts/A/applications", "include=params&limit=2&next=y&round=5"},
		{func() error { _, err := c.AccountApplicationInformation("A", 9).Round(1).Do(ctx); return err },
			"/v2/accounts/A/applications/9", "round=1"},
		{func() error { _, err := c.GetApplicationBoxByName(9, []byte{1}).Round(3).Do(ctx); return err },
			"/v2/applications/9/box", "name=b64%3AAQ%3D%3D&round=3"},
		{func() error { _, err := c.Supply().Round(0).Do(ctx); return err },
			"/v2/ledger/supply", "round=0"},
		{func() error { _, err := c.GetAssetByID(4).Round(0).Do(ctx); return err },
			"/v2/assets/4", "round=0"},
		{func() error { _, err := c.GetApplicationByID(4).Round(8).Do(ctx); return err },
			"/v2/applications/4", "round=8"},
		{func() error { _, err := c.GetApplicationBoxesAt(4, 0).Limit(2).Prefix("str:a").Do(ctx); return err },
			"/v2/applications/4/boxes", "limit=2&prefix=str%3Aa&round=0"},
	}
	for i, tc := range cases {
		require.NoError(t, tc.do(), "case %d", i)
		req := s.requests[len(s.requests)-1]
		assert.Equal(t, tc.path, req.URL.Path, "case %d", i)
		assert.Equal(t, tc.query, req.URL.RawQuery, "case %d", i)
	}
}

func TestRoundSetBeforeOrAfterSetters(t *testing.T) {
	c, s := newStub(t, echoRound)
	// Params set on the upstream request after Round still apply.
	req := c.AccountInformation("A")
	at := req.Round(5)
	req.Exclude([]string{"all"})
	_, err := at.Do(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "exclude=all&round=5", s.requests[0].URL.RawQuery)
}

func TestParseTTRound(t *testing.T) {
	h := http.Header{}
	_, ok := ParseTTRound(h)
	assert.False(t, ok)
	for value, want := range map[string]uint64{"0": 0, "45000000": 45000000, "18446744073709551615": 1<<64 - 1} {
		h.Set(TTRoundHeader, value)
		got, ok := ParseTTRound(h)
		assert.True(t, ok, value)
		assert.Equal(t, want, got, value)
	}
	for _, value := range []string{"-1", " 5", "5,6", "0x5", "18446744073709551616", "abc"} {
		h.Set(TTRoundHeader, value)
		_, ok := ParseTTRound(h)
		assert.False(t, ok, value)
	}
}

func TestTTRoundHeader(t *testing.T) {
	ctx := context.Background()
	type answer struct {
		status int
		header string // X-Algod-TT-Round, if not empty
		body   string
	}
	stubFor := func(a answer) *Client {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			if a.header != "" {
				w.Header().Set(TTRoundHeader, a.header)
			}
			reply(w, a.status, a.body)
		})
		return c
	}
	account := func(c *Client) error { _, err := c.AccountInformation("A").Round(5).Do(ctx); return err }
	asset := func(c *Client) error { _, err := c.GetAssetByID(1).Round(5).Do(ctx); return err }
	boxes := func(c *Client) error { _, err := c.GetApplicationBoxesAt(1, 5).Do(ctx); return err }
	holders := func(c *Client) error { _, err := c.GetAssetHolders(1).Round(5).Do(ctx); return err }

	const (
		accepted    = "accepted"
		unsupported = "unsupported"
		nodeBug     = "node bug"
		passed      = "passed through"
	)
	cases := []struct {
		name   string
		read   func(*Client) error
		answer answer
		want   string
		status int // of a passed-through or unsupported error
	}{
		{"account marked", account, answer{200, "5", `{"round":99}`}, accepted, 0},
		{"asset marked", asset, answer{200, "5", `{"index":1}`}, accepted, 0},
		{"account unmarked", account, answer{200, "", `{"round":5}`}, unsupported, 0},
		{"asset unmarked", asset, answer{200, "", `{"index":1}`}, unsupported, 0},
		{"account marked with another round", account, answer{200, "6", `{"round":5}`}, nodeBug, 0},
		{"account marked with garbage", account, answer{200, "five", `{"round":5}`}, nodeBug, 0},
		{"404 marked", asset, answer{404, "5", `{"message":"asset does not exist"}`}, passed, 404},
		{"404 unmarked", asset, answer{404, "", `{"message":"asset does not exist"}`}, unsupported, 404},
		{"404 marked with another round", asset, answer{404, "4", `{"message":"asset does not exist"}`}, nodeBug, 0},
		{"500 marked", account, answer{500, "5", `{"message":"boom"}`}, passed, 500},
		{"500 unmarked", account, answer{500, "", `{"message":"boom"}`}, passed, 500},
		{"400 unmarked", account, answer{400, "", `{"message":"round greater than the latest"}`}, passed, 400},
		{"boxes marked", boxes, answer{200, "5", `{"round":5,"boxes":[]}`}, accepted, 0},
		{"boxes unmarked at the round", boxes, answer{200, "", `{"round":5,"boxes":[]}`}, accepted, 0},
		{"boxes unmarked at another round", boxes, answer{200, "", `{"round":9,"boxes":[]}`}, unsupported, 0},
		{"boxes 404 unmarked", boxes, answer{404, "", `{"message":"not found"}`}, passed, 404},
		{"holders marked", holders, answer{200, "5", `{"round":5,"holders":[]}`}, accepted, 0},
		{"holders unmarked", holders, answer{200, "", `{"round":5,"holders":[]}`}, unsupported, 0},
		{"holders 404 marked", holders, answer{404, "5", `{"message":"asset does not exist"}`}, passed, 404},
		{"holders 404 unmarked", holders, answer{404, "", `{"message":"asset does not exist"}`}, unsupported, 404},
		{"holders 500 marked", holders, answer{500, "5", `{"message":"boom"}`}, passed, 500},
		{"holders 503 unmarked", holders, answer{503, "", `{"message":"timetravel: the asset holders index is not complete"}`}, passed, 503},
	}
	for _, tc := range cases {
		err := tc.read(stubFor(tc.answer))
		switch tc.want {
		case accepted:
			assert.NoError(t, err, tc.name)
			continue
		case unsupported:
			var dtErr *DTUnsupportedError
			if assert.ErrorAs(t, err, &dtErr, tc.name) {
				if assert.NotNil(t, dtErr.RequestedRound, tc.name) {
					assert.Equal(t, uint64(5), *dtErr.RequestedRound, tc.name)
				}
			}
		case nodeBug:
			assert.Error(t, err, tc.name)
			assert.NotErrorIs(t, err, ErrDTUnsupported, tc.name)
			_, isHTTP := StatusCode(err)
			assert.False(t, isHTTP, "%s: a plain error", tc.name)
			assert.Contains(t, err.Error(), "asked for round 5", tc.name)
		case passed:
			assert.Error(t, err, tc.name)
			assert.NotErrorIs(t, err, ErrDTUnsupported, tc.name)
		}
		if tc.status != 0 {
			code, ok := StatusCode(err)
			assert.True(t, ok, tc.name)
			assert.Equal(t, tc.status, code, tc.name)
		}
	}

	t.Run("no round, no check", func(t *testing.T) {
		c := stubFor(answer{200, "", `{"round":5,"holders":[]}`})
		_, err := c.GetAssetHolders(1).Do(ctx)
		assert.NoError(t, err)
	})

	t.Run("the caller's ResponseInfo is filled", func(t *testing.T) {
		c := stubFor(answer{200, "5", `{"round":5}`})
		var info common.ResponseInfo
		_, err := c.AccountInformation("A").Round(5).Do(common.WithResponseInfo(ctx, &info))
		require.NoError(t, err)
		round, ok := ParseTTRound(info.Header)
		assert.True(t, ok)
		assert.Equal(t, uint64(5), round)
	})

	t.Run("supply without online stake at the round", func(t *testing.T) {
		c := stubFor(answer{404, "5", `{"message":"the node's history does not record this online stake"}`})
		_, err := c.Supply().Round(5).Do(ctx)
		assert.True(t, IsOnlineStakeNotRecorded(err))
		assert.NotErrorIs(t, err, ErrDTUnsupported)
	})
}

func TestPastRoundRefused(t *testing.T) {
	for _, msg := range []string{
		"looking up past rounds requires EnableTimeTravel",
		"given round is no longer available; use a more recent round",
	} {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 400, fmt.Sprintf(`{"message":%q}`, msg))
		})
		_, err := c.AccountAssetInformation("A", 1).Round(5).Do(context.Background())
		assert.ErrorIs(t, err, ErrDTUnsupported, msg)
	}
}

func TestRound404IsTheNodesAnswer(t *testing.T) {
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		markRound(w, r)
		reply(w, 404, `{"message":"account asset info not found"}`)
	})
	var info common.ResponseInfo
	ctx := common.WithResponseInfo(context.Background(), &info)
	_, err := c.AccountAssetInformation("A", 1).Round(5).Do(ctx)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrDTUnsupported)
	code, ok := StatusCode(err)
	assert.True(t, ok)
	assert.Equal(t, 404, code)
	assert.Equal(t, 404, info.StatusCode)
	assert.Len(t, s.requests, 1, "no probe")
}
func TestVersionsQueryStrings(t *testing.T) {
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"round":1,"versions":[]}`)
	})
	ctx := context.Background()
	_, err := c.AccountVersions("A").MinRound(0).MaxRound(0).Limit(0).Desc().Values().Keys().Exclude([]string{"created-apps-params", "created-assets-params"}).Next("12").Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, "/v2/accounts/A/versions", s.requests[0].URL.Path)
	assert.Equal(t, url.Values{
		"min-round": {"0"}, "max-round": {"0"}, "limit": {"0"}, "order": {"desc"},
		"include": {"values,keys"}, "exclude": {"created-apps-params,created-assets-params"}, "next": {"12"},
	}, s.requests[0].URL.Query())

	_, err = c.ApplicationVersions(9).Key([]byte{}).Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, url.Values{"key": {"b64:"}}, s.requests[1].URL.Query(), "an empty key is a valid key")

	_, err = c.ApplicationBoxVersions(9, []byte("box")).Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, "/v2/applications/9/box/versions", s.requests[2].URL.Path)
	assert.Equal(t, url.Values{"name": {"b64:Ym94"}}, s.requests[2].URL.Query())

	_, err = c.ApplicationBoxesVersions(9).Prefix([]byte("b")).Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, url.Values{"prefix": {"b64:Yg=="}}, s.requests[3].URL.Query())

	for i, path := range []string{"/v2/accounts/A/assets/2/versions", "/v2/accounts/A/applications/3/versions", "/v2/assets/4/versions"} {
		switch i {
		case 0:
			_, err = c.AccountAssetVersions("A", 2).Do(ctx)
		case 1:
			_, err = c.AccountApplicationVersions("A", 3).Do(ctx)
		case 2:
			_, err = c.AssetVersions(4).Do(ctx)
		}
		require.NoError(t, err)
		assert.Equal(t, path, s.requests[4+i].URL.Path)
		assert.Equal(t, "", s.requests[4+i].URL.RawQuery)
	}
}

func TestVersionsRounds(t *testing.T) {
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"round":5,"versions":[{"round":3,"deleted":true},{"round":5,"changed-round":4,"deleted":false}]}`)
	})
	ctx := context.Background()
	page, err := c.AssetVersions(1).Rounds([]uint64{5, 3, 5, 3}).Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, "3,5", s.requests[0].URL.Query().Get("rounds"), "sorted and deduplicated")
	require.Len(t, page.Versions, 2)
	assert.Nil(t, page.Versions[0].ChangedRound)
	assert.Equal(t, uint64(4), *page.Versions[1].ChangedRound)

	_, err = c.AssetVersions(1).Rounds(nil).Do(ctx)
	assert.NoError(t, err)
	assert.False(t, s.requests[len(s.requests)-1].URL.Query().Has("rounds"))
}
func TestVersionsKeys(t *testing.T) {
	var body string
	c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, body) })
	ctx := context.Background()

	body = `{"round":5,"versions":[{"round":5,"deleted":false,"changed-keys":[]}]}`
	page, err := c.ApplicationVersions(1).Keys().Do(ctx)
	require.NoError(t, err)
	require.NotNil(t, page.Versions[0].ChangedKeys, "an empty list is not an absent one")
	assert.Empty(t, *page.Versions[0].ChangedKeys)

	body = `{"round":5,"versions":[{"round":5,"deleted":false}]}`
	page, err = c.ApplicationVersions(1).Do(ctx)
	require.NoError(t, err)
	assert.Nil(t, page.Versions[0].ChangedKeys)
}
func TestVersionsUnsupported(t *testing.T) {
	cases := []struct {
		status      int
		message     string
		unsupported bool
	}{
		{404, "Not Found", true},
		// A node without the time-travel routes: the plain asset lookup's 400.
		{400, `Invalid format for parameter asset-id: error binding string parameter: strconv.ParseUint: parsing \"4/versions\": invalid syntax`, true},
		{400, "invalid include value: keys", false},
		{400, "rounds cannot be combined with min-round or max-round", false},
		{400, `unable to parse next: strconv.ParseUint: parsing \"x\": invalid syntax`, false},
	}
	for _, tc := range cases {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, tc.status, `{"message":"`+tc.message+`"}`)
		})
		_, err := c.AssetVersions(4).Do(context.Background())
		require.Error(t, err)
		assert.Equal(t, tc.unsupported, errors.Is(err, ErrDTUnsupported), tc.message)
	}

	c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, `{"message":"Invalid format for parameter application-id: error binding string parameter: strconv.ParseUint: parsing \"3/versions\": invalid syntax"}`)
	})
	_, err := c.AccountApplicationVersions("A", 3).Do(context.Background())
	assert.ErrorIs(t, err, ErrDTUnsupported, "the plain local state lookup's 400")
	_, err = c.AccountVersions("A").Do(context.Background())
	assert.ErrorIs(t, err, ErrDTUnsupported)
	// An address the caller got wrong is the caller's error.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, `{"message":"Invalid format for parameter address: bad checksum"}`)
	})
	_, err = c.AccountAssetVersions("A", 2).Do(context.Background())
	assert.False(t, errors.Is(err, ErrDTUnsupported))
}

func TestVersionsPages(t *testing.T) {
	pages := map[string]string{
		"":  `{"round":9,"next-token":"5","versions":[{"round":1},{"round":4}]}`,
		"5": `{"round":9,"next-token":"8","versions":[]}`,
		"8": `{"round":9,"versions":[{"round":8}]}`,
	}
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, pages[r.URL.Query().Get("next")])
	})
	q := c.AssetVersions(1).Limit(2)
	var rounds []uint64
	for v, err := range q.All(context.Background()) {
		require.NoError(t, err)
		rounds = append(rounds, v.Round)
	}
	assert.Equal(t, []uint64{1, 4, 8}, rounds)
	assert.Len(t, s.requests, 3)
	for _, r := range s.requests {
		assert.Equal(t, "2", r.URL.Query().Get("limit"))
	}

	// Stopping early stops fetching.
	n := 0
	for range q.Pages(context.Background()) {
		n++
		break
	}
	assert.Equal(t, 1, n)
	assert.Len(t, s.requests, 4)

	// A node that echoes the token would page forever.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"round":9,"next-token":"5","versions":[]}`)
	})
	var lastErr error
	count := 0
	for _, err := range c.AssetVersions(1).Pages(context.Background()) {
		count++
		lastErr = err
	}
	assert.Equal(t, 2, count)
	assert.ErrorContains(t, lastErr, "paging would never end")
}

func TestAssetHolders(t *testing.T) {
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		markRound(w, r)
		if r.URL.Query().Get("next") == "" {
			reply(w, 200, `{"round":100,"next-token":"B","holders":[{"address":"A","amount":1,"is-frozen":false}]}`)
			return
		}
		reply(w, 200, `{"round":100,"holders":[{"address":"C","amount":0,"is-frozen":true}]}`)
	})
	var holders []models.AssetHolder
	for h, err := range c.GetAssetHolders(7).Limit(1).All(context.Background()) {
		require.NoError(t, err)
		holders = append(holders, h)
	}
	require.Len(t, holders, 2)
	assert.True(t, holders[1].IsFrozen)
	assert.Equal(t, "/v2/assets/7/holders", s.requests[0].URL.Path)
	assert.Equal(t, url.Values{"limit": {"1"}}, s.requests[0].URL.Query())
	assert.Equal(t, url.Values{"limit": {"1"}, "next": {"B"}, "round": {"100"}}, s.requests[1].URL.Query(), "pinned to the first page's round")

	_, err := c.GetAssetHolders(7).Round(0).Do(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "round=0", s.requests[2].URL.RawQuery)
}

func TestAssetHoldersUnsupported(t *testing.T) {
	cases := []struct {
		status      int
		message     string
		unsupported bool
	}{
		{404, "Not Found", true},
		{404, "asset does not exist", false},
		{400, `Invalid format for parameter asset-id: error binding string parameter: strconv.ParseUint: parsing \"7/holders\": invalid syntax`, true},
		{400, "limit must be between 1 and 10000", false},
		{400, "asset holders require EnableTimeTravel and TimeTravelHoldersIndex", true},
		{503, "timetravel: the asset holders index is not complete: build it with ttimport -holders-index while the node is stopped", false},
	}
	for _, tc := range cases {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, tc.status, `{"message":"`+tc.message+`"}`)
		})
		_, err := c.GetAssetHolders(7).Do(context.Background())
		require.Error(t, err)
		assert.Equal(t, tc.unsupported, errors.Is(err, ErrDTUnsupported), tc.message)
		if tc.status == 503 {
			assert.True(t, IsHistoryUnavailable(err))
			assert.False(t, IsNodeBusy(err))
		}
	}
}

// queryRequestMirror is the node's PreEncodedQueryRequest, decoded strictly
// as the node decodes a JSON body.
type queryRequestMirror struct {
	Program      []byte `json:"program,omitempty"`
	ClearProgram []byte `json:"clear-program,omitempty"`
	GlobalSchema *struct {
		NumUint      uint64 `json:"num-uint"`
		NumByteSlice uint64 `json:"num-byte-slice"`
	} `json:"global-schema,omitempty"`
	LocalSchema *struct {
		NumUint      uint64 `json:"num-uint"`
		NumByteSlice uint64 `json:"num-byte-slice"`
	} `json:"local-schema,omitempty"`
	AppID    uint64   `json:"app-id,omitempty"`
	Sender   string   `json:"sender,omitempty"`
	Rounds   []uint64 `json:"rounds,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	Budget   uint64   `json:"budget,omitempty"`
	Calls    []struct {
		Args          [][]byte `json:"args,omitempty"`
		Accounts      []string `json:"accounts,omitempty"`
		ForeignApps   []uint64 `json:"foreign-apps,omitempty"`
		ForeignAssets []uint64 `json:"foreign-assets,omitempty"`
		Boxes         []struct {
			App  uint64 `json:"app"`
			Name []byte `json:"name"`
		} `json:"boxes,omitempty"`
	} `json:"calls"`
}

func TestQuery(t *testing.T) {
	c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"last-round":10,"rounds":[{"round":0,"error":"no history"},{"round":10,"calls":[{"logs":["AQI="],"cost":5},{"logs":[],"cost":1,"error":"boom"}],"protocol":{"from":"v38","to":"v41","applied":true,"notes":[{"code":"semantics-changed","message":"MaxAppProgramCost"},{"code":"block-incentive-fields","message":"no proposer","hits":[{"group":1,"path":[0,2]}]}]}}]}`)
	})
	req := models.QueryRequest{
		Program:      []byte{1, 2},
		GlobalSchema: &models.ApplicationStateSchema{NumUint: 1},
		Rounds:       []uint64{0, 10},
		Protocol:     "round",
		Calls: []models.QueryCall{
			{Args: [][]byte{{3}}, Accounts: []string{"A"}, ForeignApps: []uint64{1}, Boxes: []models.BoxReference{{App: 0, Name: []byte("b")}}},
			{},
		},
	}
	resp, err := c.Query(req).Do(context.Background())
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, s.requests[0].Method)
	assert.Equal(t, "/v2/query", s.requests[0].URL.Path)

	var got queryRequestMirror
	dec := stdjson.NewDecoder(bytes.NewReader(s.bodies[0]))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&got), string(s.bodies[0]))
	assert.Equal(t, []byte{1, 2}, got.Program)
	assert.Equal(t, uint64(1), got.GlobalSchema.NumUint)
	assert.Nil(t, got.LocalSchema)
	assert.Equal(t, []uint64{0, 10}, got.Rounds, "round 0 (the latest) must be sent")
	require.Len(t, got.Calls, 2)
	assert.Equal(t, [][]byte{{3}}, got.Calls[0].Args)
	assert.Equal(t, []byte("b"), got.Calls[0].Boxes[0].Name)
	assert.NotContains(t, string(s.bodies[0]), "app-id", "zero fields are left out")

	require.Len(t, resp.Rounds, 2)
	assert.Equal(t, "no history", resp.Rounds[0].Error)
	assert.Equal(t, [][]byte{{1, 2}}, resp.Rounds[1].Calls[0].Logs)
	assert.Equal(t, "boom", resp.Rounds[1].Calls[1].Error)
	assert.Nil(t, resp.Rounds[0].Protocol)
	assert.Equal(t, &models.QueryProtocolOverride{
		From: "v38", To: "v41", Applied: true,
		Notes: []models.QueryProtocolNote{
			{Code: "semantics-changed", Message: "MaxAppProgramCost"},
			{Code: "block-incentive-fields", Message: "no proposer", Hits: []models.QueryProtocolNoteHit{{Group: 1, Path: []int{0, 2}}}},
		},
	}, resp.Rounds[1].Protocol)
}

func TestQueryErrors(t *testing.T) {
	cases := []struct {
		status      int
		message     string
		unsupported bool
	}{
		{404, "Not Found", true},
		{400, "query is disabled on this node (MaxAPIQueryCalls is 0)", true},
		{400, "too many calls: got 9, the limit is 8", false},
		{400, "too many calls in total: 64 calls at 16 rounds is 1024, the limit is 512 (MaxAPIQueryTotalCalls)", false},
		{503, "operation not available during catchup", false},
	}
	for _, tc := range cases {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, tc.status, `{"message":"`+tc.message+`"}`)
		})
		_, err := c.Query(models.QueryRequest{AppId: 1, Calls: []models.QueryCall{{}}}).Do(context.Background())
		require.Error(t, err)
		assert.Equal(t, tc.unsupported, errors.Is(err, ErrDTUnsupported), tc.message)
		if tc.status == 503 {
			assert.True(t, IsCatchingUp(err))
			assert.False(t, IsNodeBusy(err))
		}
	}
}

func TestDTFeatures(t *testing.T) {
	ctx := context.Background()
	want := models.CapabilitiesResponse{
		TimeTravel:       true,
		TimeLimitSeconds: 9,
		Simulate:         models.SimulateLimits{MaxTxnGroups: 16, MaxExtraOpcodeBudget: 320000},
	}
	// A current fork node.
	c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/capabilities", r.URL.Path)
		reply(w, 200, `{"time-travel":true,"developer-api":false,"time-limit-seconds":9,"simulate":{"max-txn-groups":16,"max-extra-opcode-budget":320000}}`)
	})
	f, err := c.DTFeatures(ctx)
	require.NoError(t, err)
	assert.True(t, f.Supported)
	require.NotNil(t, f.Capabilities)
	assert.Equal(t, want, *f.Capabilities)
	assert.Nil(t, f.Capabilities.Query, "queries disabled")

	// A dt4 node still reports the simulate limits dt5 dropped: ignored.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"time-travel":true,"developer-api":false,"time-limit-seconds":9,"simulate":{"max-txn-groups":16,"max-rounds":4,"max-extra-opcode-budget":320000,"resource-limit-multiplier":2}}`)
	})
	f, err = c.DTFeatures(ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Capabilities)
	assert.Equal(t, want, *f.Capabilities)

	// Queries on.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"time-travel":true,"developer-api":true,"time-limit-seconds":0,"simulate":{"max-txn-groups":16,"max-extra-opcode-budget":320000},"query":{"max-calls":64,"max-rounds":16,"max-call-budget":700000,"max-call-log-bytes":0,"max-call-state-bytes":0,"max-response-bytes":1000000}}`)
	})
	f, err = c.DTFeatures(ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Capabilities.Query)
	assert.Equal(t, uint64(64), f.Capabilities.Query.MaxCalls)
	assert.Equal(t, uint64(64*16), f.Capabilities.Query.MaxTotalCalls, "a node without max-total-calls: calls x rounds")
	assert.True(t, f.Capabilities.DeveloperApi)

	// A node that reports max-total-calls.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"time-travel":true,"developer-api":false,"time-limit-seconds":0,"simulate":{"max-txn-groups":16,"max-extra-opcode-budget":320000},"query":{"max-calls":1024,"max-rounds":16,"max-total-calls":1024,"max-call-budget":320000,"max-call-log-bytes":0,"max-call-state-bytes":0,"max-response-bytes":0}}`)
	})
	f, err = c.DTFeatures(ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Capabilities.Query)
	assert.Equal(t, uint64(1024), f.Capabilities.Query.MaxTotalCalls)

	// A reported 0 is taken as absent.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"time-travel":true,"developer-api":false,"time-limit-seconds":0,"simulate":{"max-txn-groups":16,"max-extra-opcode-budget":320000},"query":{"max-calls":64,"max-rounds":16,"max-total-calls":0,"max-call-budget":320000,"max-call-log-bytes":0,"max-call-state-bytes":0,"max-response-bytes":0}}`)
	})
	caps, err := c.Capabilities().Do(ctx)
	require.NoError(t, err)
	require.NotNil(t, caps.Query)
	assert.Equal(t, uint64(64*16), caps.Query.MaxTotalCalls)

	// Nonsense limits: rounds count at least 1, and the product saturates.
	for _, tc := range []struct {
		query string
		want  uint64
	}{
		{`"max-calls":64,"max-rounds":0`, 64},
		{`"max-calls":18446744073709551615,"max-rounds":2`, math.MaxUint64},
	} {
		c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 200, `{"time-travel":true,"developer-api":false,"time-limit-seconds":0,"simulate":{"max-txn-groups":16,"max-extra-opcode-budget":320000},"query":{`+tc.query+`,"max-call-budget":320000,"max-call-log-bytes":0,"max-call-state-bytes":0,"max-response-bytes":0}}`)
		})
		caps, err = c.Capabilities().Do(ctx)
		require.NoError(t, err)
		require.NotNil(t, caps.Query)
		assert.Equal(t, tc.want, caps.Query.MaxTotalCalls, tc.query)
	}

	// Stock algod.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 404, `{"message":"Not Found"}`) })
	f, err = c.DTFeatures(ctx)
	require.NoError(t, err)
	assert.Equal(t, DTFeatures{}, f)

	// Other errors are returned.
	c, _ = newStub(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 401, `{"message":"Invalid API Token"}`) })
	_, err = c.DTFeatures(ctx)
	require.Error(t, err)
	code, _ := StatusCode(err)
	assert.Equal(t, 401, code)
}

func TestErrorHelpers(t *testing.T) {
	c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		reply(w, 503, `{"message":"the node is busy: try again later or on another node"}`)
	})
	_, err := c.Query(models.QueryRequest{AppId: 1}).Do(context.Background())
	assert.True(t, IsNodeBusy(err))
	wait, ok := RetryAfter(err)
	assert.True(t, ok)
	assert.Equal(t, time.Second, wait)

	// Upstream methods: the status is read from the message, but the headers
	// are not there.
	_, err = c.Status().Do(context.Background())
	assert.True(t, IsNodeBusy(err))
	_, ok = RetryAfter(err)
	assert.False(t, ok)

	httpErr := &common.ResponseError{StatusCode: 503, Header: http.Header{"Retry-After": {time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)}}, Cause: upstreamErr(503, "x")}
	wait, ok = RetryAfter(httpErr)
	assert.True(t, ok)
	assert.Equal(t, time.Duration(0), wait)

	assert.True(t, IsTimeLimitExceeded(upstreamErr(504, "the request reached the node's time limit")))
	assert.True(t, IsRoundTooLarge(upstreamErr(400, `{"message":"too many objects to merge"}`)))
	assert.True(t, IsQueryResponseTooLarge(upstreamErr(400, `{"message":"query response too large"}`)))
	assert.True(t, IsOnlineStakeNotRecorded(upstreamErr(404, `{"message":"the time travel history does not record this online stake"}`)))
	assert.True(t, IsHistoryUnavailable(upstreamErr(503, `{"message":"the time travel history is not served"}`)))
	assert.False(t, IsNodeBusy(errors.New("dial tcp: connection refused")))
	_, ok = StatusCode(errors.New("dial tcp: connection refused"))
	assert.False(t, ok)
	_, ok = StatusCode(nil)
	assert.False(t, ok)
}

func TestParseEffort(t *testing.T) {
	h := http.Header{}
	_, ok := ParseEffort(h)
	assert.False(t, ok, "absent")

	h.Set(EffortHeader, "opcodes=700, lookups=3, history=2, scanned=0, response-bytes=512, eval-us=840, future=1;x=2")
	e, ok := ParseEffort(h)
	require.True(t, ok)
	assert.Equal(t, Effort{Opcodes: 700, Lookups: 3, History: 2, Scanned: 0, ResponseBytes: 512, EvalMicroseconds: 840}, e)

	h.Set(EffortHeader, "opcodes=700, lookups=3")
	_, ok = ParseEffort(h)
	assert.False(t, ok, "partial")

	h.Set(EffortHeader, strings.Replace("opcodes=1, lookups=1, history=1, scanned=1, response-bytes=1, eval-us=1", "eval-us=1", "eval-us=1.5", 1))
	_, ok = ParseEffort(h)
	assert.False(t, ok, "not an integer")
}

func TestReviewGaps(t *testing.T) {
	ctx := context.Background()

	t.Run("round too large and history messages", func(t *testing.T) {
		assert.True(t, IsRoundTooLarge(upstreamErr(400, `{"message":"one round of boxes is too large: app 5 has more than 10000 boxes at round 9: use GET /v2/applications/5/boxes?round="}`)))
		assert.True(t, IsHistoryUnavailable(upstreamErr(503, `{"message":"timetravel: the box index is incomplete"}`)))
	})

	t.Run("a fork without time travel", func(t *testing.T) {
		c, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 200, `{"time-travel":false,"simulate":{"max-txn-groups":16}}`)
		})
		f, err := c.DTFeatures(ctx)
		require.NoError(t, err)
		assert.True(t, f.Supported)
		assert.False(t, f.TimeTravel)
	})

	t.Run("holders token loop and early stop", func(t *testing.T) {
		c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			markRound(w, r)
			reply(w, 200, `{"round":3,"next-token":"B","holders":[{"address":"A","amount":1,"is-frozen":false}]}`)
		})
		var lastErr error
		for _, err := range c.GetAssetHolders(1).Pages(ctx) {
			lastErr = err
		}
		assert.ErrorContains(t, lastErr, "paging would never end")
		n := len(s.requests)
		for range c.GetAssetHolders(1).All(ctx) {
			break
		}
		assert.Len(t, s.requests, n+1)
	})

	t.Run("rounds across pages, newest first", func(t *testing.T) {
		pages := map[string]string{
			"":  `{"round":9,"next-token":"5","versions":[{"round":9},{"round":7}]}`,
			"5": `{"round":5,"versions":[{"round":5}]}`,
		}
		c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 200, pages[r.URL.Query().Get("next")])
		})
		var rounds []uint64
		for v, err := range c.AssetVersions(1).Rounds([]uint64{5, 7, 9}).Desc().Limit(2).All(ctx) {
			require.NoError(t, err)
			rounds = append(rounds, v.Round)
		}
		assert.Equal(t, []uint64{9, 7, 5}, rounds)
		require.Len(t, s.requests, 2)
		assert.Equal(t, url.Values{"rounds": {"5,7,9"}, "order": {"desc"}, "limit": {"2"}, "next": {"5"}}, s.requests[1].URL.Query())
	})

}

func TestSupportsAssetHolders(t *testing.T) {
	ctx := context.Background()
	limits := func(tt bool) string { return fmt.Sprintf(`{"time-travel":%v,"simulate":{"max-txn-groups":16}}`, tt) }
	cases := []struct {
		name     string
		limits   string
		holders  func(w http.ResponseWriter)
		want     bool
		wantErr  bool
		requests int
	}{
		{"stock algod", "", nil, false, false, 1},
		{"time travel off", limits(false), nil, false, false, 1},
		{"index off", limits(true), func(w http.ResponseWriter) {
			reply(w, 400, `{"message":"asset holders require EnableTimeTravel and TimeTravelHoldersIndex"}`)
		}, false, false, 2},
		{"index on", limits(true), func(w http.ResponseWriter) {
			reply(w, 400, `{"message":"limit must be between 1 and 10000"}`)
		}, true, false, 2},
		{"index being built", limits(true), func(w http.ResponseWriter) {
			reply(w, 503, `{"message":"timetravel: the asset holders index is not complete"}`)
		}, false, true, 2},
	}
	for _, tc := range cases {
		c, s := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v2/capabilities" && tc.limits != "":
				reply(w, 200, tc.limits)
			case r.URL.Path == "/v2/assets/0/holders" && tc.holders != nil:
				assert.Equal(t, "limit=0", r.URL.RawQuery)
				tc.holders(w)
			default:
				reply(w, 404, `{"message":"Not Found"}`)
			}
		})
		got, err := c.SupportsAssetHolders(ctx)
		assert.Equal(t, tc.wantErr, err != nil, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
		assert.Len(t, s.requests, tc.requests, tc.name)
	}
}

func TestDeltasBusy(t *testing.T) {
	assert.True(t, IsNodeBusy(upstreamErr(503, `{"message":"every slot for re-evaluating old rounds is in use"}`)))
}

// upstreamErr returns the error the client returns for an error response.
func upstreamErr(code int, body string) error {
	e := common.HTTPError{StatusCode: code, Message: body}
	switch code {
	case 400:
		return common.BadRequest{HTTPError: e}
	case 401:
		return common.InvalidToken{HTTPError: e}
	case 404:
		return common.NotFound{HTTPError: e}
	case 500:
		return common.InternalError{HTTPError: e}
	}
	return e
}

func TestStatusCodeOfUpstreamErrors(t *testing.T) {
	for _, code := range []int{400, 401, 404, 418, 500, 503} {
		got, ok := StatusCode(fmt.Errorf("wrapped: %w", upstreamErr(code, "x")))
		assert.True(t, ok, code)
		assert.Equal(t, code, got)
	}
}
