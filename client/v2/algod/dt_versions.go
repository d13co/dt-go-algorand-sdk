package algod

// This file is part of go-dtalgorand-sdk: the /versions endpoints of a
// time-travel algod, which list the rounds at which an object changed, or
// with Rounds, the object as of each of a list of rounds.

import (
	"context"
	"encoding/base64"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

// versionsParams are the query parameters of the versions endpoints.
type versionsParams struct {
	MinRound *uint64 `url:"min-round,omitempty"`
	MaxRound *uint64 `url:"max-round,omitempty"`
	Order    string  `url:"order,omitempty"`
	Limit    *uint64 `url:"limit,omitempty"`
	Next     string  `url:"next,omitempty"`
	Include  string  `url:"include,omitempty"`
	Exclude  string  `url:"exclude,omitempty"`
	Name     *string `url:"name,omitempty"`
	Rounds   string  `url:"rounds,omitempty"`
	Key      *string `url:"key,omitempty"`
	Prefix   *string `url:"prefix,omitempty"`
}

// appArg encodes bytes in the goal app call argument form the node takes.
func appArg(b []byte) *string {
	s := "b64:" + base64.StdEncoding.EncodeToString(b)
	return &s
}

// versionsPage tells a VersionsQuery how to read its responses.
type versionsPage[R, V any] struct {
	versions func(*R) []V
	next     func(*R) string
	round    func(*V) uint64
	// keysIgnored reports whether a page asked for with include=keys lacks
	// the changed keys where they must be; nil where keys don't apply.
	keysIgnored func(*R) bool
}

// VersionsQuery is a request to one of the versions endpoints of a
// time-travel node: R is its response (a page) and V a version. The
// endpoints are registered only with EnableTimeTravel: a node without them
// gives a *DTUnsupportedError.
//
// By default a page lists the rounds in [MinRound, MaxRound] at which the
// object changed, oldest first. With Rounds, it lists the object as of each
// requested round instead. Pages can be short, or with Key or Prefix empty,
// and still have a next token: Pages and All follow them.
//
// Keys, Key and Prefix apply to some endpoints only (see each constructor);
// elsewhere the node answers 400. Exclude applies to account versions only;
// elsewhere the node ignores it.
type VersionsQuery[R, V any] struct {
	c      *Client
	op     string
	path   string
	page   versionsPage[R, V]
	p      versionsParams
	values bool
	keys   bool
	rounds []uint64 // sorted and deduplicated; nil when unset
}

// MinRound includes the versions at this round or later (default 0).
func (s *VersionsQuery[R, V]) MinRound(round uint64) *VersionsQuery[R, V] {
	s.p.MinRound = &round
	return s
}

// MaxRound includes the versions at this round or earlier (default the
// latest round).
func (s *VersionsQuery[R, V]) MaxRound(round uint64) *VersionsQuery[R, V] {
	s.p.MaxRound = &round
	return s
}

// Rounds asks for the object as of each of these rounds instead of its
// versions: one entry per distinct round, at most 10,000, with ChangedRound
// the round of the version in effect. It can't be combined with MinRound,
// MaxRound or Key. Limit then counts rounds. Nil or empty unsets it. A very
// long list makes a long URL, which some proxies refuse (414).
func (s *VersionsQuery[R, V]) Rounds(rounds []uint64) *VersionsQuery[R, V] {
	if len(rounds) == 0 {
		s.rounds = nil
		s.p.Rounds = ""
		return s
	}
	s.rounds = slices.Compact(slices.Sorted(slices.Values(rounds)))
	parts := make([]string, len(s.rounds))
	for i, r := range s.rounds {
		parts[i] = strconv.FormatUint(r, 10)
	}
	s.p.Rounds = strings.Join(parts, ",")
	return s
}

// Desc lists the newest versions first.
func (s *VersionsQuery[R, V]) Desc() *VersionsQuery[R, V] {
	s.p.Order = "desc"
	return s
}

// Limit is the most versions (or with Rounds, rounds) per page: default
// 100, at most 10,000, or 1,000 with Values or Keys.
func (s *VersionsQuery[R, V]) Limit(limit uint64) *VersionsQuery[R, V] {
	s.p.Limit = &limit
	return s
}

// Next resumes from a previous page's next token. Pages and All follow the
// tokens themselves.
func (s *VersionsQuery[R, V]) Next(token string) *VersionsQuery[R, V] {
	s.p.Next = token
	return s
}

// Values includes the object's value in each version.
func (s *VersionsQuery[R, V]) Values() *VersionsQuery[R, V] {
	s.values = true
	return s
}

// Keys lists, on each application params or local state version, the state
// keys that changed (ChangedKeys). Account, account application and
// application versions only.
func (s *VersionsQuery[R, V]) Keys() *VersionsQuery[R, V] {
	s.keys = true
	return s
}

// Key keeps only the versions where this global (application versions) or
// local (account application versions) state key changed, each with its new
// value. An empty key is a valid key. Up to 64 bytes; not with Rounds.
func (s *VersionsQuery[R, V]) Key(key []byte) *VersionsQuery[R, V] {
	s.p.Key = appArg(key)
	return s
}

// Prefix keeps only the boxes whose names start with prefix, 1 to 64 bytes.
// Application boxes versions only.
func (s *VersionsQuery[R, V]) Prefix(prefix []byte) *VersionsQuery[R, V] {
	s.p.Prefix = appArg(prefix)
	return s
}

// Exclude narrows account versions as /v2/accounts/{address} does: "all"
// (the base record only), "none", "created-apps-params",
// "created-assets-params". Account versions only: other endpoints ignore it.
func (s *VersionsQuery[R, V]) Exclude(exclude ...string) *VersionsQuery[R, V] {
	s.p.Exclude = strings.Join(exclude, ",")
	return s
}

// query returns the request's query parameters.
func (s *VersionsQuery[R, V]) query() versionsParams {
	q := s.p
	var include []string
	if s.values {
		include = append(include, "values")
	}
	if s.keys {
		include = append(include, "keys")
	}
	q.Include = strings.Join(include, ",")
	return q
}

// Do gets one page.
func (s *VersionsQuery[R, V]) Do(ctx context.Context, headers ...*common.Header) (R, error) {
	return s.fetch(ctx, s.query(), headers)
}

// Pages gets every page from the request's, following the next tokens until
// a page has none. Pages can be short, or empty, and still have a token. It
// stops at the first error, which it yields. The request is not modified.
func (s *VersionsQuery[R, V]) Pages(ctx context.Context, headers ...*common.Header) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		q := s.query()
		for {
			page, err := s.fetch(ctx, q, headers)
			if err != nil {
				yield(page, err)
				return
			}
			next := s.page.next(&page)
			if next != "" && next == q.Next {
				var zero R
				yield(zero, fmt.Errorf("%s: the node answered with the next token it was sent (%s), so paging would never end", s.op, next))
				return
			}
			if !yield(page, nil) || next == "" {
				return
			}
			q.Next = next
		}
	}
}

