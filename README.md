# Pulse Rate Limiter

`ratelimiter` is a Go library for global rate control with caller fairness.

It behaves like a token bucket, but it also tracks a per-caller heat score:

- accepted requests add heat to the caller
- heat decays exponentially over time
- hotter callers pay a larger wait penalty than colder callers

That makes it useful when you want shared capacity, but do not want one noisy tenant to dominate short bursts.

## How Heat Works

Think of heat as a short-term memory of how aggressive a caller has been recently.

Each accepted request adds heat to that caller. Over time, the heat cools down automatically. `HeatHalfLife` controls that cooling rate: after one half-life, the caller keeps only half of its current heat; after another half-life, half again. `HeatCost` controls how much that stored heat turns into extra delay.

In practice, two callers can send the same total number of requests and still be treated differently if one of them arrives in a burst. The bursty caller accumulates more heat and starts paying an adaptive penalty sooner, while the steadier caller keeps flowing. That is what makes this limiter feel fairer under uneven traffic.

## Features

- Global token-bucket style rate and burst limits
- Per-caller heat decay for short-window fairness
- O(1) fast path for allow/reserve checks
- HTTP middleware with custom key extraction and rejection handling
- Allocation-free limiter hot path

## Install

```bash
go get github.com/codestorm1875/ratelimiter@latest
```

## Quick Start

```go
package main

import (
	"fmt"
	"time"

	ratelimiter "github.com/codestorm1875/ratelimiter"
)

func main() {
	limiter, err := ratelimiter.New(ratelimiter.Config{
		Rate:         20,
		Burst:        10,
		HeatHalfLife: 3 * time.Second,
		HeatCost:     1.25,
		MaxKeys:      2048,
	})
	if err != nil {
		panic(err)
	}

	decision := limiter.Reserve("alice")
	fmt.Printf("allowed=%v wait=%s reason=%q\n", decision.Allowed, decision.WaitFor, decision.Reason)
}
```

## Core API

- `New(Config) (*Limiter, error)`
- `Allow(key string) bool`
- `AllowN(key string, n int) bool`
- `Reserve(key string) Decision`
- `ReserveN(key string, n int) Decision`
- `Snapshot() (tokens float64, activeKeys int)`

`Decision` includes:

- `Allowed`: whether the request can proceed immediately
- `ReadyAt`: when the request should be retried
- `WaitFor`: computed delay before the request can run
- `Reason`: human-readable throttling reason when blocked

## HTTP Middleware

The middleware wraps an `http.Handler` and rate-limits each request using a key extracted from the request.

```go
package main

import (
	"net/http"
	"time"

	ratelimiter "github.com/codestorm1875/ratelimiter"
)

func main() {
	limiter, err := ratelimiter.New(ratelimiter.Config{
		Rate:         50,
		Burst:        20,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     1.5,
		MaxKeys:      4096,
	})
	if err != nil {
		panic(err)
	}

	http.Handle("/api", limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if decision, ok := ratelimiter.DecisionFromContext(r.Context()); ok {
			_ = decision
		}
		w.Write([]byte("ok"))
	}), ratelimiter.WithKeyFunc(func(r *http.Request) string {
		if userID := r.Header.Get("X-User-ID"); userID != "" {
			return userID
		}
		return ratelimiter.RemoteIPKey(r)
	})))

	http.ListenAndServe(":8080", nil)
}
```

If you want the explicit builder form, you can still use:

```go
middleware := ratelimiter.NewMiddleware(limiter)
http.Handle("/api", middleware.Handler(myHandler))
```

Default middleware behavior:

- keys traffic by `RemoteAddr`
- returns `429 Too Many Requests`
- sets `Retry-After`
- stores the successful `Decision` on the request context

Custom options:

- `WithKeyFunc`: override how a request key is derived
- `WithRejectFunc`: override the rejection response

## Tuning

- `Rate`: tokens replenished per second
- `Burst`: maximum burst size
- `HeatHalfLife`: how quickly caller heat cools
- `HeatCost`: extra virtual cost charged for existing heat
- `MaxKeys`: cap on tracked callers; coldest caller is evicted first

Lower `HeatHalfLife` or `HeatCost` makes the limiter behave closer to a standard token bucket.

## Why Not Just Use `golang.org/x/time/rate`?

`golang.org/x/time/rate` is a good default when all you need is a standard token bucket for one stream of traffic.

This library is aimed at a different problem: shared capacity across many callers where one hot client should not dominate short windows just because tokens happen to be available. A plain token bucket answers "is there capacity right now?" This limiter also asks "has this caller been consuming that capacity more aggressively than others?"

If your traffic is single-tenant or you already create one independent limiter per client, `x/time/rate` is often enough. If you want one shared limiter with adaptive penalties for bursty callers, this heat-based model is the reason to use this library instead.

## Testing And Benchmarking

Tests and benchmarks live in the `*_test.go` files in this repository.

```bash
go test ./...
go test -bench . -benchmem ./...
```

To run each file's checks directly:

- `limiter_test.go`
  - `go test -run 'TestReserve|TestRepeatedCaller|TestHeatDecays|TestEvicts' ./...`
- `middleware_test.go`
  - `go test -run 'TestMiddleware|TestNewMiddleware' ./...`
- `benchmark_test.go`
  - `go test -bench . -benchmem ./...`

The benchmark suite includes:

- hot-key and distributed-key limiter paths
- serial and parallel variants
- middleware and key-extraction measurements

Useful focused commands:

- `go test -run TestMiddleware ./...` to run a specific test group
- `go test -run TestReserve ./...` to run a specific limiter test group
- `go test -bench BenchmarkMiddlewareHotKey -benchmem ./...` to run one benchmark

## Project Layout

- `limiter.go`: core limiter implementation
- `middleware.go`: HTTP adapter
- `*_test.go`: correctness and benchmark coverage

## Notes

The limiter is designed for shared services where fairness matters more than strict per-call exactness. If you need strict queueing or absolute ordering, this is the wrong abstraction.
