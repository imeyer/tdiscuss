package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// MiddlewareSetup configures all middleware for the application
type MiddlewareSetup struct {
	// Core services
	Logger    *slog.Logger
	Tracer    trace.Tracer
	Meter     metric.Meter
	Telemetry *TelemetryConfig

	// Auth
	AuthProvider AuthProvider

	// Configuration
	SecurityConfig      *SecurityConfig
	RateLimitConfig     *RateLimitConfig
	ObservabilityConfig *ObservabilityConfig

	// Feature flags
	EnableAuth      bool
	EnableRateLimit bool
	EnableMetrics   bool
	EnableTracing   bool
	EnableCSRF      bool
}

// NewMiddlewareSetup creates a new middleware setup with defaults
func NewMiddlewareSetup(logger *slog.Logger, telemetry *TelemetryConfig, authProvider AuthProvider) *MiddlewareSetup {
	// Type assert the interfaces to the actual OpenTelemetry types
	tracer, _ := telemetry.Tracer.(trace.Tracer)
	meter, _ := telemetry.Meter.(metric.Meter)

	return &MiddlewareSetup{
		Logger:       logger,
		Tracer:       tracer,
		Meter:        meter,
		Telemetry:    telemetry,
		AuthProvider: authProvider,

		// Default configs
		SecurityConfig:  defaultSecurityConfig(),
		RateLimitConfig: defaultRateLimitConfig(),

		// Enable features by default
		EnableAuth:      true,
		EnableRateLimit: true,
		EnableMetrics:   true,
		EnableTracing:   true,
		EnableCSRF:      true,
	}
}

// CreatePublicChain creates middleware chain for public endpoints
func (ms *MiddlewareSetup) CreatePublicChain() *Chain {
	middlewares := []Middleware{
		// Always start with request context
		requestContextMiddleware(),
	}

	// Add observability
	if ms.EnableMetrics || ms.EnableTracing {
		middlewares = append(middlewares, ms.createObservabilityMiddleware())
	}

	// Add logging
	middlewares = append(middlewares, loggingMiddleware(ms.Logger))

	// Add security headers
	middlewares = append(middlewares, securityHeadersMiddleware(ms.SecurityConfig))

	// Add request size limiting
	middlewares = append(middlewares, requestSizeLimitMiddleware(1024*1024)) // 1MB

	// Add rate limiting
	if ms.EnableRateLimit {
		rl := newRateLimiter(ms.RateLimitConfig, ms.Logger)
		middlewares = append(middlewares, rl.Middleware())
	}

	// CSRF protection is now handled by CrossOriginProtection middleware in authenticated chains

	return newChain(middlewares...)
}

// CreateAuthenticatedChain creates middleware chain for authenticated endpoints
func (ms *MiddlewareSetup) CreateAuthenticatedChain() *Chain {
	// Start with public chain
	chain := ms.CreatePublicChain()

	// Add authentication
	if ms.EnableAuth {
		chain = chain.Append(
			authMiddleware(ms.AuthProvider, ms.Tracer),
			userEnrichmentMiddleware(),
		)
	}

	// Add CSRF protection for state-changing operations
	if ms.EnableCSRF {
		chain = chain.Append(
			when(hasMethod("POST", "PUT", "PATCH", "DELETE"),
				csrfProtectionMiddleware(ms.SecurityConfig)),
		)
	}

	return chain
}

// CreateAdminChain creates middleware chain for admin endpoints
func (ms *MiddlewareSetup) CreateAdminChain() *Chain {
	// Start with authenticated chain
	chain := ms.CreateAuthenticatedChain()

	// Add admin requirement
	chain = chain.Append(requireAdminMiddleware())

	// Add extra logging for admin actions
	chain = chain.Append(adminAuditMiddleware(ms.Logger))

	return chain
}

// CreateAPIChain creates middleware chain for API endpoints
func (ms *MiddlewareSetup) CreateAPIChain() *Chain {
	middlewares := []Middleware{
		// Always start with request context
		requestContextMiddleware(),
	}

	// Add observability
	if ms.EnableMetrics || ms.EnableTracing {
		middlewares = append(middlewares, ms.createObservabilityMiddleware())
	}

	// Add API-specific logging
	middlewares = append(middlewares, apiLoggingMiddleware(ms.Logger))

	// Add security headers (with API-specific CSP)
	apiSecurityConfig := *ms.SecurityConfig
	apiSecurityConfig.CSPDirectives = map[string]string{
		"default-src":     "'none'",
		"frame-ancestors": "'none'",
	}
	middlewares = append(middlewares, securityHeadersMiddleware(&apiSecurityConfig))

	// Add request size limiting (larger for API)
	middlewares = append(middlewares, requestSizeLimitMiddleware(10*1024*1024)) // 10MB

	// Add rate limiting with API-specific limits
	if ms.EnableRateLimit {
		apiRateLimitConfig := *ms.RateLimitConfig
		apiRateLimitConfig.RequestsPerSecond = 100 // Higher rate for API
		apiRateLimitConfig.Burst = 200

		rl := newRateLimiter(&apiRateLimitConfig, ms.Logger)
		middlewares = append(middlewares, rl.Middleware())
	}

	// Add API authentication (could be different from web auth)
	if ms.EnableAuth {
		middlewares = append(middlewares,
			authMiddleware(ms.AuthProvider, ms.Tracer),
			userEnrichmentMiddleware(),
		)
	}

	// Add JSON error handling
	middlewares = append(middlewares, jsonErrorMiddleware())

	return newChain(middlewares...)
}