// All gets every version from the request's page on, fetching pages as
// needed (see Pages). It stops at the first error, which it yields.
func (s *VersionsQuery[R, V]) All(ctx context.Context, headers ...*common.Header) iter.Seq2[V, error] {
	return func(yield func(V, error) bool) {
		for page, err := range s.Pages(ctx, headers...) {
			if err != nil {
				var zero V
				yield(zero, err)
				return
			}
			for _, v := range s.page.versions(&page) {
				if !yield(v, nil) {
					return
				}
			}
		}
	}
}

// fetch gets a page and checks that the node applied rounds and keys.
func (s *VersionsQuery[R, V]) fetch(ctx context.Context, q versionsParams, headers []*common.Header) (page R, err error) {
	err = dtCall(ctx, func(ctx context.Context) error {
		return s.c.get(ctx, &page, s.path, q, headers)
	})
	switch {
	case err == nil:
	case hasStatus(err, http.StatusNotFound, nil),
		hasStatus(err, http.StatusBadRequest, reVersionsMisparse),
		hasStatus(err, http.StatusBadRequest, reVersionsOff):
		return page, unsupported(s.op, "the node has no versions endpoints (EnableTimeTravel)", err)
	case hasStatus(err, http.StatusBadRequest, reKeysUnknown):
		return page, unsupported(s.op, "the node rejected include=keys: it does not list changed keys", err)
	default:
		return page, err
	}
	if s.rounds != nil {
		versions := s.page.versions(&page)
		if len(versions) == 0 {
			return page, unsupported(s.op, "the node answered rounds with no entries: it ignored rounds", nil)
		}
		for i := range versions {
			r := s.page.round(&versions[i])
			if _, found := slices.BinarySearch(s.rounds, r); !found {
				e := unsupported(s.op, fmt.Sprintf("the node answered an entry for round %d, which was not requested: it ignored rounds", r), nil)
				e.ReturnedRound = &r
				return page, e
			}
		}
	}
	if s.keys && s.page.keysIgnored != nil && s.page.keysIgnored(&page) {
		return page, unsupported(s.op, "the node answered include=keys without changed-keys: it ignored keys", nil)
	}
	return page, nil
}

