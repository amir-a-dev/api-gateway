package gogateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
}

// create tokenbucket table(use for track	each users tokens)
func NewTokeBucket(capacity, refillRate float64) *tokenBucket {
	return &tokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

type RateLimiter struct {
	mu           sync.RWMutex
	tokenBuckets map[string]*tokenBucket
	capacity     float64
	refillRate   float64
}

// create the rate Limiter(use for initiate the rate limiter)
func NewRateLimiter(capacity, refillRate float64) *RateLimiter {
	return &RateLimiter{
		tokenBuckets: make(map[string]*tokenBucket),
		capacity:     capacity,
		refillRate:   refillRate,
	}
}

// check for exist of bucket and get the client bucket in rate limit table
func (rl *RateLimiter) getBucket(key string) *tokenBucket {
	//Lock "just" read operation to prevent race condition
	rl.mu.RLock()
	bucket := rl.tokenBuckets[key]
	rl.mu.RUnlock()
	//check that client have bucket or not
	if bucket != nil {
		return bucket
	}

	// if client have no bucket

	//lock the for whole operation to create a new bucket(prevent race condition)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	//double check for prevent the race condition
	bucket = rl.tokenBuckets[key]

	if bucket == nil {
		//create and set bucket in rate limit table
		bucket = NewTokeBucket(rl.capacity, rl.refillRate)
		rl.tokenBuckets[key] = bucket
	}

	return bucket
}

func (rl *RateLimiter) allow(key string) bool {

	bucket := rl.getBucket(key)
	// lock operation for prevent race condition
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	now := time.Now()
	//set the amount of token according	to last request
	bucket.tokens += now.Sub(bucket.lastRefill).Seconds() * bucket.refillRate

	// keep tokens in the max capacity
	if bucket.tokens > bucket.capacity {
		bucket.tokens = bucket.capacity
	}

	//set new last Refill
	bucket.lastRefill = now

	//check for user to have at least 1 token
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}

	return false
}

// for get the IP of client
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

// the middleware
func (rl *RateLimiter) RatelimiterMW(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//get the IP of client for check tokens or create a token table for client
		key := rl.getClientIP(r)
		// check client have token to use for request or not
		if !rl.allow(key) {
			w.Header().Set("Retry-After", "1") // 👈 polite: tell client when to retry
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		//pass to next middleware
		next.ServeHTTP(w, r)
	})

}
