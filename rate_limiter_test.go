package gogateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------
// allow()
// ---------------------------------------------------------------

func TestAllow_BucketStartsFull(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(3, 1)
	for i := 1; i <= 3; i++ {
		if !rl.allow("client") {
			t.Fatalf("request %d should be allowed — bucket must start full", i)
		}
	}
	if rl.allow("client") {
		t.Fatal("4th request should be denied — bucket should be empty")
	}
}

func TestAllow_IndependentPerKey(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(2, 1)

	// Exhaust bucket A
	rl.allow("A")
	rl.allow("A")
	if rl.allow("A") {
		t.Fatal("A should be exhausted")
	}

	// Bucket B must be untouched
	if !rl.allow("B") {
		t.Fatal("B should still have full capacity")
	}
	if !rl.allow("B") {
		t.Fatal("B should still have 1 token")
	}
	if rl.allow("B") {
		t.Fatal("B should now be exhausted")
	}
}

func TestAllow_Refill(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(5, 2) // capacity 5, refill 2 tokens/sec

	for i := range 5 {
		if !rl.allow("k") {
			t.Fatalf("token %d should be available", i)
		}
	}
	if rl.allow("k") {
		t.Fatal("bucket should be empty")
	}

	// Pretend 1 second has elapsed since the last refill.
	b := rl.getBuket("k")
	b.mu.Lock()
	b.lastRefill = b.lastRefill.Add(-1 * time.Second)
	b.mu.Unlock()

	// 1s * 2 tokens/s = 2 tokens available.
	if !rl.allow("k") {
		t.Fatal("1st request after 1s refill should pass")
	}
	if !rl.allow("k") {
		t.Fatal("2nd request after 1s refill should pass")
	}
	if rl.allow("k") {
		t.Fatal("3rd request should be denied — only 2 tokens refilled")
	}
}

func TestAllow_CapsAtCapacity(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(2, 100)

	b := rl.getBuket("k")
	b.mu.Lock()
	b.lastRefill = b.lastRefill.Add(-1 * time.Hour) // huge refill would be 360k tokens
	b.mu.Unlock()

	// But capacity is 2 — no matter how much refills, only 2 can pass.
	if !rl.allow("k") {
		t.Fatal("1st should pass")
	}
	if !rl.allow("k") {
		t.Fatal("2nd should pass")
	}
	if rl.allow("k") {
		t.Fatal("3rd should fail — tokens must cap at capacity")
	}
}

// Regression test for the lastRefill-not-updated bug.
// If allow() never resets lastRefill, every call re-refills based on
// "time since creation" and the limiter admits every request once aged.
func TestAllow_BucketAgeDoesNotBypassLimit(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(5, 2)

	// Exhaust the bucket.
	for range 5 {
		rl.allow("k")
	}

	// Age it 10 seconds.
	b := rl.getBuket("k")
	b.mu.Lock()
	b.lastRefill = b.lastRefill.Add(-10 * time.Second)
	b.mu.Unlock()

	// Correct behavior: 10s * 2/s = 20 tokens → capped at 5 → allow 5, then deny.
	// Buggy behavior: every call re-adds ~20 tokens → allow all 10.
	allowed := 0
	for range 10 {
		if rl.allow("k") {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed %d of 10 requests after 10s idle; want 5 (capacity) — lastRefill is probably not being updated", allowed)
	}
}

// ---------------------------------------------------------------
// getBuket()
// ---------------------------------------------------------------

func TestGetBucket_SameKeyReturnsSameBucket(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(5, 1)
	b1 := rl.getBuket("k")
	b2 := rl.getBuket("k")
	if b1 != b2 {
		t.Fatal("same key should return identical bucket pointer")
	}
}

func TestGetBucket_DifferentKeysReturnDifferentBuckets(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(5, 1)
	if rl.getBuket("a") == rl.getBuket("b") {
		t.Fatal("different keys should have different buckets")
	}
}

func TestGetBucket_ConcurrentCreatesOneBucket(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(100, 1)

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			rl.getBuket("shared")
		})
	}
	wg.Wait()

	rl.mu.RLock()
	n := len(rl.tokenBukets)
	rl.mu.RUnlock()
	if n != 1 {
		t.Fatalf("expected 1 bucket for shared key, got %d", n)
	}
}

// ---------------------------------------------------------------
// getClientIP()
// ---------------------------------------------------------------

