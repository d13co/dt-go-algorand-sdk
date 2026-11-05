package common

// This file is part of dt-go-algorand-sdk: an opt-in retry policy for busy
// nodes, rate limits and network errors, the same as dt-algosdk's
// (DTALGOSDK.md, "Retries"). RetryTransport applies it to any client made
// with MakeClientWithTransport.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/internal/dtnode"
)

// RetryReason says why a failed request is retried, or why it is not.
type RetryReason string

// The reasons to retry a request: RetryDecision.Retry is true.
const (
	// RetryRateLimited is a 429.
	RetryRateLimited RetryReason = "rate-limited"
	// RetryNodeBusy is a busy 503 (see algod.IsNodeBusy).
	RetryNodeBusy RetryReason = "node-busy"
	// RetryServerError is another status in RetryPolicy.RetryStatuses.
	RetryServerError RetryReason = "server-error"
	// RetryNetwork is a network error: no response.
	RetryNetwork RetryReason = "network"
)

// The reasons to give up: RetryDecision.Retry is false.
const (
	// RetryAborted is a request whose context is done.
	RetryAborted RetryReason = "aborted"
	// RetryNotRetryable is a status not in RetryPolicy.RetryStatuses, or an
	// error that is no network error.
	RetryNotRetryable RetryReason = "not-retryable"
	// RetryHistoryUnavailable is a 503 for history the node can't serve now
	// (see algod.IsHistoryUnavailable), which retrying soon won't fix.
	RetryHistoryUnavailable RetryReason = "history-unavailable"
	// RetryTimeLimit is a 504 from a request under the node's time limit
	// (IsTimeLimitedRequest), which would run into it again: split it.
	RetryTimeLimit RetryReason = "time-limit"
	// RetryNotIdempotent is a transaction submit (IsNonIdempotentRequest)
	// that the node may have taken.
	RetryNotIdempotent RetryReason = "not-idempotent"
	// RetryMaxTries is a request that made RetryPolicy.MaxTries tries.
	RetryMaxTries RetryReason = "max-tries"
	// RetryAfterTooLong is a Retry-After above RetryPolicy.MaxRetryAfter.
	RetryAfterTooLong RetryReason = "retry-after-too-long"
)

// RetryContext is a request that failed, after Tries tries (the failed one
// included).
type RetryContext struct {
	// Context is the request's context: a request whose context is done is
	// never retried.
	Context context.Context
	// Method is the HTTP method, such as "GET".
	Method string
	// Path is the URL path, such as "/v2/query".
	Path string
	// Tries counts the tries made, from 1.
	Tries int
}

