package algod

// This file is part of go-dtalgorand-sdk: POST /v2/query, the simulate
// limits, and the DTFeatures probe.

import (
	"context"
	"errors"
	"net/http"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/encoding/json"
)

// MaxQueryRequestBytes is the most bytes a POST /v2/query body may have, a
// fixed limit of the node that SimulateLimits does not report. The client
// does not check it.
const MaxQueryRequestBytes = 1_000_000

// QueryLogOverheadBytes is what each log counts toward
// QueryLimits.MaxResponseBytes besides its own length.
const QueryLogOverheadBytes = 48

// Query runs read-only calls of a program, or of an existing application, at
// one or more rounds (POST /v2/query), on a time-travel node. See
// models.QueryRequest. A node without the endpoint (stock algod, or an older
// fork build), or with queries disabled, gives a *DTUnsupportedError.
type Query struct {
	c *Client

	request models.QueryRequest
}

// Query runs read-only calls at one or more rounds (POST /v2/query).
func (c *Client) Query(request models.QueryRequest) *Query {
	return &Query{c: c, request: request}
}

// Do performs the HTTP request. The body is sent as JSON, so that the node's
// decoding errors name the field at fault. It goes out as given because
// common registers /v2/query as a raw request path (dt_response.go).
func (s *Query) Do(ctx context.Context, headers ...*common.Header) (response models.QueryResponse, err error) {
	const op = "POST /v2/query"
	err = dtCall(ctx, func(ctx context.Context) error {
		return s.c.post(ctx, &response, "/v2/query", nil, headers, json.Encode(&s.request))
	})
	switch {
	case err == nil:
	case hasStatus(err, http.StatusNotFound, nil):
		err = unsupported(op, "the node has no query endpoint (stock algod, or a fork build without it)", err)
	case hasStatus(err, http.StatusBadRequest, reQueryDisabled):
		err = unsupported(op, "the node has queries disabled (MaxAPIQueryCalls is 0)", err)
	}
	return
}

// SimulateLimits gets a time-travel node's limits on simulate and query
// requests (GET /v2/transactions/simulate/limits). Every fork node serves it;
// stock algod gives a *DTUnsupportedError.
type SimulateLimits struct {
	c *Client
}

// SimulateLimits gets the node's simulate and query limits.
func (c *Client) SimulateLimits() *SimulateLimits {
	return &SimulateLimits{c: c}
}

// Do performs the HTTP request.
func (s *SimulateLimits) Do(ctx context.Context, headers ...*common.Header) (response models.SimulateLimitsResponse, err error) {
	err = dtCall(ctx, func(ctx context.Context) error {
		return s.c.get(ctx, &response, "/v2/transactions/simulate/limits", nil, headers)
	})
	if hasStatus(err, http.StatusNotFound, nil) || hasStatus(err, http.StatusMethodNotAllowed, nil) {
		err = unsupported("GET /v2/transactions/simulate/limits", "the node has no simulate limits endpoint (stock algod, or an older fork build)", err)
	}
	return
}

// DTFeatures is what DTFeatures found out about a node.
type DTFeatures struct {
	// Supported is whether the node runs dt-algorand, whatever its config.
	Supported bool
	// TimeTravel is whether it serves past rounds (EnableTimeTravel): reads
	// at a round, versions, and holders with the holders index.
	TimeTravel bool
	// Limits holds the node's simulate and query limits; nil for stock algod
	// and for fork builds older than the limits endpoint. Limits.Query says
	// whether it takes queries.
	Limits *models.SimulateLimitsResponse
}

// DTFeatures finds out whether the node runs dt-algorand and serves past
// rounds, for code that must also run against stock algod. It asks for the
// simulate limits, and if the node has none, probes a versions route, which
// older fork builds have when they serve past rounds.
// Errors other than "not supported" (network errors, 401) are returned. It
// does not cache: keep the answer. Whether the node has the asset holders
// index, or sends the effort header, shows only on real requests.
func (c *Client) DTFeatures(ctx context.Context) (DTFeatures, error) {
	limits, err := c.SimulateLimits().Do(ctx)
	if err == nil {
		return DTFeatures{Supported: true, TimeTravel: limits.TimeTravel, Limits: &limits}, nil
	}
	if !errors.Is(err, ErrDTUnsupported) {
		return DTFeatures{}, err
	}
	_, err = c.ApplicationBoxVersions(0, []byte{0}).Limit(1).Do(ctx)
	switch {
	case err == nil:
		return DTFeatures{Supported: true, TimeTravel: true}, nil
	case errors.Is(err, ErrDTUnsupported):
		return DTFeatures{}, nil
	case hasStatus(err, http.StatusBadRequest, nil):
		// The route exists but rejected the probe.
		return DTFeatures{Supported: true, TimeTravel: true}, nil
	}
	return DTFeatures{}, err
}
