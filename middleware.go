package ratelimiter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

type contextKey string

const decisionContextKey contextKey = "ratelimiter.decision"

// KeyFunc extracts the rate-limit key for a request.
type KeyFunc func(*http.Request) string

// RejectFunc handles rejected requests.
type RejectFunc func(http.ResponseWriter, *http.Request, Decision)

// MiddlewareOption customizes HTTP middleware behavior.
type MiddlewareOption func(*Middleware)

// Middleware adapts Limiter to net/http.
type Middleware struct {
	limiter  *Limiter
	keyFunc  KeyFunc
	onReject RejectFunc
}

// NewMiddleware creates an HTTP middleware around a limiter.
func NewMiddleware(limiter *Limiter, opts ...MiddlewareOption) *Middleware {
	if limiter == nil {
		panic(ErrNilLimiter)
	}

	m := &Middleware{
		limiter:  limiter,
		keyFunc:  RemoteIPKey,
		onReject: defaultReject,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}

	return m
}

// WithKeyFunc overrides request key extraction.
func WithKeyFunc(fn KeyFunc) MiddlewareOption {
	return func(m *Middleware) {
		if fn != nil {
			m.keyFunc = fn
		}
	}
}

// WithRejectFunc overrides the default rejection response.
func WithRejectFunc(fn RejectFunc) MiddlewareOption {
	return func(m *Middleware) {
		if fn != nil {
			m.onReject = fn
		}
	}
}

// Handler wraps the next handler with rate limiting.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.keyFunc(r)
		decision := m.limiter.Reserve(key)
		if !decision.Allowed {
			m.onReject(w, r, decision)
			return
		}

		ctx := context.WithValue(r.Context(), decisionContextKey, decision)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DecisionFromContext returns the limiter decision stored by Middleware.
func DecisionFromContext(ctx context.Context) (Decision, bool) {
	decision, ok := ctx.Value(decisionContextKey).(Decision)
	return decision, ok
}

// RemoteIPKey extracts a host from Request.RemoteAddr.
func RemoteIPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func defaultReject(w http.ResponseWriter, _ *http.Request, d Decision) {
	retryAfter := retryAfterSeconds(d.WaitFor)
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = fmt.Fprintf(w, "rate limited: %s; retry in %s\n", d.Reason, d.WaitFor.Round(time.Millisecond))
}

func retryAfterSeconds(wait time.Duration) int64 {
	if wait <= 0 {
		return 0
	}

	seconds := wait / time.Second
	if wait%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return int64(seconds)
}
