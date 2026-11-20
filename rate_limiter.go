package gogateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type tokenBuket struct {
	mu         sync.Mutex
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
}

func NewTokeBuket(capacity, refillRate float64) *tokenBuket {
	return &tokenBuket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

type RateLimiter struct {
	mu          sync.RWMutex
	tokenBukets map[string]*tokenBuket
	capacity    float64
	refillRate  float64
}

func NewRateLimiter(capacity, refillRate float64) *RateLimiter {
	return &RateLimiter{
		tokenBukets: make(map[string]*tokenBuket),
		capacity:    capacity,
		refillRate:  refillRate,
	}
}

func (rl *RateLimiter) getBuket(key string) *tokenBuket {
	rl.mu.RLock()
	buket := rl.tokenBukets[key]
	rl.mu.RUnlock()

	if buket != nil {
		return buket
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	buket = rl.tokenBukets[key]

	if buket == nil {
		buket = NewTokeBuket(rl.capacity, rl.refillRate)
		rl.tokenBukets[key] = buket
	}

	return buket
}

func (rl *RateLimiter) allow(key string) bool {

	buket := rl.getBuket(key)

	buket.mu.Lock()
	defer buket.mu.Unlock()

	now := time.Now()

	buket.tokens += now.Sub(buket.lastRefill).Seconds() * buket.refillRate

	if buket.tokens > buket.capacity {
		buket.tokens = buket.capacity
	}

	buket.lastRefill = now

	if buket.tokens >= 1 {
		buket.tokens--
		return true
	}

	return false

}

func (rl *RateLimiter) getClientIP(r *http.Request) string {
	// Behind a proxy/load balancer, the real client is in these headers.
	// X-Forwarded-For can be a comma-separated list: "client, proxy1, proxy2"
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Direct connection: RemoteAddr is "IP:port"
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // fallback: probably already a bare IP
	}
	return ip
}

func (rl *RateLimiter) RatelimiterMW(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.getClientIP(r)

		if !rl.allow(key) {
			w.Header().Set("Retry-After", "1") // 👈 polite: tell client when to retry
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})

}
