package algod

// This file is part of go-dtalgorand-sdk: reads at a past round. Round(r) on
// an upstream request returns a request of the same name with "At" appended,
// which sends round=r. The node marks every answer it gives as of round r
// with the X-Algod-TT-Round header, and the SDK requires it: stock algod
// ignores round and answers for its latest round, without the header. An At
// request shares the upstream request's parameters: its setters change them,
// so don't set them on one base request from several goroutines.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/google/go-querystring/query"
)

// TTRoundHeader is the response header in which a time-travel node marks an
// answer given as of the round the request asked for (round=R): the 200, and
// any error after the node accepted the round, a 404 included. Its value is R.
// The node doesn't send it without round, nor on the answers that refuse the
// round. Reads at a round check it; read it on other requests with
// common.WithResponseInfo and ParseTTRound.
const TTRoundHeader = "X-Algod-TT-Round"

var ttRoundValue = regexp.MustCompile(`^\d{1,20}$`)

// ParseTTRound parses the X-Algod-TT-Round header of a response. It returns
// false when the header is absent or not a round.
func ParseTTRound(header http.Header) (uint64, bool) {
	value := header.Get(TTRoundHeader)
	if !ttRoundValue.MatchString(value) {
		return 0, false
	}
	round, err := strconv.ParseUint(value, 10, 64)
	return round, err == nil
}

// checkTTRound checks the answer to a request at round (err is its error, or
// nil) by its X-Algod-TT-Round header:
//   - the header holds round: the answer stands;
//   - the header holds anything else: a plain error, as the node is broken;
//   - no header on a 2xx or a 404: a *DTUnsupportedError, as the node ignored
//     round (stock algod) or predates the header;
//   - no header on another error: err, unchanged (malformed requests and
//     refusals of the round carry no header).
//
// bodyRound is for the boxes listing, which stock algod answers at recent
// rounds without the header: when not nil, a 2xx without the header is
// checked by the round bodyRound reports, and a 404 without it is err.
func checkTTRound(op string, round uint64, info *common.ResponseInfo, err error, bodyRound func() uint64) error {
	if info == nil || info.StatusCode == 0 {
		return err // no response
	}
	if value := info.Header.Get(TTRoundHeader); value != "" {
		got, ok := ParseTTRound(info.Header)
		switch {
		case !ok:
			return fmt.Errorf("%s: asked for round %d, the node answered with an invalid %s header %q", op, round, TTRoundHeader, value)
		case got != round:
			return fmt.Errorf("%s: asked for round %d, the node marked its answer as round %d", op, round, got)
		}
		return err
	}
	success := info.StatusCode < 300
	switch {
	case success && err != nil:
		return err // the body could not be decoded
	case success && bodyRound != nil:
		if got := bodyRound(); got != round {
			e := unsupported(op, fmt.Sprintf("the node answered for round %d", got), nil)
			e.RequestedRound = &round
			return e
		}
		return nil
	case success || (info.StatusCode == http.StatusNotFound && bodyRound == nil):
		e := unsupported(op, "the node did not mark its answer with the round ("+TTRoundHeader+"), so it ignored round, as stock algod does", err)
		e.RequestedRound = &round
		return e
	}
	return err
}

// doAt performs a read at round, checks the answer's X-Algod-TT-Round header
// (checkTTRound; roundOf is its bodyRound, nil but for the boxes listing),
// and turns a node's refusal of past rounds ("requires EnableTimeTravel",
// "given round is no longer available") into a *DTUnsupportedError.
func doAt[T any](ctx context.Context, c *Client, op, path string, params interface{}, round uint64, headers []*common.Header, roundOf func(*T) uint64) (response T, err error) {
	info, err := dtCallInfo(ctx, func(ctx context.Context) error {
		return c.get(ctx, &response, path, params, headers)
	})
	// Refusals of the round are never marked.
	if err != nil && info.Header.Get(TTRoundHeader) == "" && isPastRoundUnsupported(err) {
		e := unsupported(op, fmt.Sprintf("the node can't read round %d (EnableTimeTravel)", round), err)
		e.RequestedRound = &round
		return response, e
	}
	var bodyRound func() uint64
	if roundOf != nil {
		bodyRound = func() uint64 { return roundOf(&response) }
	}
	return response, checkTTRound(op, round, info, err, bodyRound)
}