// RetryDecision says whether to retry a failed request, why, and how long to
// wait first.
type RetryDecision struct {
	Retry  bool
	Reason RetryReason
	// Delay is the wait before the retry.
	Delay time.Duration
	// RetryAfter is the node's Retry-After, if HasRetryAfter.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// RetryWait is a request about to wait before it is retried.
type RetryWait struct {
	// Reason is RetryRateLimited, RetryNodeBusy, RetryServerError or
	// RetryNetwork, unless RetryPolicy.Decide returned another.
	Reason RetryReason
	// Delay is how long it waits; 0 retries at once.
	Delay time.Duration
	// Until is when the retry starts.
	Until time.Time
	// RetryAfter is the node's Retry-After, if HasRetryAfter.
	RetryAfter    time.Duration
	HasRetryAfter bool
	// Retry says which retry this is, from 1.
	Retry int
	// MaxRetries is the most retries the request makes (MaxTries - 1).
	MaxRetries int
	Method     string
	Path       string
	// Status is the HTTP status, or 0 if there was no response.
	Status int
	// Err is the failed try's error: a *ResponseError for an error response.
	Err error
}

// RetryPolicy says how RetryTransport retries failed requests. A field left
// at its zero value takes its default (DefaultRetryPolicy). A negative
// duration means none (0), and a negative MaxTries means 1.
type RetryPolicy struct {
	// MaxTries is the tries in all, the first included; 1 turns retries off.
	// Default 5. Not even Decide retries past it.
	MaxTries int
	// MaxBackoff caps Backoff. Default 10 s.
	MaxBackoff time.Duration
	// MaxRetryAfter is the longest Retry-After honoured. A longer one gives
	// up at once (RetryAfterTooLong), so the caller can wait or go to another
	// node. Default 30 s.
	MaxRetryAfter time.Duration
	// BusyMinDelay is the least wait before retrying a 429 or a busy 503
	// that came with no Retry-After (or Retry-After: 0), or a refused
	// connection. Default 1 s.
	BusyMinDelay time.Duration
	// Backoff is the backoff before retry `retry` (from 1), before MaxBackoff
	// caps it. Default DefaultBackoff: 0, then 2, 4, 8 s.
	Backoff func(retry int) time.Duration
	// RetryStatuses are the HTTP statuses that may be retried. Default 408,
	// 429, 500, 502, 503 and 504. A non-nil empty list retries no status.
	RetryStatuses []int
	// Decide overrides any decision. It gets the error, the request and the
	// policy's own decision, and returns the one to follow, within MaxTries.
	// A panic in it, or in Backoff, reaches the caller, as an exception
	// decide throws does in dt-algosdk.
	Decide func(err error, rc RetryContext, decision RetryDecision) RetryDecision
	// OnWait is called before every retry, those with no wait included, to
	// tell the app that a request is rate limited, waiting for a busy node,
	// or retrying. The wait is already in RetryTransport.Waits, if it waits
	// at all. A panic in it
	// is recovered and ignored. It may be called from several goroutines at
	// once.
	OnWait func(wait RetryWait)
	// Sleep waits d, or returns ctx's error as soon as it is done. Default:
	// a timer and ctx.Done().
	Sleep func(ctx context.Context, d time.Duration) error
}

// DefaultRetryPolicy returns the default policy, with every field set.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxTries:      5,
		MaxBackoff:    10 * time.Second,
		MaxRetryAfter: 30 * time.Second,
		BusyMinDelay:  time.Second,
		Backoff:       DefaultBackoff,
		RetryStatuses: []int{408, 429, 500, 502, 503, 504},
		Sleep:         sleepContext,
	}
}

// DefaultBackoff is the default backoff before retry `retry` (from 1): 0
// before the first, then 2^(retry-1) s: 2, 4, 8 s.
func DefaultBackoff(retry int) time.Duration {
	if retry <= 1 {
		return 0
	}
	if retry > 34 {
		return math.MaxInt64
	}
	return time.Duration(1<<(retry-1)) * time.Second
}

func orDefault(d, def time.Duration) time.Duration {
	switch {
	case d == 0:
		return def
	case d < 0:
		return 0
	}
	return d
}

// resolved returns p with the defaults filled in. A nil p is the default.
func (p *RetryPolicy) resolved() RetryPolicy {
	def := DefaultRetryPolicy()
	if p == nil {
		return def
	}
	r := *p
	switch {
	case r.MaxTries == 0:
		r.MaxTries = def.MaxTries
	case r.MaxTries < 0:
		r.MaxTries = 1
	}
	r.MaxBackoff = orDefault(r.MaxBackoff, def.MaxBackoff)
	r.MaxRetryAfter = orDefault(r.MaxRetryAfter, def.MaxRetryAfter)
	r.BusyMinDelay = orDefault(r.BusyMinDelay, def.BusyMinDelay)
	if r.Backoff == nil {
		r.Backoff = def.Backoff
	}
	if r.RetryStatuses == nil {
		r.RetryStatuses = def.RetryStatuses
	}
	if r.Sleep == nil {
		r.Sleep = def.Sleep
	}
	return r
}

// reTimeLimited matches the time-limited GETs, after any prefix: a listing
// under /v2/ (not algod's own GET /versions) and /v2/deltas/{round}.
var reTimeLimited = regexp.MustCompile(`/v2/.+/(versions|holders)$|/v2/deltas/\d+$`)

