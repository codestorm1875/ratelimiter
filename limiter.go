package ratelimiter

import (
	"errors"
	"math"
	"sync"
	"time"
)

var (
	ErrInvalidRate     = errors.New("rate must be greater than zero")
	ErrInvalidBurst    = errors.New("burst must be greater than zero")
	ErrInvalidHalfLife = errors.New("half-life must be greater than zero")
	ErrInvalidHeatCost = errors.New("heat cost must be zero or greater")
	ErrNilLimiter      = errors.New("limiter must not be nil")
)

// Decision describes the outcome of a reservation attempt.
type Decision struct {
	Allowed bool
	ReadyAt time.Time
	WaitFor time.Duration
	Reason  string
}

// Config tunes the limiter.
//
// Rate and Burst behave like a token bucket. HeatHalfLife and HeatCost add a
// caller-specific penalty that decays over time, making repeated bursts from
// the same key back off sooner than evenly distributed traffic.
type Config struct {
	Rate         float64
	Burst        int
	HeatHalfLife time.Duration
	HeatCost     float64
	MaxKeys      int
}

// Limiter is a token bucket with caller heat decay.
type Limiter struct {
	mu           sync.Mutex
	rate         float64
	burst        float64
	tokens       float64
	lastRefill   time.Time
	heatHalfLife time.Duration
	heatCost     float64
	maxKeys      int
	callers      map[string]callerState
	now          func() time.Time
}

type callerState struct {
	heat float64
	last time.Time
}

// New creates a limiter with a caller-fairness penalty.
func New(cfg Config) (*Limiter, error) {
	if cfg.Rate <= 0 {
		return nil, ErrInvalidRate
	}
	if cfg.Burst <= 0 {
		return nil, ErrInvalidBurst
	}
	if cfg.HeatHalfLife <= 0 {
		return nil, ErrInvalidHalfLife
	}
	if cfg.HeatCost < 0 {
		return nil, ErrInvalidHeatCost
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 1024
	}

	now := time.Now()
	return &Limiter{
		rate:         cfg.Rate,
		burst:        float64(cfg.Burst),
		tokens:       float64(cfg.Burst),
		lastRefill:   now,
		heatHalfLife: cfg.HeatHalfLife,
		heatCost:     cfg.HeatCost,
		maxKeys:      cfg.MaxKeys,
		callers:      make(map[string]callerState, min(cfg.MaxKeys, 64)),
		now:          time.Now,
	}, nil
}

// Allow reports whether a single action can proceed immediately.
func (l *Limiter) Allow(key string) bool {
	return l.AllowN(key, 1)
}

// AllowN reports whether n actions can proceed immediately.
func (l *Limiter) AllowN(key string, n int) bool {
	now := l.now()
	if n <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill(now)

	globalWait := l.previewGlobal(float64(n))
	fairnessWait := l.previewCallerHeat(key, now)
	if maxDuration(globalWait, fairnessWait) > 0 {
		return false
	}

	l.commitGlobal(float64(n))
	l.commitCallerHeat(key, now, float64(n))
	return true
}

// Reserve returns when the request is allowed to proceed. Requests that would
// need to wait return Allowed=false; callers can inspect WaitFor or ReadyAt.
func (l *Limiter) Reserve(key string) Decision {
	return l.ReserveN(key, 1)
}

// ReserveN attempts to acquire n actions for the caller key.
func (l *Limiter) ReserveN(key string, n int) Decision {
	now := l.now()
	if n <= 0 {
		return Decision{
			Allowed: true,
			ReadyAt: now,
			WaitFor: 0,
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill(now)

	globalWait := l.previewGlobal(float64(n))
	fairnessWait := l.previewCallerHeat(key, now)
	waitFor := maxDuration(globalWait, fairnessWait)
	allowed := waitFor == 0

	if allowed {
		l.commitGlobal(float64(n))
		l.commitCallerHeat(key, now, float64(n))
	}

	reason := ""
	if !allowed {
		switch {
		case globalWait > fairnessWait:
			reason = "global capacity exhausted"
		case fairnessWait > globalWait:
			reason = "caller heat throttled"
		default:
			reason = "global and caller limits engaged"
		}
	}

	return Decision{
		Allowed: allowed,
		ReadyAt: now.Add(waitFor),
		WaitFor: waitFor,
		Reason:  reason,
	}
}

// Snapshot exposes the current token estimate and active caller count.
func (l *Limiter) Snapshot() (tokens float64, activeKeys int) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill(now)
	return l.tokens, len(l.callers)
}

func (l *Limiter) refill(now time.Time) {
	if !now.After(l.lastRefill) {
		return
	}

	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens = math.Min(l.burst, l.tokens+elapsed*l.rate)
	l.lastRefill = now
}

func (l *Limiter) previewGlobal(amount float64) time.Duration {
	if l.tokens >= amount {
		return 0
	}

	shortage := amount - l.tokens
	seconds := shortage / l.rate
	return durationFromSeconds(seconds)
}

func (l *Limiter) commitGlobal(amount float64) {
	l.tokens -= amount
}

func (l *Limiter) previewCallerHeat(key string, now time.Time) time.Duration {
	state, ok := l.callers[key]
	if ok {
		state.heat = decayHeat(state.heat, now.Sub(state.last), l.heatHalfLife)
	}

	penaltySeconds := 0.0
	if l.heatCost > 0 && state.heat > 0 {
		penaltySeconds = (state.heat * l.heatCost) / l.rate
	}

	return durationFromSeconds(penaltySeconds)
}

func (l *Limiter) commitCallerHeat(key string, now time.Time, amount float64) {
	state, ok := l.callers[key]
	if ok {
		state.heat = decayHeat(state.heat, now.Sub(state.last), l.heatHalfLife)
	}

	state.heat += amount
	state.last = now
	l.callers[key] = state

	if len(l.callers) > l.maxKeys {
		l.evictColdest(now)
	}
}

func (l *Limiter) evictColdest(now time.Time) {
	var (
		victimKey  string
		victimHeat = math.MaxFloat64
		found      bool
	)

	for key, state := range l.callers {
		heat := decayHeat(state.heat, now.Sub(state.last), l.heatHalfLife)
		if heat < victimHeat {
			victimHeat = heat
			victimKey = key
			found = true
		}
	}

	if found {
		delete(l.callers, victimKey)
	}
}

func decayHeat(heat float64, elapsed time.Duration, halfLife time.Duration) float64 {
	if heat <= 0 || elapsed <= 0 {
		return heat
	}

	return heat * math.Pow(0.5, float64(elapsed)/float64(halfLife))
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
