package middleware

import (
	"net/http"
	"sync"
	"time"

	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
)

// RateLimiter is a simple in-memory token bucket rate limiter per IP.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string]*bucket
	rate     int           // max requests per window
	window   time.Duration // time window
	burst    int           // max burst size
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a new rate limiter.
// rate: max requests per window per IP.
// window: duration of the rate limit window.
// burst: max burst size (0 = same as rate).
func NewRateLimiter(rate int, window time.Duration, burst int) *RateLimiter {
	if burst <= 0 {
		burst = rate
	}
	return &RateLimiter{
		requests: make(map[string]*bucket),
		rate:     rate,
		window:   window,
		burst:    burst,
	}
}

// RateLimit returns a Gin middleware that rate-limits requests per IP.
func (rl *RateLimiter) RateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip == "" {
			ip = c.RemoteIP()
		}

		allowed := rl.allow(ip)
		if !allowed {
			response.Error(c, http.StatusTooManyRequests, "rate_limited", "too many requests, please try again later")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.requests[ip]
	if !exists {
		rl.requests[ip] = &bucket{
			tokens:    rl.burst - 1,
			lastReset: now,
		}
		return true
	}

	// Reset if window has passed
	elapsed := now.Sub(b.lastReset)
	if elapsed >= rl.window {
		b.tokens = rl.burst
		b.lastReset = now
	}

	if b.tokens <= 0 {
		return false
	}

	b.tokens--
	return true
}

// CleanupOldEntries periodically removes stale entries to prevent memory leak.
func (rl *RateLimiter) CleanupOldEntries(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.requests {
				if now.Sub(b.lastReset) > rl.window*2 {
					delete(rl.requests, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
}