// AccountVersions lists the versions of an account. See VersionsQuery.
type AccountVersions = VersionsQuery[models.AccountVersionsResponse, models.AccountVersion]

// AccountAssetVersions lists the versions of an account's holding of an
// asset. See VersionsQuery.
type AccountAssetVersions = VersionsQuery[models.AccountVersionsResponse, models.AccountVersion]

// AccountApplicationVersions lists the versions of an account's local state
// of an application. See VersionsQuery.
type AccountApplicationVersions = VersionsQuery[models.AccountVersionsResponse, models.AccountVersion]

// AssetVersions lists the versions of an asset's params. See VersionsQuery.
type AssetVersions = VersionsQuery[models.AssetVersionsResponse, models.AssetVersion]

// ApplicationVersions lists the versions of an application's params. See
// VersionsQuery.
type ApplicationVersions = VersionsQuery[models.ApplicationVersionsResponse, models.ApplicationVersion]

// ApplicationBoxVersions lists the versions of a box. See VersionsQuery.
type ApplicationBoxVersions = VersionsQuery[models.BoxVersionsResponse, models.BoxVersion]

// ApplicationBoxesVersions lists the rounds at which an application's boxes
// changed. See VersionsQuery.
type ApplicationBoxesVersions = VersionsQuery[models.BoxesVersionsResponse, models.BoxesVersion]

var accountPage = versionsPage[models.AccountVersionsResponse, models.AccountVersion]{
	versions: func(r *models.AccountVersionsResponse) []models.AccountVersion { return r.Versions },
	next:     func(r *models.AccountVersionsResponse) string { return r.NextToken },
	round:    func(v *models.AccountVersion) uint64 { return v.Round },
	// A local state that exists must have changed keys, even an empty list.
	// Created applications are not checked: they have none with
	// exclude=created-apps-params.
	keysIgnored: func(r *models.AccountVersionsResponse) bool {
		for _, v := range r.Versions {
			for _, ls := range v.AppsLocalState {
				if !ls.Deleted && ls.ChangedKeys == nil {
					return true
				}
			}
		}
		return false
	},
}

// AccountVersions lists the versions of everything GET
// /v2/accounts/{address} returns: the base record, holdings, local states,
// and created assets' and applications' params. Each version lists only what
// changed at its round. It takes Exclude and Keys.
func (c *Client) AccountVersions(address string) *AccountVersions {
	return &AccountVersions{c: c, op: "GET /v2/accounts/{address}/versions",
		path: fmt.Sprintf("/v2/accounts/%s/versions", common.EscapeParams(address)...),
		page: accountPage}
}

// AccountAssetVersions lists the versions of an account's holding of an
// asset and, if the account created it, of the asset's params.
func (c *Client) AccountAssetVersions(address string, assetId uint64) *AccountAssetVersions {
	page := accountPage
	page.keysIgnored = nil
	return &AccountAssetVersions{c: c, op: "GET /v2/accounts/{address}/assets/{asset-id}/versions",
		path: fmt.Sprintf("/v2/accounts/%s/assets/%s/versions", common.EscapeParams(address, assetId)...),
		page: page}
}

