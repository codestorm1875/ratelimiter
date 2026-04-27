package ratelimiter

import (
	"testing"
	"time"
)

func TestReserveAllowsWithinBurst(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         4,
		Burst:        2,
		HeatHalfLife: 2 * time.Second,
		HeatCost:     0.25,
		MaxKeys:      32,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	if !l.Allow("alpha") {
		t.Fatal("expected first request to be allowed")
	}
	if !l.Allow("beta") {
		t.Fatal("expected second request within burst to be allowed")
	}
}

func TestReserveBlocksWhenGlobalCapacityIsSpent(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         2,
		Burst:        1,
		HeatHalfLife: 2 * time.Second,
		HeatCost:     0,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	if !l.Allow("alpha") {
		t.Fatal("expected first request to be allowed")
	}

	decision := l.Reserve("beta")
	if decision.Allowed {
		t.Fatal("expected second request to require waiting")
	}
	if decision.Reason != "global capacity exhausted" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
	if decision.WaitFor != 500*time.Millisecond {
		t.Fatalf("expected 500ms wait, got %s", decision.WaitFor)
	}
}

func TestRepeatedCallerAccumulatesHeatPenalty(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         100,
		Burst:        20,
		HeatHalfLife: 3 * time.Second,
		HeatCost:     1.5,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	if !l.Allow("alpha") {
		t.Fatal("expected first request to be allowed")
	}

	decision := l.Reserve("alpha")
	if decision.Allowed {
		t.Fatal("expected second back-to-back request from same caller to be delayed")
	}
	if decision.Reason != "caller heat throttled" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
	if decision.WaitFor <= 0 {
		t.Fatal("expected positive wait from caller heat")
	}
}

func TestHeatDecaysOverTime(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         100,
		Burst:        20,
		HeatHalfLife: time.Second,
		HeatCost:     1,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	if !l.Allow("alpha") {
		t.Fatal("expected first request to be allowed")
	}

	immediate := l.Reserve("alpha")
	if immediate.WaitFor <= 0 {
		t.Fatal("expected immediate repeat to incur a wait")
	}

	now = now.Add(5 * time.Second)
	afterCooldown := l.Reserve("alpha")
	if afterCooldown.WaitFor >= immediate.WaitFor {
		t.Fatalf("expected cooled request to wait less, before=%s after=%s", immediate.WaitFor, afterCooldown.WaitFor)
	}
}

func TestEvictsColdestKeyWhenMaxKeysExceeded(t *testing.T) {
	l := mustNewTestLimiter(t, Config{
		Rate:         100,
		Burst:        20,
		HeatHalfLife: time.Second,
		HeatCost:     1,
		MaxKeys:      2,
	})

	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	l.lastRefill = now

	l.Allow("alpha")
	now = now.Add(2 * time.Second)
	l.Allow("beta")
	now = now.Add(2 * time.Second)
	l.Allow("gamma")

	_, keys := l.Snapshot()
	if keys != 2 {
		t.Fatalf("expected active keys to be capped at 2, got %d", keys)
	}
}

func mustNewTestLimiter(t *testing.T, cfg Config) *Limiter {
	t.Helper()

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return l
}
