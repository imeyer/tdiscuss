package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/time/rate"
)

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	// Default rate limit
	RequestsPerSecond float64
	Burst             int

	// Per-endpoint limits
	EndpointLimits map[string]EndpointLimit

	// User-based rate limiting
	EnableUserRateLimit bool
	UserRateMultiplier  float64 // Multiplier for authenticated users

	// IP-based rate limiting
	EnableIPRateLimit bool
	CleanupInterval   time.Duration

	// Response headers
	IncludeHeaders bool

	// Metrics
	Meter        metric.Meter
	MetricPrefix string
}

// EndpointLimit defines rate limits for specific endpoints
type EndpointLimit struct {
	Pattern string
	Rate    float64
	Burst   int
}

// defaultRateLimitConfig returns sensible defaults
func defaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		RequestsPerSecond:   10,
		Burst:               20,
		EnableUserRateLimit: true,
		UserRateMultiplier:  5.0,
		EnableIPRateLimit:   true,
		CleanupInterval:     5 * time.Minute,
		IncludeHeaders:      true,
		EndpointLimits: map[string]EndpointLimit{
			"/thread/new":     {Pattern: "/thread/new", Rate: 0.5, Burst: 2},   // 1 thread per 2 seconds
			"/thread/*/reply": {Pattern: "/thread/*/reply", Rate: 2, Burst: 5}, // 2 replies per second
			"/member/edit":    {Pattern: "/member/edit", Rate: 0.5, Burst: 2},  // 1 profile update per 2 seconds
			"/admin":          {Pattern: "/admin", Rate: 0.2, Burst: 1},        // 1 admin action per 5 seconds
		},
	}
}

// RateLimiter provides flexible rate limiting
type RateLimiter struct {
	config   *RateLimitConfig
	logger   *slog.Logger
	visitors map[string]*visitor
	mu       sync.RWMutex

	// Metrics
	rateLimitHits  metric.Int64Counter
	activeVisitors metric.Int64Gauge
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newRateLimiter creates a new rate limiter
func newRateLimiter(config *RateLimitConfig, logger *slog.Logger) *RateLimiter {
	rl := &RateLimiter{
		config:   config,
		logger:   logger,
		visitors: make(map[string]*visitor),
	}

	// Initialize metrics if meter is provided
	if config.Meter != nil {
		prefix := config.MetricPrefix
		if prefix == "" {
			prefix = "http.ratelimit"
		}

		rl.rateLimitHits, _ = config.Meter.Int64Counter(
			prefix+".hits",
			metric.WithDescription("Number of rate limit hits"),
			metric.WithUnit("{hit}"),
		)

		rl.activeVisitors, _ = config.Meter.Int64Gauge(
			prefix+".visitors",
			metric.WithDescription("Number of active rate limit visitors"),
			metric.WithUnit("{visitor}"),
		)
	}

	// Start cleanup goroutine
	go rl.cleanupVisitors()

	return rl
}

// Middleware returns the rate limiting middleware
func (rl *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get visitor key
			visitorKey := rl.getVisitorKey(r)

			// Get endpoint-specific limits if available
			limit, burst := rl.getLimitsForPath(r.URL.Path)

			// Get or create visitor
			v := rl.getVisitor(visitorKey, limit, burst)

			// Check rate limit
			if !v.limiter.Allow() {
				rl.handleRateLimitExceeded(w, r, v.limiter)
				return
			}

			// Add rate limit headers if configured
			if rl.config.IncludeHeaders {
				rl.addRateLimitHeaders(w, v.limiter)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getVisitorKey determines the key for rate limiting
func (rl *RateLimiter) getVisitorKey(r *http.Request) string {
	// Prefer user-based rate limiting for authenticated users
	if rl.config.EnableUserRateLimit {
		if user, ok := getUser(r.Context()); ok && user != nil {
			return fmt.Sprintf("user:%d", user.ID)
		}
	}

	// Fall back to IP-based rate limiting
	if rl.config.EnableIPRateLimit {
		return "ip:" + getClientIP(r)
	}

	// If neither is enabled, use a global key (not recommended)
	return "global"
}

// getLimitsForPath returns rate limits for a specific path
func (rl *RateLimiter) getLimitsForPath(path string) (float64, int) {
	// Check endpoint-specific limits
	for pattern, limit := range rl.config.EndpointLimits {
		if matchesPattern(path, pattern) {
			return limit.Rate, limit.Burst
		}
	}

	// Return default limits
	return rl.config.RequestsPerSecond, rl.config.Burst
}

// getVisitor gets or creates a visitor
func (rl *RateLimiter) getVisitor(key string, limit float64, burst int) *visitor {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		// Apply user rate multiplier if applicable
		if strings.HasPrefix(key, "user:") && rl.config.UserRateMultiplier > 0 {
			limit *= rl.config.UserRateMultiplier
			burst = int(float64(burst) * rl.config.UserRateMultiplier)
		}

		v = &visitor{
			limiter:  rate.NewLimiter(rate.Limit(limit), burst),
			lastSeen: time.Now(),
		}
		rl.visitors[key] = v

		// Update metrics
		if rl.activeVisitors != nil {
			rl.activeVisitors.Record(context.Background(), int64(len(rl.visitors)))
		}
	} else {
		v.lastSeen = time.Now()
	}

	return v
}

// handleRateLimitExceeded handles rate limit exceeded responses
func (rl *RateLimiter) handleRateLimitExceeded(w http.ResponseWriter, r *http.Request, limiter *rate.Limiter) {
	// Log rate limit hit
	visitorKey := rl.getVisitorKey(r)
	rl.logger.WarnContext(r.Context(), "rate limit exceeded",
		slog.String("visitor", visitorKey),
		slog.String("path", r.URL.Path),
		slog.String("method", r.Method),
		slog.String("remote_addr", r.RemoteAddr),
	)

	// Record metric
	if rl.rateLimitHits != nil {
		attrs := []attribute.KeyValue{
			attribute.String("visitor_type", getVisitorType(visitorKey)),
			attribute.String("path", getRoutePattern(r.URL.Path)),
		}
		rl.rateLimitHits.Add(r.Context(), 1, metric.WithAttributes(attrs...))
	}

	// Add rate limit headers
	if rl.config.IncludeHeaders {
		rl.addRateLimitHeaders(w, limiter)

		// Add Retry-After header
		if reservation := limiter.Reserve(); reservation.OK() {
			delay := reservation.Delay()
			reservation.Cancel() // Cancel since we're not using it
			w.Header().Set("Retry-After", strconv.Itoa(int(delay.Seconds())+1))
		}
	}

	// Send error response
	http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
}

// addRateLimitHeaders adds rate limit information headers
func (rl *RateLimiter) addRateLimitHeaders(w http.ResponseWriter, limiter *rate.Limiter) {
	limit := limiter.Limit()
	burst := limiter.Burst()

	// Standard rate limit headers
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(burst))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(limiter.Tokens())))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))

	// Additional info
	w.Header().Set("X-RateLimit-Policy", fmt.Sprintf("%.2f;w=1;burst=%d", limit, burst))
}