// withRound returns query parameters made of params (a struct, or nil) and
// round, which is sent even when 0.
func withRound(params interface{}, round uint64) interface{} {
	return atQuery{Q: roundQuery{params: params, round: round}}
}

// atQuery holds a roundQuery, as go-querystring wants a struct.
type atQuery struct {
	Q roundQuery `url:"round"`
}

// roundQuery encodes the request's own parameters, then round.
type roundQuery struct {
	params interface{}
	round  uint64
}

// EncodeValues implements query.Encoder.
func (q roundQuery) EncodeValues(_ string, v *url.Values) error {
	if q.params != nil {
		values, err := query.Values(q.params)
		if err != nil {
			return err
		}
		for k, vs := range values {
			(*v)[k] = vs
		}
	}
	v.Set("round", strconv.FormatUint(q.round, 10))
	return nil
}

// AccountInformationAt is AccountInformation at a past round.
type AccountInformationAt struct {
	s     *AccountInformation
	round uint64
}

// Round reads the account as of the end of round r, on a time-travel node.
// Round 0 is the genesis.
func (s *AccountInformation) Round(r uint64) *AccountInformationAt {
	return &AccountInformationAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *AccountInformationAt) Round(r uint64) *AccountInformationAt { a.round = r; return a }

// Exclude is AccountInformation.Exclude.
func (a *AccountInformationAt) Exclude(Exclude []string) *AccountInformationAt {
	a.s.Exclude(Exclude)
	return a
}

