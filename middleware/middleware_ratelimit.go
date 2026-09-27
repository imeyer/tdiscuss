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

// RateLimitConfig holds rate limiting configuration.
//
// A chain applies two kinds of limit. Middleware runs before authentication
// and counts every request against a per-device bucket, keyed by the client's
// tailnet address, so a flood stops before the WhoIs call and member lookup
// that authenticating each request costs. EndpointMiddleware runs after
// authentication and counts requests to the routes in EndpointLimits against a
// per-member bucket for that route, so a member's limit on, say, posting
// replies is the same however many devices they use.
type RateLimitConfig struct {
	// Per-device limit for every request.
	RequestsPerSecond float64
	Burst             int

	// Per-member limits for particular routes. Keys are ServeMux patterns
	// exactly as registered, such as "POST /thread/{tid}"; a request matches
	// when its r.Pattern equals the key. Each route has its own bucket.
	EndpointLimits map[string]EndpointLimit

	CleanupInterval time.Duration

	IncludeHeaders bool

	Meter        metric.Meter
	MetricPrefix string
}

// EndpointLimit is the rate and burst for one route in EndpointLimits.
type EndpointLimit struct {
	Rate  float64
	Burst int
}

// defaultRateLimitConfig returns sensible defaults. Endpoint limits name the
// application's routes, so the application sets them.
func defaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             20,
		CleanupInterval:   5 * time.Minute,
		IncludeHeaders:    true,
	}
}

// RateLimiter provides flexible rate limiting
type RateLimiter struct {
	config      *RateLimitConfig
	logger      *slog.Logger
	renderError ErrorRenderer
	visitors    map[string]*visitor
	mu          sync.RWMutex

	rateLimitHits  metric.Int64Counter
	activeVisitors metric.Int64Gauge
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newRateLimiter creates a new rate limiter
func newRateLimiter(config *RateLimitConfig, logger *slog.Logger, renderError ErrorRenderer) *RateLimiter {
	rl := &RateLimiter{
		config:      config,
		logger:      logger,
		renderError: renderError,
		visitors:    make(map[string]*visitor),
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

// Middleware limits every request per device, keyed by the client's tailnet
// address. Chains put it before authentication.
func (rl *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + getClientIP(r)
			if !rl.allow(w, r, key, rl.config.RequestsPerSecond, rl.config.Burst) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// EndpointMiddleware limits requests to the routes in EndpointLimits, per
// member and per route. Chains put it after authentication; a request with no
// member falls back to its device.
func (rl *RateLimiter) EndpointMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, ok := rl.config.EndpointLimits[r.Pattern]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			who := "ip:" + getClientIP(r)
			if user, ok := getUser(r.Context()); ok && user != nil {
				who = fmt.Sprintf("user:%d", user.ID)
			}
			if !rl.allow(w, r, who+" "+r.Pattern, limit.Rate, limit.Burst) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow takes a token from the bucket for key, creating the bucket with limit
// and burst the first time key is seen. When the bucket is empty it sends the
// 429 and returns false.
func (rl *RateLimiter) allow(w http.ResponseWriter, r *http.Request, key string, limit float64, burst int) bool {
	v := rl.getVisitor(r.Context(), key, limit, burst)
	if !v.limiter.Allow() {
		rl.handleRateLimitExceeded(w, r, key, v.limiter)
		return false
	}

	if rl.config.IncludeHeaders {
		rl.addRateLimitHeaders(w, v.limiter)
	}
	return true
}

// getVisitor gets or creates the bucket for key
func (rl *RateLimiter) getVisitor(ctx context.Context, key string, limit float64, burst int) *visitor {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		v = &visitor{
			limiter:  rate.NewLimiter(rate.Limit(limit), burst),
			lastSeen: time.Now(),
		}
		rl.visitors[key] = v

		if rl.activeVisitors != nil {
			rl.activeVisitors.Record(ctx, int64(len(rl.visitors)))
		}
	} else {
		v.lastSeen = time.Now()
	}

	return v
}

// handleRateLimitExceeded handles rate limit exceeded responses
func (rl *RateLimiter) handleRateLimitExceeded(w http.ResponseWriter, r *http.Request, visitorKey string, limiter *rate.Limiter) {
	rl.logger.WarnContext(r.Context(), "rate limit exceeded",
		slog.String("visitor", visitorKey),
		slog.String("path", r.URL.Path),
		slog.String("method", r.Method),
		slog.String("remote_addr", r.RemoteAddr),
	)

	if rl.rateLimitHits != nil {
		attrs := []attribute.KeyValue{
			attribute.String("visitor_type", getVisitorType(visitorKey)),
			attribute.String("path", routeLabel(r)),
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

	rl.renderError(w, r, http.StatusTooManyRequests,
		"You're sending requests too quickly. Wait a few seconds and try again.")
}

// addRateLimitHeaders adds rate limit information headers
func (rl *RateLimiter) addRateLimitHeaders(w http.ResponseWriter, limiter *rate.Limiter) {
	limit := limiter.Limit()
	burst := limiter.Burst()

	// Spelled as Go sends them: Header.Set canonicalizes every name, so
	// "X-RateLimit-Limit" would go out as X-Ratelimit-Limit anyway.
	w.Header().Set("X-Ratelimit-Limit", strconv.Itoa(burst))
	w.Header().Set("X-Ratelimit-Remaining", strconv.Itoa(int(limiter.Tokens())))
	w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))

	w.Header().Set("X-Ratelimit-Policy", fmt.Sprintf("%.2f;w=1;burst=%d", limit, burst))
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

func getVisitorType(key string) string {
	if strings.HasPrefix(key, "user:") {
		return "user"
	} else if strings.HasPrefix(key, "ip:") {
		return "ip"
	}
	return "global"
}