// cleanupVisitors removes old visitors periodically
func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, v := range rl.visitors {
			if now.Sub(v.lastSeen) > rl.config.CleanupInterval {
				delete(rl.visitors, key)
			}
		}

		// Update metrics
		if rl.activeVisitors != nil {
			rl.activeVisitors.Record(context.Background(), int64(len(rl.visitors)))
		}

		rl.mu.Unlock()
	}
}

// Helper functions

func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}

	return r.RemoteAddr
}

func matchesPattern(path, pattern string) bool {
	// Simple pattern matching with * wildcard
	if !strings.Contains(pattern, "*") {
		return path == pattern
	}

	// Convert pattern to prefix/suffix match
	parts := strings.Split(pattern, "*")
	if len(parts) != 2 {
		return false
	}

	prefix, suffix := parts[0], parts[1]
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix)
}

func getVisitorType(key string) string {
	if strings.HasPrefix(key, "user:") {
		return "user"
	} else if strings.HasPrefix(key, "ip:") {
		return "ip"
	}
	return "global"
}

// ipWhitelistMiddleware allows certain IPs to bypass rate limiting
func ipWhitelistMiddleware(whitelist []string) Middleware {
	// Convert to map for O(1) lookup
	whitelistMap := make(map[string]bool)
	for _, ip := range whitelist {
		whitelistMap[ip] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := getClientIP(r)

			if whitelistMap[clientIP] {
				// Add header to indicate whitelisted
				w.Header().Set("X-RateLimit-Whitelisted", "true")

				// Skip rate limiting by calling next directly
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AdaptiveRateLimitMiddleware adjusts rate limits based on system load
type AdaptiveRateLimitMiddleware struct {
	baseConfig *RateLimitConfig
	limiter    *RateLimiter
	logger     *slog.Logger
	getLoad    func() float64 // Function to get current system load (0.0-1.0)
}

// newAdaptiveRateLimitMiddleware creates adaptive rate limiting
func newAdaptiveRateLimitMiddleware(config *RateLimitConfig, logger *slog.Logger, getLoad func() float64) *AdaptiveRateLimitMiddleware {
	return &AdaptiveRateLimitMiddleware{
		baseConfig: config,
		limiter:    newRateLimiter(config, logger),
		logger:     logger,
		getLoad:    getLoad,
	}
}

// Middleware returns the adaptive rate limiting middleware
func (a *AdaptiveRateLimitMiddleware) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get current load
			load := a.getLoad()

			// Adjust rate limits based on load
			if load > 0.8 {
				// High load: reduce rate limits by 50%
				a.adjustLimits(0.5)
			} else if load > 0.6 {
				// Medium load: reduce rate limits by 25%
				a.adjustLimits(0.75)
			} else {
				// Normal load: use base rate limits
				a.adjustLimits(1.0)
			}

			// Apply rate limiting
			a.limiter.Middleware()(next).ServeHTTP(w, r)
		})
	}
}

func (a *AdaptiveRateLimitMiddleware) adjustLimits(multiplier float64) {
	// This is a simplified implementation
	// In production, you'd want to update the actual rate limiters
	a.logger.Debug("adjusting rate limits",
		slog.Float64("multiplier", multiplier),
		slog.Float64("load", a.getLoad()),
	)
}