// Do performs the HTTP request.
func (a *AccountInformationAt) Do(ctx context.Context, headers ...*common.Header) (models.Account, error) {
	return doAt[models.Account](ctx, a.s.c, "GET /v2/accounts/{address}",
		fmt.Sprintf("/v2/accounts/%s", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// AccountAssetInformationAt is AccountAssetInformation at a past round.
type AccountAssetInformationAt struct {
	s     *AccountAssetInformation
	round uint64
}

// Round reads the holding as of the end of round r, on a time-travel node.
// Round 0 is the genesis.
func (s *AccountAssetInformation) Round(r uint64) *AccountAssetInformationAt {
	return &AccountAssetInformationAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *AccountAssetInformationAt) Round(r uint64) *AccountAssetInformationAt {
	a.round = r
	return a
}

// Do performs the HTTP request.
func (a *AccountAssetInformationAt) Do(ctx context.Context, headers ...*common.Header) (models.AccountAssetResponse, error) {
	return doAt[models.AccountAssetResponse](ctx, a.s.c, "GET /v2/accounts/{address}/assets/{asset-id}",
		fmt.Sprintf("/v2/accounts/%s/assets/%s", common.EscapeParams(a.s.address, a.s.assetId)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// AccountAssetsInformationAt is AccountAssetsInformation at a past round.
type AccountAssetsInformationAt struct {
	s     *AccountAssetsInformation
	round uint64
}

// Round lists the holdings as of the end of round r, on a time-travel node.
// Round 0 is the genesis. Page with Next at the same round.
func (s *AccountAssetsInformation) Round(r uint64) *AccountAssetsInformationAt {
	return &AccountAssetsInformationAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *AccountAssetsInformationAt) Round(r uint64) *AccountAssetsInformationAt {
	a.round = r
	return a
}

// Limit is AccountAssetsInformation.Limit.
func (a *AccountAssetsInformationAt) Limit(Limit uint64) *AccountAssetsInformationAt {
	a.s.Limit(Limit)
	return a
}

// Next is AccountAssetsInformation.Next.
func (a *AccountAssetsInformationAt) Next(Next string) *AccountAssetsInformationAt {
	a.s.Next(Next)
	return a
}

// Do performs the HTTP request.
func (a *AccountAssetsInformationAt) Do(ctx context.Context, headers ...*common.Header) (models.AccountAssetsInformationResponse, error) {
	return doAt[models.AccountAssetsInformationResponse](ctx, a.s.c, "GET /v2/accounts/{address}/assets",
		fmt.Sprintf("/v2/accounts/%s/assets", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// AccountApplicationsInformationAt is AccountApplicationsInformation at a
// past round.
type AccountApplicationsInformationAt struct {
	s     *AccountApplicationsInformation
	round uint64
}

// Round lists the applications as of the end of round r, on a time-travel
// node. Round 0 is the genesis. Page with Next at the same round.
func (s *AccountApplicationsInformation) Round(r uint64) *AccountApplicationsInformationAt {
	return &AccountApplicationsInformationAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *AccountApplicationsInformationAt) Round(r uint64) *AccountApplicationsInformationAt {
	a.round = r
	return a
}

// Include is AccountApplicationsInformation.Include.
func (a *AccountApplicationsInformationAt) Include(Include []string) *AccountApplicationsInformationAt {
	a.s.Include(Include)
	return a
}

// Limit is AccountApplicationsInformation.Limit.
func (a *AccountApplicationsInformationAt) Limit(Limit uint64) *AccountApplicationsInformationAt {
	a.s.Limit(Limit)
	return a
}

// Next is AccountApplicationsInformation.Next.
func (a *AccountApplicationsInformationAt) Next(Next string) *AccountApplicationsInformationAt {
	a.s.Next(Next)
	return a
}

// Do performs the HTTP request.
func (a *AccountApplicationsInformationAt) Do(ctx context.Context, headers ...*common.Header) (models.AccountApplicationsInformationResponse, error) {
	return doAt[models.AccountApplicationsInformationResponse](ctx, a.s.c, "GET /v2/accounts/{address}/applications",
		fmt.Sprintf("/v2/accounts/%s/applications", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// AccountApplicationInformationAt is AccountApplicationInformation at a past
// round.
type AccountApplicationInformationAt struct {
	s     *AccountApplicationInformation
	round uint64
}

// Round reads the local state and params as of the end of round r, on a
// time-travel node. Round 0 is the genesis.
func (s *AccountApplicationInformation) Round(r uint64) *AccountApplicationInformationAt {
	return &AccountApplicationInformationAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *AccountApplicationInformationAt) Round(r uint64) *AccountApplicationInformationAt {
	a.round = r
	return a
}

// Do performs the HTTP request.
func (a *AccountApplicationInformationAt) Do(ctx context.Context, headers ...*common.Header) (models.AccountApplicationResponse, error) {
	return doAt[models.AccountApplicationResponse](ctx, a.s.c, "GET /v2/accounts/{address}/applications/{application-id}",
		fmt.Sprintf("/v2/accounts/%s/applications/%s", common.EscapeParams(a.s.address, a.s.applicationId)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// GetApplicationBoxByNameAt is GetApplicationBoxByName at a past round.
type GetApplicationBoxByNameAt struct {
	s     *GetApplicationBoxByName
	round uint64
}

// Round reads the box as of the end of round r, on a time-travel node.
// Round 0 is the genesis.
func (s *GetApplicationBoxByName) Round(r uint64) *GetApplicationBoxByNameAt {
	return &GetApplicationBoxByNameAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *GetApplicationBoxByNameAt) Round(r uint64) *GetApplicationBoxByNameAt {
	a.round = r
	return a
}

// Do performs the HTTP request.
func (a *GetApplicationBoxByNameAt) Do(ctx context.Context, headers ...*common.Header) (models.Box, error) {
	return doAt[models.Box](ctx, a.s.c, "GET /v2/applications/{application-id}/box",
		fmt.Sprintf("/v2/applications/%s/box", common.EscapeParams(a.s.applicationId)...),
		withRound(a.s.p, a.round), a.round, headers, nil)
}

// SupplyAt is Supply at a past round.
type SupplyAt struct {
	s     *Supply
	round uint64
}

// Round reads the totals as of the end of round r, on a time-travel node.
// Round 0 is the genesis. A node whose history did not record the round's
// online stake answers 404 (IsOnlineStakeNotRecorded).
func (s *Supply) Round(r uint64) *SupplyAt {
	return &SupplyAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *SupplyAt) Round(r uint64) *SupplyAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *SupplyAt) Do(ctx context.Context, headers ...*common.Header) (models.Supply, error) {
	return doAt[models.Supply](ctx, a.s.c, "GET /v2/ledger/supply", "/v2/ledger/supply",
		withRound(nil, a.round), a.round, headers, nil)
}

// GetAssetByIDAt is GetAssetByID at a past round.
type GetAssetByIDAt struct {
	s     *GetAssetByID
	round uint64
}

// Round reads the asset's params as of the end of round r, on a time-travel
// node. Round 0 is the genesis.
func (s *GetAssetByID) Round(r uint64) *GetAssetByIDAt {
	return &GetAssetByIDAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *GetAssetByIDAt) Round(r uint64) *GetAssetByIDAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *GetAssetByIDAt) Do(ctx context.Context, headers ...*common.Header) (models.Asset, error) {
	return doAt[models.Asset](ctx, a.s.c, "GET /v2/assets/{asset-id}",
		fmt.Sprintf("/v2/assets/%s", common.EscapeParams(a.s.assetId)...),
		withRound(nil, a.round), a.round, headers, nil)
}

// GetApplicationByIDAt is GetApplicationByID at a past round.
type GetApplicationByIDAt struct {
	s     *GetApplicationByID
	round uint64
}

// Round reads the application's params, global state included, as of the end
// of round r, on a time-travel node. Round 0 is the genesis.
func (s *GetApplicationByID) Round(r uint64) *GetApplicationByIDAt {
	return &GetApplicationByIDAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *GetApplicationByIDAt) Round(r uint64) *GetApplicationByIDAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *GetApplicationByIDAt) Do(ctx context.Context, headers ...*common.Header) (models.Application, error) {
	return doAt[models.Application](ctx, a.s.c, "GET /v2/applications/{application-id}",
		fmt.Sprintf("/v2/applications/%s", common.EscapeParams(a.s.applicationId)...),
		withRound(nil, a.round), a.round, headers, nil)
}

// GetApplicationBoxesAt lists an application's boxes at a round. Upstream's
// GetApplicationBoxes.Round drops round 0.
type GetApplicationBoxesAt struct {
	s     *GetApplicationBoxes
	round uint64
}

// GetApplicationBoxesAt lists an application's boxes as of the end of round,
// on a time-travel node (stock algod serves only its last few rounds).
// Round 0 is the genesis. Page with Next at the same round.
func (c *Client) GetApplicationBoxesAt(applicationId uint64, round uint64) *GetApplicationBoxesAt {
	return &GetApplicationBoxesAt{s: c.GetApplicationBoxes(applicationId), round: round}
}

// Round sets the round to read at.
func (a *GetApplicationBoxesAt) Round(r uint64) *GetApplicationBoxesAt { a.round = r; return a }

// Include is GetApplicationBoxes.Include.
func (a *GetApplicationBoxesAt) Include(Include []string) *GetApplicationBoxesAt {
	a.s.Include(Include)
	return a
}

// Limit is GetApplicationBoxes.Limit.
func (a *GetApplicationBoxesAt) Limit(Limit uint64) *GetApplicationBoxesAt {
	a.s.Limit(Limit)
	return a
}

// Max is GetApplicationBoxes.Max.
func (a *GetApplicationBoxesAt) Max(Max uint64) *GetApplicationBoxesAt {
	a.s.Max(Max)
	return a
}

// Next is GetApplicationBoxes.Next.
func (a *GetApplicationBoxesAt) Next(Next string) *GetApplicationBoxesAt {
	a.s.Next(Next)
	return a
}

// Prefix is GetApplicationBoxes.Prefix.
func (a *GetApplicationBoxesAt) Prefix(Prefix string) *GetApplicationBoxesAt {
	a.s.Prefix(Prefix)
	return a
}

// Do performs the HTTP request.
func (a *GetApplicationBoxesAt) Do(ctx context.Context, headers ...*common.Header) (models.BoxesResponse, error) {
	p := a.s.p
	p.Round = 0 // sent by withRound, also when 0
	return doAt[models.BoxesResponse](ctx, a.s.c, "GET /v2/applications/{application-id}/boxes",
		fmt.Sprintf("/v2/applications/%s/boxes", common.EscapeParams(a.s.applicationId)...),
		withRound(p, a.round), a.round, headers,
		func(r *models.BoxesResponse) uint64 { return r.Round })
}