func TestGetClientIP(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(1, 1)

	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For single IP",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For list picks first",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4, 9.9.9.9, 8.8.8.8"},
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For with surrounding spaces",
			headers:    map[string]string{"X-Forwarded-For": "  1.2.3.4  "},
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For empty first entry falls through",
			headers:    map[string]string{"X-Forwarded-For": ", 1.2.3.4"},
			remoteAddr: "5.6.7.8:1234",
			want:       "5.6.7.8",
		},
		{
			name:       "X-Real-IP used when XFF absent",
			headers:    map[string]string{"X-Real-IP": "1.2.3.4"},
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For wins over X-Real-IP",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "9.9.9.9"},
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "RemoteAddr with port",
			headers:    nil,
			remoteAddr: "5.6.7.8:1234",
			want:       "5.6.7.8",
		},
		{
			name:       "RemoteAddr without port falls back to raw",
			headers:    nil,
			remoteAddr: "5.6.7.8",
			want:       "5.6.7.8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if got := rl.getClientIP(req); got != tt.want {
				t.Errorf("getClientIP() = %q; want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------
// RatelimiterMW()
// ---------------------------------------------------------------

func TestRatelimiterMW(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		capacity   float64
		refillRate float64
		requests   int
		wantNext   int // number of times next should be called
	}{
		{name: "under limit", capacity: 5, refillRate: 1, requests: 3, wantNext: 3},
		{name: "over limit", capacity: 2, refillRate: 1, requests: 5, wantNext: 2},
		{name: "exactly at limit", capacity: 3, refillRate: 1, requests: 3, wantNext: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rl := NewRateLimiter(tt.capacity, tt.refillRate)
			nextCalls := 0
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalls++
				w.WriteHeader(http.StatusOK)
			})
			mw := rl.RatelimiterMW(next)

			var last *httptest.ResponseRecorder
			for i := 0; i < tt.requests; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "1.2.3.4:5678"
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				last = rec
			}

			if nextCalls != tt.wantNext {
				t.Errorf("next called %d times; want %d", nextCalls, tt.wantNext)
			}
			if tt.wantNext < tt.requests {
				if last.Code != http.StatusTooManyRequests {
					t.Errorf("last status = %d; want %d", last.Code, http.StatusTooManyRequests)
				}
				if got := last.Header().Get("Retry-After"); got != "1" {
					t.Errorf("Retry-After = %q; want %q", got, "1")
				}
			}
		})
	}
}

func TestRatelimiterMW_PerClientIsolation(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(1, 1)
	mw := rl.RatelimiterMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send("1.1.1.1"); code != http.StatusOK {
		t.Errorf("client A 1st = %d; want 200", code)
	}
	if code := send("1.1.1.1"); code != http.StatusTooManyRequests {
		t.Errorf("client A 2nd = %d; want 429", code)
	}
	// Client B must be unaffected.
	if code := send("2.2.2.2"); code != http.StatusOK {
		t.Errorf("client B 1st = %d; want 200", code)
	}
}

func TestRatelimiterMW_UsesXFForwardedFor(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter(1, 1)
	mw := rl.RatelimiterMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234" // same proxy IP for everyone
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send("1.1.1.1"); code != http.StatusOK {
		t.Errorf("xff=1.1.1.1 1st = %d; want 200", code)
	}
	// Different real client behind the same proxy → fresh bucket.
	if code := send("2.2.2.2"); code != http.StatusOK {
		t.Errorf("xff=2.2.2.2 1st = %d; want 200 (must not share proxy's bucket)", code)
	}
	// Same real client again → blocked.
	if code := send("1.1.1.1"); code != http.StatusTooManyRequests {
		t.Errorf("xff=1.1.1.1 2nd = %d; want 429", code)
	}
}

// ---------------------------------------------------------------
// Concurrency — run with: go test -race
// ---------------------------------------------------------------

func TestRateLimiter_Concurrent(t *testing.T) {
	t.Parallel()

	// refillRate 0 → fully deterministic count under any scheduling.
	rl := NewRateLimiter(1000, 0)

	const goroutines = 10
	const perGoroutine = 100

	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)

	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				if rl.allow("shared") {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()

	if got := allowed.Load(); got != int64(goroutines*perGoroutine) {
		t.Fatalf("allowed %d requests; want %d", got, goroutines*perGoroutine)
	}
}
