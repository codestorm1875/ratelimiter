package ratelimiter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddlewareAllowsAndStoresDecision(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         10,
		Burst:        2,
		HeatHalfLife: time.Second,
		HeatCost:     0,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	mw := NewMiddleware(l)

	called := false
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		decision, ok := DecisionFromContext(r.Context())
		if !ok {
			t.Fatal("expected decision in context")
		}
		if !decision.Allowed {
			t.Fatal("expected allowed decision in context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected wrapped handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestMiddlewareRejectsWithDefaultHandler(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         1,
		Burst:        1,
		HeatHalfLife: time.Second,
		HeatCost:     0,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	mw := NewMiddleware(l)
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("wrapped handler should not be called")
	}))

	if !l.Allow("127.0.0.1") {
		t.Fatal("expected warm-up request to consume initial capacity")
	}

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "10.0.0.8:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, second)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("expected Retry-After 1, got %q", rec.Header().Get("Retry-After"))
	}
}

func TestMiddlewareSupportsCustomKeyFunc(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         100,
		Burst:        10,
		HeatHalfLife: 10 * time.Second,
		HeatCost:     2,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	mw := NewMiddleware(l, WithKeyFunc(func(r *http.Request) string {
		return r.Header.Get("X-User-ID")
	}))

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.Header.Set("X-User-ID", "alice")
	first.RemoteAddr = "127.0.0.1:9000"
	handler.ServeHTTP(httptest.NewRecorder(), first)

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.Header.Set("X-User-ID", "alice")
	second.RemoteAddr = "10.0.0.8:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, second)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected custom key function to group requests, got %d", rec.Code)
	}
}

func TestMiddlewareSupportsCustomRejectFunc(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         1,
		Burst:        1,
		HeatHalfLife: time.Second,
		HeatCost:     0,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	customCalled := false
	mw := NewMiddleware(l, WithRejectFunc(func(w http.ResponseWriter, r *http.Request, d Decision) {
		customCalled = true
		w.Header().Set("X-Limit-Reason", d.Reason)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("wrapped handler should not be called")
	}))

	if !l.Allow("127.0.0.1") {
		t.Fatal("expected warm-up request to consume initial capacity")
	}

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "10.0.0.8:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, second)

	if !customCalled {
		t.Fatal("expected custom reject handler to be called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if rec.Header().Get("X-Limit-Reason") == "" {
		t.Fatal("expected rejection reason header")
	}
}

func TestNewMiddlewarePanicsOnNilLimiter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil limiter")
		}
	}()

	NewMiddleware(nil)
}
