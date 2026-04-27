package ratelimiter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterMiddlewareHelper(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         10,
		Burst:        1,
		HeatHalfLife: time.Second,
		HeatCost:     0,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	called := false
	handler := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
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
