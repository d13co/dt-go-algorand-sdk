package algod

// This file is part of go-dtalgorand-sdk: POST /v2/query, the node's
// capabilities, and the DTFeatures probe.

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/encoding/json"
)

// MaxQueryRequestBytes is the most bytes a POST /v2/query body may have, a
// fixed limit of the node that Capabilities does not report. The client
// does not check it.
const MaxQueryRequestBytes = 1_000_000

// QueryLogOverheadBytes is what each log counts toward
// QueryLimits.MaxResponseBytes besides its own length.
const QueryLogOverheadBytes = 48

// Query runs read-only calls of a program, or of an existing application, at
// one or more rounds (POST /v2/query), on a time-travel node. See
// models.QueryRequest. Stock algod, or a node with queries disabled, gives a
// *DTUnsupportedError.
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
		err = unsupported(op, "the node has no query endpoint (stock algod)", err)
	case hasStatus(err, http.StatusBadRequest, reQueryDisabled):
		err = unsupported(op, "the node has queries disabled (MaxAPIQueryCalls is 0)", err)
	}
	return
}

// Capabilities gets what a dt-algorand node serves and its limits on
// simulate, query and other expensive requests (GET /v2/capabilities). Every
// fork node serves it; stock algod gives a *DTUnsupportedError.
type Capabilities struct {
	c *Client
}

// Capabilities gets what the node serves and its limits.
func (c *Client) Capabilities() *Capabilities {
	return &Capabilities{c: c}
}

// Do performs the HTTP request.
func (s *Capabilities) Do(ctx context.Context, headers ...*common.Header) (response models.CapabilitiesResponse, err error) {
	err = dtCall(ctx, func(ctx context.Context) error {
		return s.c.get(ctx, &response, "/v2/capabilities", nil, headers)
	})
	if hasStatus(err, http.StatusNotFound, nil) {
		err = unsupported("GET /v2/capabilities", "the node is stock algod", err)
	}
	// 0 or absent: a node from before the limit, whose only limits are calls
	// and rounds. Saturates rather than wrapping on a nonsense response.
	if q := response.Query; err == nil && q != nil && q.MaxTotalCalls == 0 {
		if q.MaxRounds != 0 && q.MaxCalls > math.MaxUint64/q.MaxRounds {
			q.MaxTotalCalls = math.MaxUint64
		} else {
			q.MaxTotalCalls = q.MaxCalls * q.MaxRounds
		}
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
	// Capabilities holds what the node serves and its limits; nil for stock
	// algod. Capabilities.Query says whether it takes queries.
	Capabilities *models.CapabilitiesResponse
}

// DTFeatures finds out whether the node runs dt-algorand and serves past
// rounds, from its capabilities: stock algod has none (404). Reads at a
// round don't need it, as they check the node's answer themselves
// (X-Algod-TT-Round); use it to choose a code path beforehand. Other errors
// (network, 401) are returned. It does not cache: keep the answer. Older
// dt-algorand builds, without the capabilities endpoint, are not supported.
func (c *Client) DTFeatures(ctx context.Context) (DTFeatures, error) {
	caps, err := c.Capabilities().Do(ctx)
	if err == nil {
		return DTFeatures{Supported: true, TimeTravel: caps.TimeTravel, Capabilities: &caps}, nil
	}
	if errors.Is(err, ErrDTUnsupported) {
		return DTFeatures{}, nil
	}
	return DTFeatures{}, err
}

// SupportsAssetHolders reports whether the node lists asset holders
// (GetAssetHolders): it must serve past rounds and have the holders index
// (TimeTravelHoldersIndex). It asks for the capabilities, then sends a holders
// request the node rejects: for its index if off, else for its limit. An
// index still being built (503) and other errors are returned. It does not
// cache: keep the answer.
func (c *Client) SupportsAssetHolders(ctx context.Context) (bool, error) {
	features, err := c.DTFeatures(ctx)
	if err != nil || !features.TimeTravel {
		return false, err
	}
	_, err = c.GetAssetHolders(0).Limit(0).Do(ctx)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrDTUnsupported):
		return false, nil
	case hasStatus(err, http.StatusBadRequest, nil):
		// The node checks the index before the limit.
		return true, nil
	}
	return false, err
}
