package algod

// This file is part of go-dtalgorand-sdk: reads at a past round. Round(r) on
// an upstream request returns a request of the same name with "At" appended,
// which sends round=r and checks that the node answered for r. An At request
// shares the upstream request's parameters: its setters change them, so don't
// set them on one base request from several goroutines.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/google/go-querystring/query"
)

// doAt performs a read at round and turns the ways a node shows it can't read
// past rounds into a *DTUnsupportedError: an answer for another round (stock
// algod ignores round), a refusal of past rounds, or a 404 from a node that is
// not a time-travel node (stock algod looked the object up at the latest
// round, where it may no longer exist). roundOf gives the round a response
// reports; nil for the responses that report none, which can't be checked.
func doAt[T any](ctx context.Context, c *Client, op, path string, params interface{}, round uint64, roundOf func(*T) uint64, headers []*common.Header) (response T, err error) {
	err = dtCall(ctx, func(ctx context.Context) error {
		return c.get(ctx, &response, path, params, headers)
	})
	if err != nil {
		switch {
		case isPastRoundUnsupported(err):
			e := unsupported(op, fmt.Sprintf("the node can't read round %d", round), err)
			e.RequestedRound = &round
			return response, e
		case hasStatus(err, http.StatusNotFound, nil) && !c.probeDT(ctx, headers):
			e := unsupported(op, fmt.Sprintf("the node answered 404 for round %d but is not a time-travel node, so it looked at the latest round instead", round), err)
			e.RequestedRound = &round
			return response, e
		}
		return response, err
	}
	if roundOf != nil {
		if got := roundOf(&response); got != round {
			e := unsupported(op, fmt.Sprintf("asked for round %d but the node answered for round %d; it does not support reads at past rounds", round, got), nil)
			e.RequestedRound, e.ReturnedRound = &round, &got
			return response, e
		}
	}
	return response, nil
}

// dtNodes caches, per node URL, a probe that found the node serves past
// rounds. It holds one entry per node a program talks to.
var dtNodes sync.Map // string -> struct{}

// probeDT reports whether the node serves past rounds, so that its 404 for a
// round is its answer for that round. It assumes so when the probe fails (a
// gateway answering 401 for unknown paths, a network error): a stock node's
// 404 is then returned as a plain 404. A yes is cached per node URL. The
// probe doesn't touch the caller's ResponseInfo.
func (c *Client) probeDT(ctx context.Context, headers []*common.Header) bool {
	node := (*common.Client)(c).ServerURL()
	if _, ok := dtNodes.Load(node); ok {
		return true
	}
	ctx = common.WithResponseInfo(ctx, &common.ResponseInfo{})
	limits, err := c.SimulateLimits().Do(ctx, headers...)
	if err == nil {
		if limits.TimeTravel {
			dtNodes.Store(node, struct{}{})
		}
		return limits.TimeTravel
	}
	if !errors.Is(err, ErrDTUnsupported) {
		return true
	}
	_, err = c.ApplicationBoxVersions(0, []byte{0}).Limit(1).Do(ctx, headers...)
	switch {
	case err == nil, hasStatus(err, http.StatusBadRequest, nil) && !errors.Is(err, ErrDTUnsupported):
		dtNodes.Store(node, struct{}{})
		return true
	case errors.Is(err, ErrDTUnsupported):
		return false
	}
	return true
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
// Round 0 is the genesis. The response's round is checked: stock algod gives
// a *DTUnsupportedError.
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
	return doAt(ctx, a.s.c, "GET /v2/accounts/{address}",
		fmt.Sprintf("/v2/accounts/%s", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.Account) uint64 { return r.Round }, headers)
}

// AccountAssetInformationAt is AccountAssetInformation at a past round.
type AccountAssetInformationAt struct {
	s     *AccountAssetInformation
	round uint64
}

// Round reads the holding as of the end of round r, on a time-travel node.
// Round 0 is the genesis. The response's round is checked: stock algod gives
// a *DTUnsupportedError.
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
	return doAt(ctx, a.s.c, "GET /v2/accounts/{address}/assets/{asset-id}",
		fmt.Sprintf("/v2/accounts/%s/assets/%s", common.EscapeParams(a.s.address, a.s.assetId)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.AccountAssetResponse) uint64 { return r.Round }, headers)
}

// AccountAssetsInformationAt is AccountAssetsInformation at a past round.
type AccountAssetsInformationAt struct {
	s     *AccountAssetsInformation
	round uint64
}

// Round lists the holdings as of the end of round r, on a time-travel node.
// Round 0 is the genesis. The response's round is checked: stock algod gives
// a *DTUnsupportedError. Page with Next at the same round.
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
	return doAt(ctx, a.s.c, "GET /v2/accounts/{address}/assets",
		fmt.Sprintf("/v2/accounts/%s/assets", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.AccountAssetsInformationResponse) uint64 { return r.Round }, headers)
}

// AccountApplicationsInformationAt is AccountApplicationsInformation at a
// past round.
type AccountApplicationsInformationAt struct {
	s     *AccountApplicationsInformation
	round uint64
}

// Round lists the applications as of the end of round r, on a time-travel
// node. Round 0 is the genesis. The response's round is checked: stock algod
// gives a *DTUnsupportedError. Page with Next at the same round.
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
	return doAt(ctx, a.s.c, "GET /v2/accounts/{address}/applications",
		fmt.Sprintf("/v2/accounts/%s/applications", common.EscapeParams(a.s.address)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.AccountApplicationsInformationResponse) uint64 { return r.Round }, headers)
}

