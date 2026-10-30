package common

import (
	"context"
	"net/http"
)

// This file is part of go-dtalgorand-sdk, the time-travel fork of the SDK. It
// adds an opt-in way to see the status and headers of a response, which the
// upstream methods do not return.

func init() {
	// POST /v2/query takes a body encoded by the caller, as simulate does.
	rawRequestPaths["/v2/query"] = true
}

// ResponseInfo receives the status code and headers of a response. Pass one
// in a request's context with WithResponseInfo to read them, for example the
// X-Algod-Effort or Retry-After headers. Every request made with that
// context overwrites it: after a request that got no response (a network
// error), it is left empty. Paging methods make several requests, so it holds
// the last page's. Don't share one between concurrent requests.
type ResponseInfo struct {
	// StatusCode is the HTTP status code, or 0 if there was no response.
	StatusCode int
	// Header holds the response headers, or nil if there was no response.
	Header http.Header
}

type responseInfoKey struct{}

// WithResponseInfo returns a context that makes requests fill info with the
// status and headers of their response. It works with every request of the
// algod and indexer clients, and does not change what they return.
func WithResponseInfo(ctx context.Context, info *ResponseInfo) context.Context {
	return context.WithValue(ctx, responseInfoKey{}, info)
}

// ResponseInfoFrom returns the ResponseInfo of a context made by
// WithResponseInfo, or nil.
func ResponseInfoFrom(ctx context.Context) *ResponseInfo {
	info, _ := ctx.Value(responseInfoKey{}).(*ResponseInfo)
	return info
}

// resetResponseInfo clears the context's ResponseInfo, if any, before a
// request, and returns it.
func resetResponseInfo(ctx context.Context) *ResponseInfo {
	info := ResponseInfoFrom(ctx)
	if info != nil {
		*info = ResponseInfo{}
	}
	return info
}

// fill copies the status and headers of resp. The headers are cloned: the
// caller closes the response.
func (info *ResponseInfo) fill(resp *http.Response) {
	if info == nil {
		return
	}
	info.StatusCode = resp.StatusCode
	info.Header = resp.Header.Clone()
}

// HTTPError is an error response from a time-travel method of the SDK, with
// its status code and headers. Its message is that of the error it wraps,
// which is what the upstream methods return ("HTTP 400: ..."). Upstream
// methods never return it; use WithResponseInfo to read their headers.
type HTTPError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Header holds the response headers.
	Header http.Header
	// Cause is the error the request returned.
	Cause error
}

func (e *HTTPError) Error() string { return e.Cause.Error() }

// Unwrap returns the error the request returned.
func (e *HTTPError) Unwrap() error { return e.Cause }

// WrapHTTPError wraps err in an HTTPError if info holds an error status. It
// returns err unchanged when err is nil, when there was no response, and when
// the response succeeded (err is then a decoding error).
func WrapHTTPError(err error, info *ResponseInfo) error {
	if err == nil || info == nil || info.StatusCode < 300 {
		return err
	}
	return &HTTPError{StatusCode: info.StatusCode, Header: info.Header, Cause: err}
}
