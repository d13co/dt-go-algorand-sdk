package common

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/internal/dtnode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// respErr is an error response as RetryTransport passes it to decisions.
func respErr(status int, message string, header map[string]string) error {
	h := http.Header{}
	for k, v := range header {
		h.Set(k, v)
	}
	body := ""
	if message != "" {
		body = `{"message":"` + message + `"}`
	}
	return &ResponseError{StatusCode: status, Header: h, Cause: extractError(status, []byte(body))}
}

func busyErr() error { return respErr(503, "the node is busy", nil) }

func rc(tries int, method, path string) RetryContext {
	return RetryContext{Context: context.Background(), Method: method, Path: path, Tries: tries}
}

func get(tries int) RetryContext { return rc(tries, "GET", "/v2/status") }

func dialErr(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: errno}}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func decide(err error, c RetryContext, p *RetryPolicy) RetryDecision { return p.Decision(err, c) }

func TestRetryDecision(t *testing.T) {
	t.Run("never retries a request whose context is done", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := get(1)
		c.Context = ctx
		assert.Equal(t, RetryDecision{Reason: RetryAborted}, decide(busyErr(), c, nil))
	})

	t.Run("waits at least 1 s for a busy 503 without Retry-After", func(t *testing.T) {
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryNodeBusy, Delay: time.Second}, decide(busyErr(), get(1), nil))
	})

	t.Run("treats a 429 like a busy node", func(t *testing.T) {
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryRateLimited, Delay: time.Second}, decide(respErr(429, "", nil), get(1), nil))
		withHeader := respErr(429, "", map[string]string{"Retry-After": "1"})
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryRateLimited, Delay: time.Second, RetryAfter: time.Second, HasRetryAfter: true}, decide(withHeader, get(1), nil))
	})

	t.Run("waits the longer of the backoff and Retry-After", func(t *testing.T) {
		ra := func(s string) error { return respErr(503, "the node is busy", map[string]string{"Retry-After": s}) }
		assert.Equal(t, 3*time.Second, decide(ra("3"), get(1), nil).Delay)
		d := decide(ra("1"), get(3), nil)
		assert.True(t, d.Retry)
		assert.Equal(t, 4*time.Second, d.Delay)
	})

	t.Run("honours Retry-After up to 30 s and gives up above it", func(t *testing.T) {
		ra := func(s string) error { return respErr(429, "", map[string]string{"Retry-After": s}) }
		at30 := decide(ra("30"), get(1), nil)
		assert.True(t, at30.Retry)
		assert.Equal(t, 30*time.Second, at30.Delay)
		assert.Equal(t, RetryDecision{Reason: RetryAfterTooLong, RetryAfter: 31 * time.Second, HasRetryAfter: true}, decide(ra("31"), get(1), nil))
		longer := decide(ra("31"), get(1), &RetryPolicy{MaxRetryAfter: time.Minute})
		assert.True(t, longer.Retry)
		assert.Equal(t, 31*time.Second, longer.Delay)
	})

	t.Run("reads Retry-After only on 429 and 503", func(t *testing.T) {
		err := respErr(500, "", map[string]string{"Retry-After": "120"})
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryServerError}, decide(err, get(1), nil))
	})

	t.Run("backs off 0, 2, 4, 8 s, capped at 10 s", func(t *testing.T) {
		var delays []time.Duration
		for tries := 1; tries <= 6; tries++ {
			d := decide(respErr(502, "", nil), get(tries), &RetryPolicy{MaxTries: 10})
			require.True(t, d.Retry)
			delays = append(delays, d.Delay)
		}
		s := time.Second
		assert.Equal(t, []time.Duration{0, 2 * s, 4 * s, 8 * s, 10 * s, 10 * s}, delays)
		assert.Equal(t, time.Duration(1<<33)*s, DefaultBackoff(34))
		assert.Equal(t, time.Duration(math.MaxInt64), DefaultBackoff(35), "no overflow")
		assert.Equal(t, time.Duration(math.MaxInt64), DefaultBackoff(1000))
	})

	t.Run("gives up after MaxTries", func(t *testing.T) {
		assert.Equal(t, RetryDecision{Reason: RetryMaxTries}, decide(busyErr(), get(5), nil))
		assert.Equal(t, RetryMaxTries, decide(busyErr(), get(1), &RetryPolicy{MaxTries: 1}).Reason)
		assert.Equal(t, RetryMaxTries, decide(busyErr(), get(1), &RetryPolicy{MaxTries: -1}).Reason)
	})

	t.Run("never retries history the node cannot serve, on any try", func(t *testing.T) {
		for _, message := range []string{
			"the box index is incomplete",
			"the asset holders index is not complete",
			"the time travel history is not served",
		} {
			for _, tries := range []int{1, 5} {
				assert.Equal(t, RetryDecision{Reason: RetryHistoryUnavailable}, decide(respErr(503, message, nil), get(tries), nil), message)
			}
		}
	})

	t.Run("never retries a 504 from a request under the time limit", func(t *testing.T) {
		for _, req := range [][2]string{
			{"POST", "/v2/query"},
			{"POST", "/v2/transactions/simulate"},
			{"GET", "/v2/deltas/123"},
			{"GET", "/v2/applications/1/boxes/versions"},
			{"GET", "/v2/assets/31566704/holders"},
			{"POST", "/algod/v2/query"},
			{"GET", "/v2/assets/31566704/holders?limit=10"},
		} {
			assert.True(t, IsTimeLimitedRequest(req[0], req[1]), req[1])
			assert.Equal(t, RetryTimeLimit, decide(respErr(504, "", nil), rc(1, req[0], req[1]), nil).Reason, req[1])
		}
		assert.Equal(t, RetryServerError, decide(respErr(504, "", nil), get(1), nil).Reason)
		assert.Equal(t, RetryServerError, decide(respErr(503, "", nil), rc(1, "POST", "/v2/query"), nil).Reason)
		assert.False(t, IsTimeLimitedRequest("GET", "/v2/query"))
		assert.False(t, IsTimeLimitedRequest("GET", "/v2/deltas/txn/group/abc"))
	})

	t.Run("does not retry other statuses, 413 included", func(t *testing.T) {
		for _, status := range []int{400, 401, 404, 413} {
			assert.Equal(t, RetryNotRetryable, decide(respErr(status, "", nil), get(1), nil).Reason, status)
		}
	})

	t.Run("reads the errors upstream methods return", func(t *testing.T) {
		assert.Equal(t, RetryNodeBusy, decide(extractError(503, []byte(`{"message":"the node is busy"}`)), get(1), nil).Reason)
		assert.Equal(t, RetryServerError, decide(extractError(500, nil), get(1), nil).Reason)
		assert.Equal(t, RetryNotRetryable, decide(extractError(404, nil), get(1), nil).Reason)
		assert.Equal(t, RetryRateLimited, decide(extractError(429, nil), get(1), nil).Reason)
	})

	t.Run("retries network errors, refused connections after at least 1 s", func(t *testing.T) {
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryNetwork, Delay: time.Second}, decide(dialErr(syscall.ECONNREFUSED), get(1), nil))
		reset := &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryNetwork}, decide(reset, get(1), nil))
		assert.Equal(t, RetryNetwork, decide(io.ErrUnexpectedEOF, get(1), nil).Reason)
		assert.Equal(t, RetryNetwork, decide(timeoutErr{}, get(1), nil).Reason)
		assert.Equal(t, RetryNetwork, decide(&net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, get(1), nil).Reason)
	})

	t.Run("does not retry other errors", func(t *testing.T) {
		for _, err := range []error{
			errors.New("boom"),
			x509.UnknownAuthorityError{},
			context.Canceled,
			&net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")},
			nil,
		} {
			assert.Equal(t, RetryNotRetryable, decide(err, get(1), nil).Reason, "%v", err)
		}
	})

	t.Run("retries a transaction submit only when the node surely did not take it", func(t *testing.T) {
		reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
		for _, path := range []string{"/v2/transactions", "/v2/transactions/async", "/v2/transactions/"} {
			assert.True(t, IsNonIdempotentRequest("POST", path))
			at := func(err error) RetryReason { return decide(err, rc(1, "POST", path), nil).Reason }
			assert.Equal(t, RetryRateLimited, at(respErr(429, "", nil)))
			assert.Equal(t, RetryNodeBusy, at(busyErr()))
			assert.Equal(t, RetryNetwork, at(dialErr(syscall.ECONNREFUSED)))
			assert.Equal(t, RetryNetwork, at(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", IsNotFound: true}}))
			assert.Equal(t, RetryNotIdempotent, at(reset))
			assert.Equal(t, RetryNotIdempotent, at(io.EOF))
			assert.Equal(t, RetryNotIdempotent, at(respErr(502, "", nil)))
			assert.Equal(t, RetryNotIdempotent, at(respErr(504, "", nil)))
		}
		assert.False(t, IsNonIdempotentRequest("POST", "/v2/transactions/simulate"))
		assert.False(t, IsNonIdempotentRequest("GET", "/v2/transactions/params"))
		assert.False(t, IsNonIdempotentRequest("POST", "/v2/accounts/X/transactions"))
	})

	t.Run("retries a submit whose connection was never made", func(t *testing.T) {
		for _, err := range []error{
			dialErr(syscall.ENETUNREACH),
			dialErr(syscall.EHOSTUNREACH),
			&net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}},
		} {
			d := decide(err, rc(1, "POST", "/v2/transactions"), nil)
			assert.Equal(t, RetryNetwork, d.Reason, "%v", err)
			assert.Zero(t, d.Delay, "only a refused connection waits")
		}
	})

	t.Run("reads Retry-After as an HTTP date, and treats 0 as none", func(t *testing.T) {
		at := func(v string) RetryDecision {
			return decide(respErr(429, "", map[string]string{"Retry-After": v}), get(1), nil)
		}
		d := at(time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat))
		assert.True(t, d.Retry)
		assert.True(t, d.Delay >= time.Second && d.Delay <= 4*time.Second, d.Delay)
		past := at(time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat))
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryRateLimited, Delay: time.Second, HasRetryAfter: true}, past)
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryRateLimited, Delay: time.Second, HasRetryAfter: true}, at("0"))
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryRateLimited, Delay: time.Second}, at("soon"))
	})

	t.Run("treats a negative backoff as 0, keeping the other floors", func(t *testing.T) {
		ra := respErr(429, "", map[string]string{"Retry-After": "5"})
		negative := func(int) time.Duration { return -50 * time.Millisecond }
		assert.Equal(t, 5*time.Second, decide(ra, get(2), &RetryPolicy{Backoff: negative}).Delay)
		d := decide(respErr(502, "", nil), get(2), &RetryPolicy{Backoff: negative})
		assert.True(t, d.Retry)
		assert.Zero(t, d.Delay)
	})

	t.Run("lets Decide override a decision", func(t *testing.T) {
		var seen []any
		d := decide(respErr(404, "", nil), get(2), &RetryPolicy{
			Decide: func(_ error, c RetryContext, def RetryDecision) RetryDecision {
				seen = append(seen, def.Reason, c.Tries)
				return RetryDecision{Retry: true, Reason: RetryServerError, Delay: 5}
			},
		})
		assert.Equal(t, []any{RetryNotRetryable, 2}, seen)
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryServerError, Delay: 5}, d)
	})
}

