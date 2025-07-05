package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter manages rate limiting for the application
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	limit    rate.Limit
	burst    int
	logger   *slog.Logger
}

// visitor represents a single user's rate limiter
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerSecond float64, burst int, logger *slog.Logger) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		limit:    rate.Limit(requestsPerSecond),
		burst:    burst,
		logger:   logger,
	}

	// Start cleanup goroutine to remove old visitors
	go rl.cleanupVisitors()

	return rl
}

// getVisitor retrieves or creates a rate limiter for a specific user
func (rl *RateLimiter) getVisitor(userID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[userID]
	if !exists {
		limiter := rate.NewLimiter(rl.limit, rl.burst)
		rl.visitors[userID] = &visitor{
			limiter:  limiter,
			lastSeen: time.Now(),
		}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

// cleanupVisitors removes visitors that haven't been seen for over an hour
func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		for id, v := range rl.visitors {
			if time.Since(v.lastSeen) > time.Hour {
				delete(rl.visitors, id)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimitMiddleware creates a middleware that enforces rate limits
func (rl *RateLimiter) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get user from context
		user, err := GetUser(r)
		if err != nil {
			// If no user context, fall back to IP-based limiting
			userID := r.RemoteAddr
			limiter := rl.getVisitor(userID)

			if !limiter.Allow() {
				rl.logger.WarnContext(r.Context(), "rate limit exceeded (IP)",
					slog.String("ip", r.RemoteAddr),
					slog.String("path", r.URL.Path))
				http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
				return
			}
		} else {
			// User-based rate limiting
			userID := fmt.Sprintf("user:%d", user.ID)
			limiter := rl.getVisitor(userID)

			if !limiter.Allow() {
				rl.logger.WarnContext(r.Context(), "rate limit exceeded (user)",
					slog.Int64("user_id", user.ID),
					slog.String("path", r.URL.Path))
				http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
				return
			}
		}

		// Add rate limit headers
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", float64(rl.burst)))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))

		next.ServeHTTP(w, r)
	})
}

// EndpointRateLimiter provides per-endpoint rate limiting
type EndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	logger   *slog.Logger
}

// NewEndpointRateLimiter creates a new endpoint-specific rate limiter
func NewEndpointRateLimiter(logger *slog.Logger) *EndpointRateLimiter {
	return &EndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
		logger:   logger,
	}
}

// AddEndpoint adds a rate limiter for a specific endpoint pattern
func (erl *EndpointRateLimiter) AddEndpoint(pattern string, requestsPerSecond float64, burst int) {
	erl.limiters[pattern] = NewRateLimiter(requestsPerSecond, burst, erl.logger)
}

// Middleware returns a middleware that applies endpoint-specific rate limits
func (erl *EndpointRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for endpoint-specific rate limiter
		for pattern, limiter := range erl.limiters {
			if matched, _ := path.Match(pattern, r.URL.Path); matched {
				limiter.RateLimitMiddleware(next).ServeHTTP(w, r)
				return
			}
		}

		// No specific rate limit for this endpoint, pass through
		next.ServeHTTP(w, r)
	})
}

// WaitRateLimiter provides a rate limiter that waits instead of rejecting
func (rl *RateLimiter) WaitRateLimitMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			user, err := GetUser(r)
			var userID string
			if err != nil {
				userID = r.RemoteAddr
			} else {
				userID = fmt.Sprintf("user:%d", user.ID)
			}

			limiter := rl.getVisitor(userID)

			// Wait for permission with timeout
			err = limiter.Wait(ctx)
			if err != nil {
				rl.logger.WarnContext(r.Context(), "rate limit wait timeout",
					slog.String("user", userID),
					slog.String("path", r.URL.Path))
				http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