// createObservabilityMiddleware creates the observability middleware
func (ms *MiddlewareSetup) createObservabilityMiddleware() Middleware {
	// Type assert the metrics to the actual OpenTelemetry types
	requestCounter, _ := ms.Telemetry.Metrics.RequestCounter.(metric.Int64Counter)
	requestDuration, _ := ms.Telemetry.Metrics.RequestDuration.(metric.Float64Histogram)
	errorCounter, _ := ms.Telemetry.Metrics.ErrorCounter.(metric.Int64Counter)

	config := &ObservabilityConfig{
		ServiceName:     "tdiscuss",
		Logger:          ms.Logger,
		Tracer:          ms.Tracer,
		Meter:           ms.Meter,
		RequestCounter:  requestCounter,
		RequestDuration: requestDuration,
		ErrorCounter:    errorCounter,
		SampleRate:      1.0, // TODO: Get from config
	}

	// Add additional metrics if available
	if ms.Meter != nil {
		config.RequestSize, _ = ms.Meter.Int64Histogram(
			"http.server.request.size",
			metric.WithDescription("Size of HTTP request bodies"),
			metric.WithUnit("By"),
		)

		config.ResponseSize, _ = ms.Meter.Int64Histogram(
			"http.server.response.size",
			metric.WithDescription("Size of HTTP response bodies"),
			metric.WithUnit("By"),
		)

		config.ActiveRequests, _ = ms.Meter.Int64UpDownCounter(
			"http.server.active_requests",
			metric.WithDescription("Number of active HTTP requests"),
			metric.WithUnit("{request}"),
		)
	}

	return newObservabilityMiddleware(config)
}

// SetupRoutes configures routes with appropriate middleware chains
func (ms *MiddlewareSetup) SetupRoutes(svc DiscussService) http.Handler {
	mux := http.NewServeMux()

	// Create middleware chains
	publicChain := ms.CreatePublicChain()
	authChain := ms.CreateAuthenticatedChain()
	adminChain := ms.CreateAdminChain()

	// Public routes
	mux.Handle("/", publicChain.ThenFunc(svc.ListThreads))
	mux.Handle("/thread/", publicChain.ThenFunc(svc.ListThreadPosts))
	mux.Handle("/member/", publicChain.ThenFunc(svc.ListMember))

	// Authenticated routes
	mux.Handle("/thread/new", authChain.ThenFunc(svc.NewThread))
	mux.Handle("/thread/create", authChain.ThenFunc(svc.CreateThread))
	mux.Handle("/member/edit", authChain.ThenFunc(svc.EditMemberProfile))

	// Routes with conditional authentication
	mux.Handle("/thread/edit", authChain.ThenFunc(svc.EditThread))
	mux.Handle("/thread/post/edit", authChain.ThenFunc(svc.EditThreadPost))
	mux.Handle("/thread/reply", authChain.ThenFunc(svc.CreateThreadPost))

	// Admin routes
	mux.Handle("/admin", adminChain.ThenFunc(svc.Admin))

	// Static files (with caching headers)
	staticChain := publicChain.Append(staticFileMiddleware())
	mux.Handle("/static/", staticChain.ThenFunc(svc.ServeStatic))

	// Health check (minimal middleware)
	healthChain := newChain(
		requestContextMiddleware(),
		loggingMiddleware(ms.Logger),
	)
	mux.Handle("/health", healthChain.ThenFunc(svc.HealthCheck))

	// Metrics endpoint (if Prometheus is enabled)
	if ms.EnableMetrics {
		mux.Handle("/metrics", http.HandlerFunc(svc.MetricsHandler))
	}

	return mux
}

// Additional middleware implementations

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

// apiLoggingMiddleware provides structured logging for API requests
func apiLoggingMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Similar to loggingMiddleware but with API-specific fields
			rc := getOrCreateRequestContext(r.Context())
			wrapped := newResponseWriter(w)

			apiLogger := logger.With(
				slog.String("api_version", getAPIVersion(r.URL.Path)),
				slog.String("request_id", rc.RequestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			// Store logger in context
			ctx := context.WithValue(r.Context(), contextKey("logger"), apiLogger)

			next.ServeHTTP(wrapped, r.WithContext(ctx))
		})
	}
}

// jsonErrorMiddleware handles errors in JSON format for API endpoints
func jsonErrorMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the ResponseWriter to intercept errors
			wrapped := &jsonErrorResponseWriter{
				ResponseWriter: w,
				request:        r,
			}

			next.ServeHTTP(wrapped, r)
		})
	}
}

// staticFileMiddleware adds caching headers for static files
func staticFileMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add cache headers for static assets
			w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day

			// Add CORS for fonts and images
			if strings.HasSuffix(r.URL.Path, ".woff") ||
				strings.HasSuffix(r.URL.Path, ".woff2") ||
				strings.HasSuffix(r.URL.Path, ".ttf") {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Helper functions

func getAPIVersion(path string) string {
	if strings.HasPrefix(path, "/api/v1/") {
		return "v1"
	} else if strings.HasPrefix(path, "/api/v2/") {
		return "v2"
	}
	return "unknown"
}

// jsonErrorResponseWriter wraps ResponseWriter to handle JSON errors
type jsonErrorResponseWriter struct {
	http.ResponseWriter
	request *http.Request
	wrote   bool
}

func (w *jsonErrorResponseWriter) WriteHeader(status int) {
	if !w.wrote && status >= 400 {
		// Intercept error responses and convert to JSON
		w.Header().Set("Content-Type", "application/json")
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *jsonErrorResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