func trimPath(path string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return strings.TrimRight(path, "/")
}

// IsTimeLimitedRequest reports whether a request runs under the node's time
// limit, so a 504 means it ran too long and a retry would do the same: POST
// /v2/query, POST /v2/transactions/simulate, GET /v2/deltas/{round}, and GET
// of a /versions or /holders listing (not GET /versions, algod's version).
// Split or narrow it instead. The path
// may have a prefix, such as a proxy's.
func IsTimeLimitedRequest(method, path string) bool {
	p := trimPath(path)
	switch strings.ToUpper(method) {
	case http.MethodPost:
		return strings.HasSuffix(p, "/v2/query") || strings.HasSuffix(p, "/v2/transactions/simulate")
	case http.MethodGet:
		return reTimeLimited.MatchString(p)
	}
	return false
}

// IsNonIdempotentRequest reports whether a request must not be sent twice:
// POST /v2/transactions and POST /v2/transactions/async. If the node took the
// transactions but the answer was lost, a retry gets "transaction already in
// ledger" instead of the transaction ID. They are retried only when the node
// surely did not take them: a 429, a busy 503, or a connection that was
// refused or never made. The path may have a prefix, such as a proxy's.
func IsNonIdempotentRequest(method, path string) bool {
	p := trimPath(path)
	return strings.ToUpper(method) == http.MethodPost &&
		(strings.HasSuffix(p, "/v2/transactions") || strings.HasSuffix(p, "/v2/transactions/async"))
}

// statusOf returns the status and headers (if any) of an error response: a
// *ResponseError, or the HTTPError (or a type that embeds it) the client
// returns.
func statusOf(err error) (int, http.Header, bool) {
	var respErr *ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode, respErr.Header, true
	}
	var (
		plain    HTTPError
		bad      BadRequest
		token    InvalidToken
		notFound NotFound
		internal InternalError
	)
	switch {
	case errors.As(err, &plain):
		return plain.StatusCode, nil, true
	case errors.As(err, &bad):
		return bad.StatusCode, nil, true
	case errors.As(err, &token):
		return token.StatusCode, nil, true
	case errors.As(err, &notFound):
		return notFound.StatusCode, nil, true
	case errors.As(err, &internal):
		return internal.StatusCode, nil, true
	}
	return 0, nil, false
}

// wsaeconnrefused is Windows' "connection refused", which syscall.ECONNREFUSED
// doesn't match there. No other system uses this number.
const wsaeconnrefused = syscall.Errno(10061)

// classifyNetwork reports whether err is a network error that may be retried,
// whether the request surely never reached the node (the connection was
// never made), and whether the connection was refused. Timeouts and dropped
// connections are network errors; certificate and TLS errors, a bad address,
// a bare context error, and errors of the client itself are not. A *url.Error, which http.Client returns, is
// classified by its cause.
func classifyNetwork(err error) (network, notSent, refused bool) {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if err == nil {
		return false, false, false
	}
	var (
		unknownAuthority x509.UnknownAuthorityError
		invalidCert      x509.CertificateInvalidError
		hostname         x509.HostnameError
		verify           *tls.CertificateVerificationError
		recordHeader     tls.RecordHeaderError
		alert            tls.AlertError
	)
	if errors.As(err, &unknownAuthority) || errors.As(err, &invalidCert) || errors.As(err, &hostname) ||
		errors.As(err, &verify) || errors.As(err, &recordHeader) || errors.As(err, &alert) {
		return false, false, false
	}
	refused = errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeconnrefused)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true, true, refused
	}
	// Checked before the context errors: Go's network timeouts match
	// context.DeadlineExceeded too.
	// A bad address or network fails the same way every time
	var (
		addrErr    *net.AddrError
		parseErr   *net.ParseError
		unknownNet net.UnknownNetworkError
		invalid    net.InvalidAddrError
	)
	if errors.As(err, &addrErr) || errors.As(err, &parseErr) || errors.As(err, &unknownNet) || errors.As(err, &invalid) {
		return false, false, false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		switch opErr.Op {
		case "remote error":
			// A TLS alert from the server
			return false, false, false
		case "dial", "proxyconnect":
			return true, true, refused
		}
		return true, false, refused
	}
	// A request's own context is decided before (RetryAborted): a bare
	// context error is some other cancellation. A wrapped DeadlineExceeded,
	// such as http's header timeout, is a per-try deadline: a network error.
	if err == context.DeadlineExceeded || errors.Is(err, context.Canceled) {
		return false, false, false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		isDroppedConnection(err) {
		return true, false, refused
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true, false, refused
	}
	return false, false, false
}