// AccountApplicationInformationAt is AccountApplicationInformation at a past
// round.
type AccountApplicationInformationAt struct {
	s     *AccountApplicationInformation
	round uint64
}

// Round reads the local state and params as of the end of round r, on a
// time-travel node. Round 0 is the genesis. The response's round is checked:
// stock algod gives a *DTUnsupportedError.
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
	return doAt(ctx, a.s.c, "GET /v2/accounts/{address}/applications/{application-id}",
		fmt.Sprintf("/v2/accounts/%s/applications/%s", common.EscapeParams(a.s.address, a.s.applicationId)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.AccountApplicationResponse) uint64 { return r.Round }, headers)
}

// GetApplicationBoxByNameAt is GetApplicationBoxByName at a past round.
type GetApplicationBoxByNameAt struct {
	s     *GetApplicationBoxByName
	round uint64
}

// Round reads the box as of the end of round r, on a time-travel node.
// Round 0 is the genesis. The response's round is checked: stock algod gives
// a *DTUnsupportedError.
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
	return doAt(ctx, a.s.c, "GET /v2/applications/{application-id}/box",
		fmt.Sprintf("/v2/applications/%s/box", common.EscapeParams(a.s.applicationId)...),
		withRound(a.s.p, a.round), a.round,
		func(r *models.Box) uint64 { return r.Round }, headers)
}

// SupplyAt is Supply at a past round.
type SupplyAt struct {
	s     *Supply
	round uint64
}

// Round reads the totals as of the end of round r, on a time-travel node.
// Round 0 is the genesis. The response's round is checked: stock algod gives
// a *DTUnsupportedError. A node whose history did not record the round's
// online stake answers 404 (IsOnlineStakeNotRecorded).
func (s *Supply) Round(r uint64) *SupplyAt {
	return &SupplyAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *SupplyAt) Round(r uint64) *SupplyAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *SupplyAt) Do(ctx context.Context, headers ...*common.Header) (models.Supply, error) {
	return doAt(ctx, a.s.c, "GET /v2/ledger/supply", "/v2/ledger/supply",
		withRound(nil, a.round), a.round,
		func(r *models.Supply) uint64 { return r.Current_round }, headers)
}

// GetAssetByIDAt is GetAssetByID at a past round.
type GetAssetByIDAt struct {
	s     *GetAssetByID
	round uint64
}

// Round reads the asset's params as of the end of round r, on a time-travel
// node. Round 0 is the genesis. The response reports no round, so a stock
// algod that ignores round and answers with the latest params can't be told
// apart, except when the asset no longer exists (a *DTUnsupportedError).
// Check DTFeatures first when talking to unknown nodes.
func (s *GetAssetByID) Round(r uint64) *GetAssetByIDAt {
	return &GetAssetByIDAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *GetAssetByIDAt) Round(r uint64) *GetAssetByIDAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *GetAssetByIDAt) Do(ctx context.Context, headers ...*common.Header) (models.Asset, error) {
	return doAt[models.Asset](ctx, a.s.c, "GET /v2/assets/{asset-id}",
		fmt.Sprintf("/v2/assets/%s", common.EscapeParams(a.s.assetId)...),
		withRound(nil, a.round), a.round, nil, headers)
}

// GetApplicationByIDAt is GetApplicationByID at a past round.
type GetApplicationByIDAt struct {
	s     *GetApplicationByID
	round uint64
}

// Round reads the application's params, global state included, as of the end
// of round r, on a time-travel node. Round 0 is the genesis. The response
// reports no round, so a stock algod that ignores round and answers with the
// latest params can't be told apart, except when the application no longer
// exists (a *DTUnsupportedError). Check DTFeatures first when talking to
// unknown nodes.
func (s *GetApplicationByID) Round(r uint64) *GetApplicationByIDAt {
	return &GetApplicationByIDAt{s: s, round: r}
}

// Round sets the round to read at.
func (a *GetApplicationByIDAt) Round(r uint64) *GetApplicationByIDAt { a.round = r; return a }

// Do performs the HTTP request.
func (a *GetApplicationByIDAt) Do(ctx context.Context, headers ...*common.Header) (models.Application, error) {
	return doAt[models.Application](ctx, a.s.c, "GET /v2/applications/{application-id}",
		fmt.Sprintf("/v2/applications/%s", common.EscapeParams(a.s.applicationId)...),
		withRound(nil, a.round), a.round, nil, headers)
}

// GetApplicationBoxesAt lists an application's boxes at a round, checking
// that the node answered for it. Upstream's GetApplicationBoxes.Round sends
// the round but doesn't check it, and drops round 0.
type GetApplicationBoxesAt struct {
	s     *GetApplicationBoxes
	round uint64
}

// GetApplicationBoxesAt lists an application's boxes as of the end of round,
// on a time-travel node (stock algod serves only its last few rounds).
// Round 0 is the genesis. The response's round is checked: a node that
// ignored it gives a *DTUnsupportedError. Page with Next at the same round.
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
	return doAt(ctx, a.s.c, "GET /v2/applications/{application-id}/boxes",
		fmt.Sprintf("/v2/applications/%s/boxes", common.EscapeParams(a.s.applicationId)...),
		withRound(p, a.round), a.round,
		func(r *models.BoxesResponse) uint64 { return r.Round }, headers)
}