func TestRetryPolicyDefaults(t *testing.T) {
	def := DefaultRetryPolicy()
	assert.Equal(t, 5, def.MaxTries)
	assert.Equal(t, 10*time.Second, def.MaxBackoff)
	assert.Equal(t, 30*time.Second, def.MaxRetryAfter)
	assert.Equal(t, time.Second, def.BusyMinDelay)
	assert.Equal(t, []int{408, 429, 500, 502, 503, 504}, def.RetryStatuses)

	p := (&RetryPolicy{MaxTries: 3}).resolved()
	assert.Equal(t, 3, p.MaxTries)
	assert.Equal(t, def.MaxBackoff, p.MaxBackoff)
	assert.Equal(t, 5, (&RetryPolicy{}).resolved().MaxTries)
	assert.Equal(t, 1, (&RetryPolicy{MaxTries: -3}).resolved().MaxTries)
	assert.Zero(t, (&RetryPolicy{BusyMinDelay: -1}).resolved().BusyMinDelay)
	assert.Equal(t, []int{503}, (&RetryPolicy{RetryStatuses: []int{503}}).resolved().RetryStatuses)
	assert.Equal(t, RetryNotRetryable, decide(busyErr(), get(1), &RetryPolicy{RetryStatuses: []int{}}).Reason)
	// The defaults are a fresh copy each time
	def.RetryStatuses[0] = 999
	assert.Equal(t, 408, DefaultRetryPolicy().RetryStatuses[0])
}

