package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
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

	EnableUserRateLimit bool
	UserRateMultiplier  float64 // Multiplier for authenticated users

	EnableIPRateLimit bool
	CleanupInterval   time.Duration

	IncludeHeaders bool

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

	go rl.cleanupVisitors()

	return rl
}

// Middleware returns the rate limiting middleware
func (rl *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			visitorKey := rl.getVisitorKey(r)

			limit, burst := rl.getLimitsForPath(r.URL.Path)

			v := rl.getVisitor(visitorKey, limit, burst)

			if !v.limiter.Allow() {
				rl.handleRateLimitExceeded(w, r, v.limiter)
				return
			}

			if rl.config.IncludeHeaders {
				rl.addRateLimitHeaders(w, v.limiter)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getVisitorKey determines the key for rate limiting
func (rl *RateLimiter) getVisitorKey(r *http.Request) string {
	if rl.config.EnableUserRateLimit {
		if user, ok := getUser(r.Context()); ok && user != nil {
			return fmt.Sprintf("user:%d", user.ID)
		}
	}

	if rl.config.EnableIPRateLimit {
		return "ip:" + getClientIP(r)
	}

	// If neither is enabled, use a global key (not recommended)
	return "global"
}

// getLimitsForPath returns rate limits for a specific path
func (rl *RateLimiter) getLimitsForPath(path string) (float64, int) {
	for pattern, limit := range rl.config.EndpointLimits {
		if matchesPattern(path, pattern) {
			return limit.Rate, limit.Burst
		}
	}

	return rl.config.RequestsPerSecond, rl.config.Burst
}

// getVisitor gets or creates a visitor
func (rl *RateLimiter) getVisitor(key string, limit float64, burst int) *visitor {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		if strings.HasPrefix(key, "user:") && rl.config.UserRateMultiplier > 0 {
			limit *= rl.config.UserRateMultiplier
			burst = int(float64(burst) * rl.config.UserRateMultiplier)
		}

		v = &visitor{
			limiter:  rate.NewLimiter(rate.Limit(limit), burst),
			lastSeen: time.Now(),
		}
		rl.visitors[key] = v

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
	visitorKey := rl.getVisitorKey(r)
	rl.logger.WarnContext(r.Context(), "rate limit exceeded",
		slog.String("visitor", visitorKey),
		slog.String("path", r.URL.Path),
		slog.String("method", r.Method),
		slog.String("remote_addr", r.RemoteAddr),
	)

	if rl.rateLimitHits != nil {
		attrs := []attribute.KeyValue{
			attribute.String("visitor_type", getVisitorType(visitorKey)),
			attribute.String("path", getRoutePattern(r.URL.Path)),
		}
		rl.rateLimitHits.Add(r.Context(), 1, metric.WithAttributes(attrs...))
	}

	if rl.config.IncludeHeaders {
		rl.addRateLimitHeaders(w, limiter)

		if reservation := limiter.Reserve(); reservation.OK() {
			delay := reservation.Delay()
			reservation.Cancel() // Cancel since we're not using it
			w.Header().Set("Retry-After", strconv.Itoa(int(delay.Seconds())+1))
		}
	}

	http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
}

// addRateLimitHeaders adds rate limit information headers
func (rl *RateLimiter) addRateLimitHeaders(w http.ResponseWriter, limiter *rate.Limiter) {
	limit := limiter.Limit()
	burst := limiter.Burst()

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(burst))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(limiter.Tokens())))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))

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

		if rl.activeVisitors != nil {
			rl.activeVisitors.Record(context.Background(), int64(len(rl.visitors)))
		}

		rl.mu.Unlock()
	}
}

// getClientIP returns the address of the peer that made the request.
//
// Requests only ever arrive over a tsnet listener, so r.RemoteAddr is the
// peer's WireGuard-authenticated tailnet address and there is no proxy in
// front of us. X-Forwarded-For and X-Real-IP are therefore attacker-controlled
// and must never be consulted here: a peer that could set them would choose
// its own rate-limit bucket and forge the address we log.
func getClientIP(r *http.Request) string {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().String()
	}
	// Not host:port. Accept a bare address (some tests set RemoteAddr this
	// way) rather than string-mangling a value we failed to parse.
	if addr, err := netip.ParseAddr(r.RemoteAddr); err == nil {
		return addr.String()
	}
	return r.RemoteAddr
}

func matchesPattern(path, pattern string) bool {
	// Simple pattern matching with * wildcard
	if !strings.Contains(pattern, "*") {
		return path == pattern
	}

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