// isDroppedConnection reports whether err is net/http's own error for a
// connection the server dropped, which it doesn't export: a request written
// onto a keep-alive connection the server had just closed, or an HTTP/2
// GOAWAY or stream reset. The request may have reached the node.
func isDroppedConnection(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "http: server closed idle connection") ||
		strings.Contains(msg, "http2: server sent GOAWAY") ||
		strings.Contains(msg, "stream error: stream ID")
}

func giveUp(reason RetryReason, retryAfter time.Duration, has bool) RetryDecision {
	return RetryDecision{Reason: reason, RetryAfter: retryAfter, HasRetryAfter: has}
}

// Decision says whether to retry a request that failed with err, and after
// how long, by the policy in DTALGOSDK.md ("Retries"). err is what the try
// returned: a *ResponseError or an HTTPError for an error response, or a
// network error. In order:
//   - a request whose context is done is not retried;
//   - a 503 for history the node can't serve, a 504 from a request under the
//     node's time limit (IsTimeLimitedRequest), a status not in
//     RetryStatuses and an error that is no network error are not retried;
//   - a transaction submit (IsNonIdempotentRequest) is retried only when the
//     node surely did not take it;
//   - after MaxTries tries, or with a Retry-After above MaxRetryAfter, it
//     gives up;
//   - otherwise it waits the backoff, at least Retry-After (429 and 503),
//     and at least BusyMinDelay for a 429 or a busy 503 without one (or with
//     0), or a refused connection.
//
// Decide can then override the decision. A nil policy is the default.
func (p *RetryPolicy) Decision(err error, rc RetryContext) RetryDecision {
	policy := p.resolved()
	return policy.decide(err, rc)
}

// decide is Decision for a resolved policy.
func (p *RetryPolicy) decide(err error, rc RetryContext) RetryDecision {
	decision := p.defaultDecision(err, rc)
	if p.Decide != nil {
		decision = p.Decide(err, rc, decision)
	}
	return decision
}