// step is one scripted answer: a status (with headers and body) or an error.
type step struct {
	status int
	header map[string]string
	body   string
	err    error
}

// scripted is a RoundTripper that answers from a list, one per request, then
// 200 "{}".
type scripted struct {
	mu     sync.Mutex
	steps  []step
	calls  []string
	bodies []string
}

func (s *scripted) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req.Method+" "+req.URL.Path)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		s.bodies = append(s.bodies, string(b))
	}
	next := step{status: 200, body: "{}"}
	if len(s.steps) > 0 {
		next, s.steps = s.steps[0], s.steps[1:]
	}
	if next.err != nil {
		return nil, next.err
	}
	h := http.Header{}
	for k, v := range next.header {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: next.status, Header: h, Body: io.NopCloser(strings.NewReader(next.body)), Request: req}, nil
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

var busyStep = step{status: 503, body: `{"message":"the node is busy"}`}

// recordingSleep records the waits and doesn't wait.
func recordingSleep(waits *[]time.Duration) func(context.Context, time.Duration) error {
	var mu sync.Mutex
	return func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		*waits = append(*waits, d)
		return ctx.Err()
	}
}

func send(ctx context.Context, t *testing.T, rt http.RoundTripper, method, path, body string) (*http.Response, error) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path, r)
	require.NoError(t, err)
	return rt.RoundTrip(req)
}

