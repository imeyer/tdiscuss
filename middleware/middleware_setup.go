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
	Logger    *slog.Logger
	Tracer    trace.Tracer
	Meter     metric.Meter
	Telemetry *TelemetryConfig

	AuthProvider AuthProvider

	SecurityConfig      *SecurityConfig
	RateLimitConfig     *RateLimitConfig
	ObservabilityConfig *ObservabilityConfig

	EnableAuth      bool
	EnableRateLimit bool
	EnableMetrics   bool
	EnableTracing   bool
	EnableCSRF      bool
}

// NewMiddlewareSetup creates a new middleware setup with defaults
func NewMiddlewareSetup(logger *slog.Logger, telemetry *TelemetryConfig, authProvider AuthProvider) *MiddlewareSetup {
	tracer, _ := telemetry.Tracer.(trace.Tracer)
	meter, _ := telemetry.Meter.(metric.Meter)

	return &MiddlewareSetup{
		Logger:       logger,
		Tracer:       tracer,
		Meter:        meter,
		Telemetry:    telemetry,
		AuthProvider: authProvider,

		SecurityConfig:  defaultSecurityConfig(),
		RateLimitConfig: defaultRateLimitConfig(),

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
		requestContextMiddleware(),
	}

	if ms.EnableMetrics || ms.EnableTracing {
		middlewares = append(middlewares, ms.createObservabilityMiddleware())
	}

	middlewares = append(middlewares, loggingMiddleware(ms.Logger))

	middlewares = append(middlewares, securityHeadersMiddleware(ms.SecurityConfig))

	middlewares = append(middlewares, requestSizeLimitMiddleware(1024*1024)) // 1MB

	if ms.EnableRateLimit {
		rl := newRateLimiter(ms.RateLimitConfig, ms.Logger)
		middlewares = append(middlewares, rl.Middleware())
	}

	// CSRF protection is now handled by CrossOriginProtection middleware in authenticated chains

	return newChain(middlewares...)
}

// CreateAuthenticatedChain creates middleware chain for authenticated endpoints
func (ms *MiddlewareSetup) CreateAuthenticatedChain() *Chain {
	chain := ms.CreatePublicChain()

	if ms.EnableAuth {
		chain = chain.Append(
			authMiddleware(ms.AuthProvider, ms.Tracer),
			userEnrichmentMiddleware(),
		)
	}

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
	chain := ms.CreateAuthenticatedChain()

	chain = chain.Append(requireAdminMiddleware())

	chain = chain.Append(adminAuditMiddleware(ms.Logger))

	return chain
}

// CreateAPIChain creates middleware chain for API endpoints
func (ms *MiddlewareSetup) CreateAPIChain() *Chain {
	middlewares := []Middleware{
		requestContextMiddleware(),
	}

	if ms.EnableMetrics || ms.EnableTracing {
		middlewares = append(middlewares, ms.createObservabilityMiddleware())
	}

	middlewares = append(middlewares, apiLoggingMiddleware(ms.Logger))

	apiSecurityConfig := *ms.SecurityConfig
	apiSecurityConfig.CSPDirectives = map[string]string{
		"default-src":     "'none'",
		"frame-ancestors": "'none'",
	}
	middlewares = append(middlewares, securityHeadersMiddleware(&apiSecurityConfig))

	middlewares = append(middlewares, requestSizeLimitMiddleware(10*1024*1024)) // 10MB

	if ms.EnableRateLimit {
		apiRateLimitConfig := *ms.RateLimitConfig
		apiRateLimitConfig.RequestsPerSecond = 100
		apiRateLimitConfig.Burst = 200

		rl := newRateLimiter(&apiRateLimitConfig, ms.Logger)
		middlewares = append(middlewares, rl.Middleware())
	}

	if ms.EnableAuth {
		middlewares = append(middlewares,
			authMiddleware(ms.AuthProvider, ms.Tracer),
			userEnrichmentMiddleware(),
		)
	}

	middlewares = append(middlewares, jsonErrorMiddleware())

	return newChain(middlewares...)
}

// createObservabilityMiddleware creates the observability middleware
func (ms *MiddlewareSetup) createObservabilityMiddleware() Middleware {
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

	publicChain := ms.CreatePublicChain()
	authChain := ms.CreateAuthenticatedChain()
	adminChain := ms.CreateAdminChain()

	mux.Handle("/", publicChain.ThenFunc(svc.ListThreads))
	mux.Handle("/thread/", publicChain.ThenFunc(svc.ListThreadPosts))
	mux.Handle("/member/", publicChain.ThenFunc(svc.ListMember))

	mux.Handle("/thread/new", authChain.ThenFunc(svc.NewThread))
	mux.Handle("/thread/create", authChain.ThenFunc(svc.CreateThread))
	mux.Handle("/member/edit", authChain.ThenFunc(svc.EditMemberProfile))

	mux.Handle("/thread/edit", authChain.ThenFunc(svc.EditThread))
	mux.Handle("/thread/post/edit", authChain.ThenFunc(svc.EditThreadPost))
	mux.Handle("/thread/reply", authChain.ThenFunc(svc.CreateThreadPost))

	mux.Handle("/admin", adminChain.ThenFunc(svc.Admin))

	staticChain := publicChain.Append(staticFileMiddleware())
	mux.Handle("/static/", staticChain.ThenFunc(svc.ServeStatic))

	healthChain := newChain(
		requestContextMiddleware(),
		loggingMiddleware(ms.Logger),
	)
	mux.Handle("/health", healthChain.ThenFunc(svc.HealthCheck))

	if ms.EnableMetrics {
		mux.Handle("/metrics", http.HandlerFunc(svc.MetricsHandler))
	}

	return mux
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

// apiLoggingMiddleware provides structured logging for API requests
func apiLoggingMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := getOrCreateRequestContext(r.Context())
			wrapped := newResponseWriter(w)

			apiLogger := logger.With(
				slog.String("api_version", getAPIVersion(r.URL.Path)),
				slog.String("request_id", rc.RequestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			ctx := context.WithValue(r.Context(), contextKey("logger"), apiLogger)

			next.ServeHTTP(wrapped, r.WithContext(ctx))
		})
	}
}

// jsonErrorMiddleware handles errors in JSON format for API endpoints
func jsonErrorMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
