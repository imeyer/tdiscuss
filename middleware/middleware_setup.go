package middleware

import (
	"log/slog"
	"maps"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// MiddlewareSetup builds the application's middleware chains. Its settings
// are fixed when NewMiddlewareSetup returns, so every chain built from one
// setup, and the rate limiter those chains share, see the same configuration.
type MiddlewareSetup struct {
	logger    *slog.Logger
	tracer    trace.Tracer
	meter     metric.Meter
	telemetry *TelemetryConfig

	authProvider AuthProvider
	renderError  ErrorRenderer

	securityConfig  *SecurityConfig
	rateLimitConfig *RateLimitConfig

	// rateLimiter is shared by the public, authenticated and admin chains, so
	// a device or member has one set of buckets whichever chain a route is on.
	rateLimiter *RateLimiter
}

// SetupOptions configures NewMiddlewareSetup.
type SetupOptions struct {
	Logger *slog.Logger
	// Telemetry must carry a trace.Tracer: the observability middleware
	// starts a span for every request.
	Telemetry    *TelemetryConfig
	AuthProvider AuthProvider

	// ErrorRenderer writes the responses the chains send when they reject a
	// request. Nil means plain text.
	ErrorRenderer ErrorRenderer

	// RateLimit replaces the default rate limits. It is copied, so changing
	// it after NewMiddlewareSetup returns has no effect. A zero rate, burst
	// or cleanup interval takes the default, and a nil Meter means the
	// telemetry meter.
	RateLimit *RateLimitConfig
}

// NewMiddlewareSetup creates a middleware setup from opts.
func NewMiddlewareSetup(opts SetupOptions) *MiddlewareSetup {
	tracer, _ := opts.Telemetry.Tracer.(trace.Tracer)
	meter, _ := opts.Telemetry.Meter.(metric.Meter)

	renderError := opts.ErrorRenderer
	if renderError == nil {
		renderError = plainTextErrors
	}

	rateLimit := defaultRateLimitConfig()
	if opts.RateLimit != nil {
		c := *opts.RateLimit
		c.EndpointLimits = maps.Clone(opts.RateLimit.EndpointLimits)
		// A zero rate or burst would reject every request, so a config that
		// sets only EndpointLimits still gets the default per-device limit.
		if c.RequestsPerSecond <= 0 {
			c.RequestsPerSecond = rateLimit.RequestsPerSecond
		}
		if c.Burst <= 0 {
			c.Burst = rateLimit.Burst
		}
		if c.CleanupInterval <= 0 {
			c.CleanupInterval = rateLimit.CleanupInterval
		}
		rateLimit = &c
	}
	if rateLimit.Meter == nil {
		rateLimit.Meter = meter
	}

	return &MiddlewareSetup{
		logger:       opts.Logger,
		tracer:       tracer,
		meter:        meter,
		telemetry:    opts.Telemetry,
		authProvider: opts.AuthProvider,
		renderError:  renderError,

		securityConfig:  defaultSecurityConfig(),
		rateLimitConfig: rateLimit,
		rateLimiter:     newRateLimiter(rateLimit, opts.Logger, renderError),
	}
}

// CreatePublicChain creates middleware chain for public endpoints
func (ms *MiddlewareSetup) CreatePublicChain() *Chain {
	return newChain(
		requestContextMiddleware(),
		ms.createObservabilityMiddleware(),
		loggingMiddleware(ms.logger),
		securityHeadersMiddleware(ms.securityConfig),
		requestSizeLimitMiddleware(1024*1024, ms.renderError), // 1MB
		ms.rateLimiter.Middleware(),
		// CSRF protection is handled by CrossOriginProtection middleware in authenticated chains
	)
}

// CreateAuthenticatedChain creates middleware chain for authenticated endpoints
func (ms *MiddlewareSetup) CreateAuthenticatedChain() *Chain {
	return ms.withWriteLimits(ms.signedInChain())
}

// CreateAdminChain creates middleware chain for admin endpoints
func (ms *MiddlewareSetup) CreateAdminChain() *Chain {
	chain := ms.signedInChain()

	chain = chain.Append(requireAdminMiddleware(ms.renderError))

	// After the admin check, so a non-admin is told they are not an admin
	// rather than that they are sending too many requests.
	chain = ms.withWriteLimits(chain)

	chain = chain.Append(adminAuditMiddleware(ms.logger))

	return chain
}

// signedInChain is the public chain plus authentication and the CSRF check.
func (ms *MiddlewareSetup) signedInChain() *Chain {
	return ms.CreatePublicChain().Append(
		authMiddleware(ms.authProvider, ms.tracer, ms.renderError),
		userEnrichmentMiddleware(),
		when(hasMethod("POST", "PUT", "PATCH", "DELETE"),
			csrfProtectionMiddleware(ms.securityConfig, ms.renderError)),
	)
}

// withWriteLimits appends the per-member write limits. They run after
// authentication, so the limits are per member, and after the CSRF check, so
// a forged cross-site post cannot spend a member's allowance.
func (ms *MiddlewareSetup) withWriteLimits(chain *Chain) *Chain {
	return chain.Append(ms.rateLimiter.EndpointMiddleware())
}

// createObservabilityMiddleware creates the observability middleware
func (ms *MiddlewareSetup) createObservabilityMiddleware() Middleware {
	requestCounter, _ := ms.telemetry.Metrics.RequestCounter.(metric.Int64Counter)
	requestDuration, _ := ms.telemetry.Metrics.RequestDuration.(metric.Float64Histogram)
	errorCounter, _ := ms.telemetry.Metrics.ErrorCounter.(metric.Int64Counter)

	config := &ObservabilityConfig{
		ServiceName:     "tdiscuss",
		Logger:          ms.logger,
		Tracer:          ms.tracer,
		Meter:           ms.meter,
		RequestCounter:  requestCounter,
		RequestDuration: requestDuration,
		ErrorCounter:    errorCounter,
		SampleRate:      1.0, // TODO: Get from config
	}

	if ms.meter != nil {
		config.RequestSize, _ = ms.meter.Int64Histogram(
			"http.server.request.size",
			metric.WithDescription("Size of HTTP request bodies"),
			metric.WithUnit("By"),
		)

		config.ResponseSize, _ = ms.meter.Int64Histogram(
			"http.server.response.size",
			metric.WithDescription("Size of HTTP response bodies"),
			metric.WithUnit("By"),
		)

		config.ActiveRequests, _ = ms.meter.Int64UpDownCounter(
			"http.server.active_requests",
			metric.WithDescription("Number of active HTTP requests"),
			metric.WithUnit("{request}"),
		)
	}

	return newObservabilityMiddleware(config)
}

// adminAuditMiddleware logs all admin actions
func adminAuditMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := getUser(r.Context())

			logger.InfoContext(r.Context(), "admin_action",
				slog.String("action", r.Method+" "+r.URL.Path),
				slog.Int64("admin_id", user.ID),
				slog.String("query", r.URL.RawQuery),
				slog.String("remote_addr", r.RemoteAddr),
			)

			wrapped := newResponseWriter(w)
			next.ServeHTTP(wrapped, r)

			logger.InfoContext(r.Context(), "admin_action_completed",
				slog.String("action", r.Method+" "+r.URL.Path),
				slog.Int64("admin_id", user.ID),
				slog.Int("status", wrapped.Status()),
			)
		})
	}
}

// staticFileMiddleware adds caching headers for static files
func staticFileMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day

			if strings.HasSuffix(r.URL.Path, ".woff") ||
				strings.HasSuffix(r.URL.Path, ".woff2") ||
				strings.HasSuffix(r.URL.Path, ".ttf") {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}

			next.ServeHTTP(w, r)
		})
	}
}