func TestRetryTransport(t *testing.T) {
	ctx := context.Background()

	t.Run("retries until the request succeeds, telling OnWait each time", func(t *testing.T) {
		base := &scripted{steps: []step{{status: 429, header: map[string]string{"Retry-After": "1"}}, busyStep, {status: 502}}}
		var waits []time.Duration
		var seen []RetryWait
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), OnWait: func(w RetryWait) { seen = append(seen, w) }}}
		resp, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 4, base.count())
		s := time.Second
		assert.Equal(t, []time.Duration{s, 2 * s, 4 * s}, waits)
		require.Len(t, seen, 3)
		type row struct {
			Reason            RetryReason
			Retry, Max, State int
			Delay             time.Duration
		}
		var rows []row
		for _, w := range seen {
			rows = append(rows, row{w.Reason, w.Retry, w.MaxRetries, w.Status, w.Delay})
		}
		assert.Equal(t, []row{
			{RetryRateLimited, 1, 4, 429, s},
			{RetryNodeBusy, 2, 4, 503, 2 * s},
			{RetryServerError, 3, 4, 502, 4 * s},
		}, rows)
		assert.True(t, seen[0].HasRetryAfter)
		assert.Equal(t, s, seen[0].RetryAfter)
		assert.False(t, seen[1].HasRetryAfter)
		assert.Equal(t, "GET", seen[0].Method)
		assert.Equal(t, "/v2/status", seen[0].Path)
		var respErr *ResponseError
		require.ErrorAs(t, seen[1].Err, &respErr)
		assert.Equal(t, 503, respErr.StatusCode)
		assert.Equal(t, "The node is rate limiting requests: retrying in 1 s (retry 1 of 4)", FormatRetryWait(seen[0]))
		now := seen[2]
		now.Delay = 0
		assert.Equal(t, "The node answered 502: retrying now (retry 3 of 4)", FormatRetryWait(now))
	})

	t.Run("returns the last response itself when it gives up, body intact", func(t *testing.T) {
		big := `{"message":"the node is busy","pad":"` + strings.Repeat("x", 100<<10) + `"}`
		base := &scripted{steps: []step{busyStep, {status: 503, body: big, header: map[string]string{"X-Last": "1"}}}}
		var waits []time.Duration
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2}}
		resp, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 503, resp.StatusCode)
		assert.Equal(t, "1", resp.Header.Get("X-Last"))
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, big, string(body))
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, 2, base.count())
	})

	t.Run("returns the last network error itself", func(t *testing.T) {
		refused := dialErr(syscall.ECONNREFUSED)
		var waits []time.Duration
		base := &scripted{steps: []step{{err: refused}, {err: refused}}}
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2}}
		_, err := send(ctx, t, rt, "GET", "/v2/status", "")
		assert.Same(t, refused, err)
		assert.Equal(t, []time.Duration{time.Second}, waits)
	})

	t.Run("does not retry a status it must not", func(t *testing.T) {
		base := &scripted{steps: []step{{status: 404, body: `{"message":"no"}`}}}
		resp, err := send(ctx, t, &RetryTransport{Base: base}, "GET", "/v2/accounts/x", "")
		require.NoError(t, err)
		assert.Equal(t, 404, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, `{"message":"no"}`, string(body))
		assert.Equal(t, 1, base.count())
	})

	t.Run("replays the request body", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, {status: 429}}}
		var waits []time.Duration
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits)}}
		_, err := send(ctx, t, rt, "POST", "/v2/query", "payload")
		require.NoError(t, err)
		assert.Equal(t, []string{"payload", "payload", "payload"}, base.bodies)
	})

	t.Run("does not retry a body it cannot replay", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep}}
		req, err := http.NewRequest("POST", "http://node/v2/query", io.NopCloser(bytes.NewReader([]byte("x"))))
		require.NoError(t, err)
		require.Nil(t, req.GetBody)
		resp, err := (&RetryTransport{Base: base}).RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, 503, resp.StatusCode)
		assert.Equal(t, 1, base.count())
	})

	t.Run("ignores an OnWait that panics", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, busyStep}}
		var waits []time.Duration
		calls := 0
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), OnWait: func(RetryWait) {
			calls++
			panic("broken listener")
		}}}
		resp, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 2, calls)
	})

	t.Run("lists the waits in progress, per request", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, {status: 429}}}
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: func(context.Context, time.Duration) error {
			started <- struct{}{}
			<-release
			return nil
		}}}
		var wg sync.WaitGroup
		for _, req := range [][2]string{{"GET", "/v2/status"}, {"POST", "/v2/query"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := send(ctx, t, rt, req[0], req[1], "")
				assert.NoError(t, err)
			}()
		}
		<-started
		<-started
		waits := rt.Waits()
		assert.Len(t, waits, 2)
		latest, ok := rt.Waiting()
		require.True(t, ok)
		for _, w := range waits {
			assert.False(t, w.Until.After(latest.Until))
		}
		close(release)
		wg.Wait()
		assert.Empty(t, rt.Waits())
		_, ok = rt.Waiting()
		assert.False(t, ok)
	})

	t.Run("stops waiting when the context is cancelled", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, busyStep}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{BusyMinDelay: 5 * time.Second}}
		time.AfterFunc(10*time.Millisecond, cancel)
		start := time.Now()
		_, err := send(ctx, t, rt, "GET", "/v2/status", "")
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 2*time.Second)
		assert.Equal(t, 1, base.count())
		assert.Empty(t, rt.Waits())
	})

	t.Run("sends nothing for a request already cancelled", func(t *testing.T) {
		base := &scripted{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := send(ctx, t, &RetryTransport{Base: base}, "GET", "/v2/status", "")
		assert.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, base.count())
	})

	t.Run("stops after MaxTries even when Decide says retry", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, busyStep, busyStep, busyStep}}
		var waits []time.Duration
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 3,
			Decide: func(error, RetryContext, RetryDecision) RetryDecision {
				return RetryDecision{Retry: true, Reason: RetryServerError, Delay: -time.Second}
			}}}
		resp, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 503, resp.StatusCode)
		assert.Equal(t, 3, base.count())
		assert.Empty(t, waits, "a negative delay retries at once")
	})

	t.Run("shows the wait in Waiting from inside OnWait", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep}}
		var waits []time.Duration
		var inside RetryWait
		var found bool
		rt := &RetryTransport{Base: base}
		rt.Policy = &RetryPolicy{Sleep: recordingSleep(&waits), OnWait: func(RetryWait) { inside, found = rt.Waiting() }}
		_, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, RetryNodeBusy, inside.Reason)
		_, found = rt.Waiting()
		assert.False(t, found)
	})

	t.Run("passes the request to the decisions", func(t *testing.T) {
		base := &scripted{steps: []step{{status: 504}}}
		var seen []RetryContext
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Decide: func(_ error, c RetryContext, def RetryDecision) RetryDecision {
			seen = append(seen, c)
			return def
		}}}
		resp, err := send(ctx, t, rt, "POST", "/v2/query", "q")
		require.NoError(t, err)
		assert.Equal(t, 504, resp.StatusCode)
		require.Len(t, seen, 1)
		assert.Equal(t, RetryContext{Context: seen[0].Context, Method: "POST", Path: "/v2/query", Tries: 1}, seen[0])
	})

	t.Run("retries a connection to a closed port as a network error", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())
		var seen []RetryWait
		var waits []time.Duration
		rt := &RetryTransport{Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2, OnWait: func(w RetryWait) { seen = append(seen, w) }}}
		req, _ := http.NewRequest("GET", "http://"+addr+"/v2/status", nil)
		_, err = rt.RoundTrip(req)
		require.Error(t, err)
		require.Len(t, seen, 1)
		assert.Equal(t, RetryNetwork, seen[0].Reason)
		assert.Equal(t, time.Second, seen[0].Delay, "a refused connection waits BusyMinDelay")
		assert.Zero(t, seen[0].Status)
	})

	t.Run("works under the client, which reads the error after the retries", func(t *testing.T) {
		var calls int
		var mu sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			b, _ := io.ReadAll(r.Body)
			assert.Equal(t, "raw", string(b))
			switch n {
			case 1:
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(429)
			case 2:
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"message":"the node is busy"}`))
			default:
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"message":"the node is busy"}`))
			}
		}))
		defer server.Close()
		var waits []time.Duration
		rt := &RetryTransport{Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 3}}
		c, err := MakeClientWithTransport(server.URL, "X-Algo-API-Token", "", nil, rt)
		require.NoError(t, err)
		var info ResponseInfo
		var out map[string]any
		err = c.Post(WithResponseInfo(ctx, &info), &out, "/v2/query", nil, nil, []byte("raw"))
		var httpErr HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, 503, httpErr.StatusCode)
		assert.Contains(t, httpErr.Message, "the node is busy")
		assert.Equal(t, 503, info.StatusCode)
		assert.Equal(t, "1", info.Header.Get("Retry-After"))
		assert.Equal(t, 3, calls)
		assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, waits)
	})
}

