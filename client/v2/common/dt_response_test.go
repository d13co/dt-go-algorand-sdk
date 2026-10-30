package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryIsRawRequestPath(t *testing.T) {
	assert.True(t, rawRequestPaths["/v2/query"], "POST /v2/query must send its body raw")
}

func TestResponseInfo(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Algod-Effort", "opcodes=1")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := MakeClient(server.URL, "X-Algo-API-Token", "")
	require.NoError(t, err)

	// Filled on success.
	var info ResponseInfo
	ctx := WithResponseInfo(context.Background(), &info)
	var out map[string]interface{}
	require.NoError(t, client.Get(ctx, &out, "/v2/status", nil, nil))
	assert.Equal(t, http.StatusOK, info.StatusCode)
	assert.Equal(t, "opcodes=1", info.Header.Get("X-Algod-Effort"))

	// Filled on error, and the error is the same with and without it.
	status = http.StatusServiceUnavailable
	plainErr := client.Get(context.Background(), &out, "/v2/status", nil, nil)
	infoErr := client.Get(ctx, &out, "/v2/status", nil, nil)
	require.Error(t, plainErr)
	assert.Equal(t, plainErr, infoErr)
	assert.Equal(t, http.StatusServiceUnavailable, info.StatusCode)
	assert.Equal(t, "1", info.Header.Get("Retry-After"))

	// GetRaw and GetRawMsgpack go through the same hook.
	_, _ = client.GetRaw(ctx, "/v2/status", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, info.StatusCode)
	_ = client.GetRawMsgpack(ctx, &out, "/v2/status", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, info.StatusCode)

	// Cleared when there is no response.
	server.Close()
	require.Error(t, client.Get(ctx, &out, "/v2/status", nil, nil))
	assert.Equal(t, ResponseInfo{}, info)
}

func TestWrapResponseError(t *testing.T) {
	cause := errors.New("HTTP 503: busy")
	assert.Nil(t, WrapResponseError(nil, &ResponseInfo{StatusCode: 503}))
	assert.Equal(t, cause, WrapResponseError(cause, nil))
	assert.Equal(t, cause, WrapResponseError(cause, &ResponseInfo{}), "no response")
	assert.Equal(t, cause, WrapResponseError(cause, &ResponseInfo{StatusCode: 200}), "decode error")

	wrapped := WrapResponseError(cause, &ResponseInfo{StatusCode: 503, Header: http.Header{"Retry-After": {"1"}}})
	var httpErr *ResponseError
	require.ErrorAs(t, wrapped, &httpErr)
	assert.Equal(t, 503, httpErr.StatusCode)
	assert.Equal(t, cause.Error(), wrapped.Error())
	assert.ErrorIs(t, wrapped, cause)
}

func TestResponseInfoClearedOnEarlyError(t *testing.T) {
	client, err := MakeClient("http://127.0.0.1:1", "X-Algo-API-Token", "")
	require.NoError(t, err)
	info := ResponseInfo{StatusCode: 503, Header: http.Header{"Retry-After": {"1"}}}
	ctx := WithResponseInfo(context.Background(), &info)
	// A raw-body path with a body that is not bytes fails before sending.
	err = client.Post(ctx, nil, "/v2/query", nil, nil, "not bytes")
	require.Error(t, err)
	assert.Equal(t, ResponseInfo{}, info, "no stale status from an earlier request")
}