// AccountApplicationVersions lists the versions of an account's local state
// of an application and, if the account created it, of its params. It takes
// Keys and Key (a local state key).
func (c *Client) AccountApplicationVersions(address string, applicationId uint64) *AccountApplicationVersions {
	return &AccountApplicationVersions{c: c, op: "GET /v2/accounts/{address}/applications/{application-id}/versions",
		path: fmt.Sprintf("/v2/accounts/%s/applications/%s/versions", common.EscapeParams(address, applicationId)...),
		page: accountPage}
}

// AssetVersions lists the versions of an asset's params.
func (c *Client) AssetVersions(assetId uint64) *AssetVersions {
	return &AssetVersions{c: c, op: "GET /v2/assets/{asset-id}/versions",
		path: fmt.Sprintf("/v2/assets/%s/versions", common.EscapeParams(assetId)...),
		page: versionsPage[models.AssetVersionsResponse, models.AssetVersion]{
			versions: func(r *models.AssetVersionsResponse) []models.AssetVersion { return r.Versions },
			next:     func(r *models.AssetVersionsResponse) string { return r.NextToken },
			round:    func(v *models.AssetVersion) uint64 { return v.Round },
		}}
}

// ApplicationVersions lists the versions of an application's params, global
// state included. It takes Keys and Key (a global state key).
func (c *Client) ApplicationVersions(applicationId uint64) *ApplicationVersions {
	return &ApplicationVersions{c: c, op: "GET /v2/applications/{application-id}/versions",
		path: fmt.Sprintf("/v2/applications/%s/versions", common.EscapeParams(applicationId)...),
		page: versionsPage[models.ApplicationVersionsResponse, models.ApplicationVersion]{
			versions: func(r *models.ApplicationVersionsResponse) []models.ApplicationVersion { return r.Versions },
			next:     func(r *models.ApplicationVersionsResponse) string { return r.NextToken },
			round:    func(v *models.ApplicationVersion) uint64 { return v.Round },
			keysIgnored: func(r *models.ApplicationVersionsResponse) bool {
				for _, v := range r.Versions {
					if !v.Deleted && v.ChangedKeys == nil {
						return true
					}
				}
				return false
			},
		}}
}

// ApplicationBoxVersions lists the versions of an application's box.
func (c *Client) ApplicationBoxVersions(applicationId uint64, name []byte) *ApplicationBoxVersions {
	s := &ApplicationBoxVersions{c: c, op: "GET /v2/applications/{application-id}/box/versions",
		path: fmt.Sprintf("/v2/applications/%s/box/versions", common.EscapeParams(applicationId)...),
		page: versionsPage[models.BoxVersionsResponse, models.BoxVersion]{
			versions: func(r *models.BoxVersionsResponse) []models.BoxVersion { return r.Versions },
			next:     func(r *models.BoxVersionsResponse) string { return r.NextToken },
			round:    func(v *models.BoxVersion) uint64 { return v.Round },
		}}
	s.p.Name = appArg(name)
	return s
}

// ApplicationBoxesVersions lists the rounds at which any box of an
// application changed, with the boxes that did, in name order. It takes
// Prefix. Ranges before the node's box index is complete get a 503
// (IsHistoryUnavailable).
func (c *Client) ApplicationBoxesVersions(applicationId uint64) *ApplicationBoxesVersions {
	return &ApplicationBoxesVersions{c: c, op: "GET /v2/applications/{application-id}/boxes/versions",
		path: fmt.Sprintf("/v2/applications/%s/boxes/versions", common.EscapeParams(applicationId)...),
		page: versionsPage[models.BoxesVersionsResponse, models.BoxesVersion]{
			versions: func(r *models.BoxesVersionsResponse) []models.BoxesVersion { return r.Versions },
			next:     func(r *models.BoxesVersionsResponse) string { return r.NextToken },
			round:    func(v *models.BoxesVersion) uint64 { return v.Round },
		}}
}