func TestFormatRetryWait(t *testing.T) {
	base := RetryWait{Delay: 2500 * time.Millisecond, Retry: 1, MaxRetries: 4, Method: "GET", Path: "/v2/status"}
	w := base
	w.Reason, w.Status = RetryNodeBusy, 503
	assert.Equal(t, "The node is busy: retrying in 3 s (retry 1 of 4)", FormatRetryWait(w))
	w = base
	w.Reason = RetryServerError
	assert.Equal(t, "The node answered an error: retrying in 3 s (retry 1 of 4)", FormatRetryWait(w))
	w = base
	w.Reason = RetryNetwork
	assert.Equal(t, "Could not reach the node: retrying in 3 s (retry 1 of 4)", FormatRetryWait(w))
	w.Delay = time.Millisecond
	assert.Equal(t, "Could not reach the node: retrying in 1 s (retry 1 of 4)", FormatRetryWait(w))
}

func TestRetryReviewFixes(t *testing.T) {
	ctx := context.Background()

	t.Run("retries real timeouts, and a dial timeout as never sent", func(t *testing.T) {
		// A dial that can't finish in 1 ms: a timeout, or no route in a sandbox
		dialer := &net.Dialer{Timeout: time.Millisecond}
		base := &http.Transport{DialContext: dialer.DialContext}
		for _, path := range []string{"/v2/status", "/v2/transactions"} {
			var seen []RetryWait
			var waits []time.Duration
			rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2, OnWait: func(w RetryWait) { seen = append(seen, w) }}}
			method := "GET"
			if path == "/v2/transactions" {
				method = "POST"
			}
			req, err := http.NewRequest(method, "http://10.255.255.1"+path, strings.NewReader("x"))
			require.NoError(t, err)
			_, err = rt.RoundTrip(req)
			require.Error(t, err)
			require.Len(t, seen, 1, "%v", err)
			assert.Equal(t, RetryNetwork, seen[0].Reason, "%v", err)
		}
	})

	t.Run("retries a response header timeout, but not for a submit", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer server.Close()
		defer close(release)
		base := &http.Transport{ResponseHeaderTimeout: 20 * time.Millisecond}
		defer base.CloseIdleConnections()
		at := func(method, path string) []RetryReason {
			var reasons []RetryReason
			var waits []time.Duration
			rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2,
				Decide: func(_ error, _ RetryContext, d RetryDecision) RetryDecision {
					reasons = append(reasons, d.Reason)
					return d
				}}}
			req, err := http.NewRequest(method, server.URL+path, strings.NewReader("x"))
			require.NoError(t, err)
			_, err = rt.RoundTrip(req)
			require.Error(t, err)
			return reasons
		}
		assert.Equal(t, []RetryReason{RetryNetwork, RetryMaxTries}, at("GET", "/v2/status"))
		assert.Equal(t, []RetryReason{RetryNotIdempotent}, at("POST", "/v2/transactions"))
	})

	t.Run("keeps negative durations as none under the transport", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, {status: 502}, {status: 502}}}
		var waits []time.Duration
		var delays []time.Duration
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), BusyMinDelay: -1, MaxBackoff: -1,
			OnWait: func(w RetryWait) { delays = append(delays, w.Delay) }}}
		_, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, []time.Duration{0, 0, 0}, delays)
		assert.Empty(t, waits)

		base = &scripted{steps: []step{{status: 429, header: map[string]string{"Retry-After": "1"}}}}
		rt = &RetryTransport{Base: base, Policy: &RetryPolicy{MaxRetryAfter: -1}}
		resp, err := send(ctx, t, rt, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 429, resp.StatusCode)
		assert.Equal(t, 1, base.count(), "any Retry-After is too long")
	})

	t.Run("classifies a url.Error by its cause", func(t *testing.T) {
		scheme := &url.Error{Op: "Get", URL: "ftp://x", Err: errors.New("unsupported protocol scheme")}
		assert.Equal(t, RetryNotRetryable, decide(scheme, get(1), nil).Reason)
		reset := &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}}
		assert.Equal(t, RetryNetwork, decide(reset, get(1), nil).Reason)
		assert.Equal(t, RetryNotRetryable, decide(context.DeadlineExceeded, get(1), nil).Reason)
		// Go's network timeouts can match context.DeadlineExceeded
		dialTimeout := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
		assert.Equal(t, RetryDecision{Retry: true, Reason: RetryNetwork}, decide(dialTimeout, rc(1, "POST", "/v2/transactions"), nil))
		readTimeout := &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}
		assert.Equal(t, RetryNetwork, decide(readTimeout, get(1), nil).Reason)
		assert.Equal(t, RetryNotRetryable, decide(fmt.Errorf("x: %w", context.Canceled), get(1), nil).Reason)
		// A wrapped deadline is some per-try deadline, not the request's own
		assert.Equal(t, RetryNetwork, decide(fmt.Errorf("x: %w", context.DeadlineExceeded), get(1), nil).Reason)
	})

	t.Run("leaves the retries to an inner RetryTransport", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep, busyStep, busyStep}}
		var waits []time.Duration
		inner := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: recordingSleep(&waits), MaxTries: 2}}
		outer := &RetryTransport{Base: inner, Policy: &RetryPolicy{Sleep: recordingSleep(&waits)}}
		resp, err := send(ctx, t, outer, "GET", "/v2/status", "")
		require.NoError(t, err)
		assert.Equal(t, 503, resp.StatusCode)
		assert.Equal(t, 2, base.count())
	})

	t.Run("returns the response when GetBody fails", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep}}
		req, err := http.NewRequest("POST", "http://node/v2/query", strings.NewReader("x"))
		require.NoError(t, err)
		req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("gone") }
		resp, err := (&RetryTransport{Base: base}).RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, 503, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "the node is busy")
		assert.Equal(t, 1, base.count())
	})

	t.Run("cleans up after a Sleep that panics", func(t *testing.T) {
		base := &scripted{steps: []step{busyStep}}
		closed := false
		req, err := http.NewRequest("POST", "http://node/v2/query", strings.NewReader("x"))
		require.NoError(t, err)
		req.GetBody = func() (io.ReadCloser, error) {
			return closeFunc{strings.NewReader("x"), func() { closed = true }}, nil
		}
		rt := &RetryTransport{Base: base, Policy: &RetryPolicy{Sleep: func(context.Context, time.Duration) error { panic("broken sleep") }}}
		assert.Panics(t, func() { _, _ = rt.RoundTrip(req) })
		assert.Empty(t, rt.Waits())
		assert.True(t, closed, "the next try's body is closed")
	})

	t.Run("reads Retry-After as dt-algosdk does", func(t *testing.T) {
		now := time.Now()
		ra := func(v string) time.Duration {
			d, ok := dtnode.ParseRetryAfter(v, now)
			require.True(t, ok, v)
			return d
		}
		// http.TimeFormat has whole seconds: pick dates 0.4 s and 1.4 s ahead
		base := now.Truncate(time.Second).Add(time.Second)
		d0 := base.Sub(now)
		assert.Equal(t, time.Second, ra(base.UTC().Format(http.TimeFormat)), "rounded up from %v", d0)
		assert.Equal(t, 2*time.Second, ra(base.Add(time.Second).UTC().Format(http.TimeFormat)))
		assert.Equal(t, time.Duration(math.MaxInt64), ra("99999999999999999999"))
		assert.Equal(t, time.Duration(math.MaxInt64), ra("18446744073709551615"))
		huge := respErr(429, "", map[string]string{"Retry-After": "99999999999"})
		assert.Equal(t, RetryAfterTooLong, decide(huge, get(1), nil).Reason)
		assert.Equal(t, time.Duration(math.MaxInt64), ra("Fri, 31 Dec 9999 23:59:59 GMT"))
		farDate := respErr(429, "", map[string]string{"Retry-After": "Fri, 31 Dec 9999 23:59:59 GMT"})
		assert.Equal(t, RetryAfterTooLong, decide(farDate, get(1), nil).Reason)
	})

	t.Run("formats a rate limit", func(t *testing.T) {
		w := RetryWait{Reason: RetryRateLimited, Status: 429, Delay: time.Second, Retry: 2, MaxRetries: 4}
		assert.Equal(t, "The node is rate limiting requests: retrying in 1 s (retry 2 of 4)", FormatRetryWait(w))
	})
}

type closeFunc struct {
	io.Reader
	close func()
}

func (c closeFunc) Close() error {
	c.close()
	return nil
}