func (p *RetryPolicy) defaultDecision(err error, rc RetryContext) RetryDecision {
	if rc.Context != nil && rc.Context.Err() != nil {
		return RetryDecision{Reason: RetryAborted}
	}
	var (
		reason     RetryReason
		minDelay   time.Duration
		retryAfter time.Duration
		hasRA      bool
		notSent    bool
	)
	if status, header, ok := statusOf(err); ok {
		msg := err.Error()
		if status == http.StatusServiceUnavailable && dtnode.History.MatchString(msg) {
			return RetryDecision{Reason: RetryHistoryUnavailable}
		}
		if status == http.StatusGatewayTimeout && IsTimeLimitedRequest(rc.Method, rc.Path) {
			return RetryDecision{Reason: RetryTimeLimit}
		}
		if !slices.Contains(p.RetryStatuses, status) {
			return RetryDecision{Reason: RetryNotRetryable}
		}
		switch {
		case status == http.StatusTooManyRequests:
			reason, notSent = RetryRateLimited, true
		case status == http.StatusServiceUnavailable && dtnode.NodeBusy.MatchString(msg):
			reason, notSent = RetryNodeBusy, true
		default:
			reason = RetryServerError
		}
		if (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && header != nil {
			retryAfter, hasRA = dtnode.ParseRetryAfter(header.Get("Retry-After"), time.Now())
		}
		if notSent && retryAfter == 0 {
			minDelay = p.BusyMinDelay
		}
	} else if network, ns, refused := classifyNetwork(err); network {
		reason, notSent = RetryNetwork, ns
		// A refused connection may be the node's connection limit
		// (RestConnectionsHardLimit): don't hammer it.
		if refused {
			minDelay = p.BusyMinDelay
		}
	} else {
		return RetryDecision{Reason: RetryNotRetryable}
	}
	if !notSent && IsNonIdempotentRequest(rc.Method, rc.Path) {
		return giveUp(RetryNotIdempotent, retryAfter, hasRA)
	}
	if rc.Tries >= p.MaxTries {
		return giveUp(RetryMaxTries, retryAfter, hasRA)
	}
	if hasRA && retryAfter > p.MaxRetryAfter {
		return giveUp(RetryAfterTooLong, retryAfter, hasRA)
	}
	backoff := min(max(0, p.Backoff(rc.Tries)), p.MaxBackoff)
	return RetryDecision{
		Retry:         true,
		Reason:        reason,
		Delay:         max(backoff, minDelay, retryAfter),
		RetryAfter:    retryAfter,
		HasRetryAfter: hasRA,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func notifyWait(onWait func(RetryWait), wait RetryWait) {
	if onWait == nil {
		return
	}
	// A broken listener must not break the request
	defer func() { _ = recover() }()
	onWait(wait)
}

var waitReasons = map[RetryReason]string{
	RetryRateLimited: "The node is rate limiting requests",
	RetryNodeBusy:    "The node is busy",
	RetryNetwork:     "Could not reach the node",
}

// FormatRetryWait makes a message of a wait to show users, such as "The node
// is rate limiting requests: retrying in 3 s (retry 2 of 4)". It is the same
// as dt-algosdk's formatRetryWait.
func FormatRetryWait(wait RetryWait) string {
	what, ok := waitReasons[wait.Reason]
	switch {
	case wait.Reason == RetryServerError:
		status := "an error"
		if wait.Status != 0 {
			status = fmt.Sprint(wait.Status)
		}
		what = "The node answered " + status
	case !ok:
		// A reason Decide made up
		what = "The request failed"
	}
	when := "retrying now"
	if seconds := int64(math.Ceil(max(0, wait.Delay).Seconds())); seconds > 0 {
		when = fmt.Sprintf("retrying in %d s", seconds)
	}
	return fmt.Sprintf("%s: %s (retry %d of %d)", what, when, wait.Retry, wait.MaxRetries)
}

// maxErrorBodyBytes is how much of an error response RetryTransport reads to
// decide on it. The caller still gets the whole body.
const maxErrorBodyBytes = 64 << 10

// RetryTransport is an http.RoundTripper that retries the requests of Base by
// Policy, to pass to MakeClientWithTransport (algod and indexer have one
// too):
//
//	client, err := algod.MakeClientWithTransport(address, token, nil, &common.RetryTransport{
//		Policy: &common.RetryPolicy{
//			OnWait: func(w common.RetryWait) { log.Print(common.FormatRetryWait(w)) },
//		},
//	})
//
// When it gives up, it returns the last try's response or error itself, so
// the client returns the error it would without retries. A request body is
// replayed with Request.GetBody, which the clients set; a request with a body
// and no GetBody is not retried. A RetryTransport whose Base is another one
// leaves the retries to that one. Use it by pointer, and don't copy it after
// first use.
type RetryTransport struct {
	// Base sends the requests. Default http.DefaultTransport.
	Base http.RoundTripper
	// Policy says how to retry. Default DefaultRetryPolicy.
	Policy *RetryPolicy

	mu     sync.Mutex
	active map[*RetryWait]struct{}
}

// Waits returns the requests waiting to be retried now, one each, the one
// that retries soonest first. A retry with no wait is never in it.
func (t *RetryTransport) Waits() []RetryWait {
	t.mu.Lock()
	defer t.mu.Unlock()
	waits := make([]RetryWait, 0, len(t.active))
	for w := range t.active {
		waits = append(waits, *w)
	}
	slices.SortFunc(waits, func(a, b RetryWait) int { return a.Until.Compare(b.Until) })
	return waits
}

// CloseIdleConnections closes the idle connections of Base, if it has any,
// so that http.Client.CloseIdleConnections reaches it.
func (t *RetryTransport) CloseIdleConnections() {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if closer, ok := base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Waiting returns, of the requests waiting now, the one that waits longest.
func (t *RetryTransport) Waiting() (RetryWait, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var latest *RetryWait
	for w := range t.active {
		if latest == nil || w.Until.After(latest.Until) {
			latest = w
		}
	}
	if latest == nil {
		return RetryWait{}, false
	}
	return *latest, true
}

func (t *RetryTransport) track(w *RetryWait, on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !on {
		delete(t.active, w)
		return
	}
	if t.active == nil {
		t.active = make(map[*RetryWait]struct{})
	}
	t.active[w] = struct{}{}
}

// readBody is a response body whose start was read to decide on it.
type readBody struct {
	io.Reader
	io.Closer
}

// RoundTrip sends req, retrying it by the policy.
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if inner, ok := base.(*RetryTransport); ok {
		// Retry once, by the inner policy, not MaxTries × MaxTries times
		return inner.RoundTrip(req)
	}
	ctx := req.Context()
	if err := ctx.Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return base.RoundTrip(req)
	}
	policy := t.Policy.resolved()
	try := req
	for tries := 1; ; tries++ {
		resp, err := base.RoundTrip(try)
		failure := err
		if err == nil {
			if resp.StatusCode < 400 {
				return resp, nil
			}
			if resp.Body == nil {
				resp.Body = http.NoBody
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body = readBody{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
			failure = &ResponseError{
				StatusCode: resp.StatusCode,
				Header:     resp.Header,
				Cause:      extractError(resp.StatusCode, body),
			}
		}
		rc := RetryContext{Context: ctx, Method: req.Method, Path: req.URL.Path, Tries: tries}
		decision := decideOrClose(&policy, failure, rc, resp)
		if !decision.Retry || tries >= policy.MaxTries {
			return resp, err
		}
		next := req.Clone(ctx)
		if req.GetBody != nil {
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return resp, err
			}
			next.Body = body
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		wait := &RetryWait{
			Reason:        decision.Reason,
			Delay:         max(0, decision.Delay),
			RetryAfter:    decision.RetryAfter,
			HasRetryAfter: decision.HasRetryAfter,
			Retry:         tries,
			MaxRetries:    policy.MaxTries - 1,
			Method:        req.Method,
			Path:          req.URL.Path,
			Err:           failure,
		}
		wait.Until = time.Now().Add(wait.Delay)
		if resp != nil {
			wait.Status = resp.StatusCode
		}
		if err := t.wait(ctx, &policy, wait, next.Body); err != nil {
			return nil, err
		}
		try = next
	}
}

// decideOrClose decides on a failed try, closing its response if Decide or
// Backoff panics.
func decideOrClose(policy *RetryPolicy, err error, rc RetryContext, resp *http.Response) RetryDecision {
	done := false
	defer func() {
		if !done && resp != nil {
			_ = resp.Body.Close()
		}
	}()
	decision := policy.decide(err, rc)
	done = true
	return decision
}

// wait tells OnWait and sleeps before a retry. Unless it returns nil, as on a
// panic in Sleep, it closes the next try's body.
func (t *RetryTransport) wait(ctx context.Context, policy *RetryPolicy, wait *RetryWait, body io.Closer) (err error) {
	done := false
	defer func() {
		if !done && body != nil {
			_ = body.Close()
		}
	}()
	if wait.Delay > 0 {
		t.track(wait, true)
		defer t.track(wait, false)
	}
	notifyWait(policy.OnWait, *wait)
	if wait.Delay > 0 {
		err = policy.Sleep(ctx, wait.Delay)
	}
	if err == nil {
		err = ctx.Err()
	}
	done = err == nil
	return err
}
