package ratelimiter

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkAllowHotKey(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if !l.Allow("hot") {
			b.Fatal("unexpected throttle in hot-key benchmark")
		}
	}
}

func BenchmarkAllowDistributedKeys(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      4096,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	keys := benchmarkKeys(1024)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if !l.Allow(keys[i%len(keys)]) {
			b.Fatal("unexpected throttle in distributed benchmark")
		}
	}
}

func BenchmarkReserveHotKeyWithHeat(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0.25,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = l.Reserve("hot")
	}
}

func BenchmarkReserveDistributedKeysWithHeat(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0.25,
		MaxKeys:      4096,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	keys := benchmarkKeys(1024)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = l.Reserve(keys[i%len(keys)])
	}
}

func BenchmarkMiddlewareHotKey(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	handler := NewMiddleware(l).Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	w := newBenchmarkResponseWriter()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w.reset()
		handler.ServeHTTP(w, req)
		if w.status != http.StatusNoContent {
			b.Fatalf("unexpected status code: %d", w.status)
		}
	}
}

func BenchmarkRemoteIPKey(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9000"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if got := RemoteIPKey(req); got != "127.0.0.1" {
			b.Fatalf("unexpected key: %q", got)
		}
	}
}

func BenchmarkAllowHotKeyParallel(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if !l.Allow("hot") {
				b.Fatal("unexpected throttle in hot-key parallel benchmark")
			}
		}
	})
}

func BenchmarkAllowDistributedKeysParallel(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      4096,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	keys := benchmarkKeys(1024)
	var counter atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx := counter.Add(1) - 1
			if !l.Allow(keys[idx%uint64(len(keys))]) {
				b.Fatal("unexpected throttle in distributed parallel benchmark")
			}
		}
	})
}

func BenchmarkReserveHotKeyWithHeatParallel(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0.25,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = l.Reserve("hot")
		}
	})
}

func BenchmarkReserveDistributedKeysWithHeatParallel(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0.25,
		MaxKeys:      4096,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	keys := benchmarkKeys(1024)
	var counter atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx := counter.Add(1) - 1
			_ = l.Reserve(keys[idx%uint64(len(keys))])
		}
	})
}

func BenchmarkMiddlewareHotKeyParallel(b *testing.B) {
	l := benchmarkLimiter(b, Config{
		Rate:         1_000_000,
		Burst:        1024,
		HeatHalfLife: 5 * time.Second,
		HeatCost:     0,
		MaxKeys:      128,
	})
	benchmarkClock(l, time.Unix(100, 0), time.Microsecond)

	handler := NewMiddleware(l).Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9000"

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		w := newBenchmarkResponseWriter()
		for pb.Next() {
			w.reset()
			handler.ServeHTTP(w, req)
			if w.status != http.StatusNoContent {
				b.Fatalf("unexpected status code: %d", w.status)
			}
		}
	})
}

func benchmarkLimiter(b *testing.B, cfg Config) *Limiter {
	b.Helper()

	l, err := New(cfg)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}

	return l
}

func benchmarkKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	return keys
}

func benchmarkClock(l *Limiter, start time.Time, step time.Duration) {
	var tick atomic.Int64
	startUnix := start.UnixNano()
	stepNanos := step.Nanoseconds()
	l.now = func() time.Time {
		offset := tick.Add(1) - 1
		return time.Unix(0, startUnix+offset*stepNanos)
	}
	l.lastRefill = start
}

type benchmarkResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBenchmarkResponseWriter() *benchmarkResponseWriter {
	return &benchmarkResponseWriter{header: make(http.Header)}
}

func (w *benchmarkResponseWriter) Header() http.Header {
	return w.header
}

func (w *benchmarkResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *benchmarkResponseWriter) reset() {
	w.status = 0
	w.body.Reset()
	for k := range w.header {
		delete(w.header, k)
	}
}
