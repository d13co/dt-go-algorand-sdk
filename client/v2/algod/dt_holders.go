package algod

// This file is part of dt-go-algorand-sdk: the asset holders listing of a
// time-travel algod.

import (
	"context"
	"fmt"
	"iter"
	"net/http"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

type getAssetHoldersParams struct {
	Limit *uint64 `url:"limit,omitempty"`
	Next  string  `url:"next,omitempty"`
	Round *uint64 `url:"round,omitempty"`
}

// GetAssetHolders lists the accounts that hold an asset at a round, in
// address order, with their balance and frozen flag; accounts opted in with
// a zero balance are listed. It needs a time-travel node with the asset
// holders index (TimeTravelHoldersIndex): other nodes, stock algod
// included, give a *DTUnsupportedError. SupportsAssetHolders tells beforehand. An asset that
// doesn't exist at the round is a 404, and an index still being built a 503
// (IsHistoryUnavailable). With a round, the answer must carry it in its
// X-Algod-TT-Round header, as for reads at a round.
//
// Pages hold at most Limit holders, and stop after examining 100,000
// accounts that ever held the asset, so a page can be short, or empty, while
// more remain: only a missing next token means there are no more. Every page
// must be read at the same round: Pages and All pin the first page's round
// when Round wasn't set.
type GetAssetHolders struct {
	c *Client

	assetId uint64

	p getAssetHoldersParams
}

// GetAssetHolders lists the accounts that hold an asset at a round.
func (c *Client) GetAssetHolders(assetId uint64) *GetAssetHolders {
	return &GetAssetHolders{c: c, assetId: assetId}
}

// Round lists the holders as of the end of round r (default the latest).
// Round 0 is the genesis.
func (s *GetAssetHolders) Round(r uint64) *GetAssetHolders {
	s.p.Round = &r
	return s
}

// Limit is the most holders per page: default 1,000, at most 10,000.
func (s *GetAssetHolders) Limit(limit uint64) *GetAssetHolders {
	s.p.Limit = &limit
	return s
}

// Next resumes from a previous page's next token. Set Round to that page's
// round too: without it the node reads the latest round.
func (s *GetAssetHolders) Next(token string) *GetAssetHolders {
	s.p.Next = token
	return s
}

const assetHoldersOp = "GET /v2/assets/{asset-id}/holders"

// Do gets one page.
func (s *GetAssetHolders) Do(ctx context.Context, headers ...*common.Header) (models.AssetHoldersResponse, error) {
	return s.fetch(ctx, s.p, headers)
}

// Pages gets every page from the request's, at the request's round or else
// the first page's, following the next tokens until a page has none. It
// stops at the first error, which it yields. The request is not modified.
func (s *GetAssetHolders) Pages(ctx context.Context, headers ...*common.Header) iter.Seq2[models.AssetHoldersResponse, error] {
	return func(yield func(models.AssetHoldersResponse, error) bool) {
		q := s.p
		for {
			page, err := s.fetch(ctx, q, headers)
			if err != nil {
				yield(page, err)
				return
			}
			if page.NextToken != "" && page.NextToken == q.Next {
				yield(models.AssetHoldersResponse{}, fmt.Errorf("%s: the node answered with the next token it was sent (%s), so paging would never end", assetHoldersOp, page.NextToken))
				return
			}
			if !yield(page, nil) || page.NextToken == "" {
				return
			}
			if q.Round == nil {
				round := page.Round
				q.Round = &round
			}
			q.Next = page.NextToken
		}
	}
}

// All gets every holder from the request's page on (see Pages). It stops at
// the first error, which it yields.
func (s *GetAssetHolders) All(ctx context.Context, headers ...*common.Header) iter.Seq2[models.AssetHolder, error] {
	return func(yield func(models.AssetHolder, error) bool) {
		for page, err := range s.Pages(ctx, headers...) {
			if err != nil {
				yield(models.AssetHolder{}, err)
				return
			}
			for _, h := range page.Holders {
				if !yield(h, nil) {
					return
				}
			}
		}
	}
}

// fetch gets a page. With a round, the answer must carry it in its
// X-Algod-TT-Round header (checkTTRound).
func (s *GetAssetHolders) fetch(ctx context.Context, q getAssetHoldersParams, headers []*common.Header) (page models.AssetHoldersResponse, err error) {
	info, err := dtCallInfo(ctx, func(ctx context.Context) error {
		return s.c.get(ctx, &page, fmt.Sprintf("/v2/assets/%s/holders", common.EscapeParams(s.assetId)...), q, headers)
	})
	switch {
	case isRouteMissing(err),
		err != nil && hasStatus(err, http.StatusNotFound, nil) && !reAssetMissing.MatchString(err.Error()):
		return page, unsupported(assetHoldersOp, "the node has time travel off (EnableTimeTravel)", err)
	case err != nil && hasStatus(err, http.StatusBadRequest, reHoldersOff):
		return page, unsupported(assetHoldersOp, "the node has the asset holders index off (TimeTravelHoldersIndex)", err)
	case q.Round != nil:
		return page, checkTTRound(assetHoldersOp, *q.Round, info, err, nil)
	}
	return page, err
}
