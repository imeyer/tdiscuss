package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// writeRateLimits are the per-member limits on the routes that write. Each key
// must be a pattern registered in setupRoutes, spelled exactly the same: the
// limiter matches on r.Pattern, so a key that names no route limits nothing.
// Reads are only subject to the per-device limit.
func writeRateLimits(devMode bool) map[string]middleware.EndpointLimit {
	adminRate := 0.2 // 1 admin action per 5 seconds
	if devMode {
		adminRate = 10.0
	}

	return map[string]middleware.EndpointLimit{
		"POST /thread/new":              {Rate: 0.5, Burst: 2}, // 1 thread per 2 seconds
		"POST /thread/{tid}":            {Rate: 2, Burst: 5},   // 2 replies per second
		"POST /thread/{tid}/edit":       {Rate: 1, Burst: 3},   // 1 thread edit per second
		"POST /thread/{tid}/{pid}/edit": {Rate: 1, Burst: 3},   // 1 post edit per second
		"POST /member/edit":             {Rate: 0.5, Burst: 2}, // 1 profile update per 2 seconds
		"POST /admin":                   {Rate: adminRate, Burst: 1},
	}
}

// rateLimits is the production rate limit configuration: the middleware's
// per-device default plus the per-member write limits.
func rateLimits(devMode bool) middleware.RateLimitConfig {
	limits := *middleware.DefaultRateLimitConfig()
	limits.EndpointLimits = writeRateLimits(devMode)
	return limits
}

// SetupRoutes configures all HTTP routes with their appropriate middleware chains
func SetupRoutes(dsvc *DiscussService, staticFS embed.FS) http.Handler {
	isDevMode := dsvc.logger.Enabled(context.Background(), slog.LevelDebug)
	return setupRoutes(dsvc, staticFS, rateLimits(isDevMode))
}

// setupRoutes is SetupRoutes with the rate limits as a parameter, so tests can
// use limits that do not refill while they run.
func setupRoutes(dsvc *DiscussService, staticFS embed.FS, limits middleware.RateLimitConfig) http.Handler {
	// Create auth provider. dsvc.tailClient satisfies middleware.TailscaleClient
	// directly, so the full WhoIs response reaches the auth code intact.
	querierAdapter := NewQuerierAdapter(dsvc.queries)
	authProvider := middleware.NewTailscaleAuthProvider(
		dsvc.tailClient,
		querierAdapter,
		dsvc.logger,
		middleware.TailscaleAuthConfig{
			AllowSharedNodes: *allowSharedNodes,
		},
	)

	ms := middleware.NewMiddlewareSetup(middleware.SetupOptions{
		Logger:       dsvc.logger,
		Telemetry:    ConvertTelemetryConfig(dsvc.telemetry),
		AuthProvider: authProvider,
		// Requests the middleware rejects get the board's error page, not
		// plain text.
		ErrorRenderer: dsvc.renderErrorMessage,
		RateLimit:     &limits,
	})

	mux := http.NewServeMux()

	boardDataMiddleware := middleware.BoardDataMiddleware(querierAdapter)

	// All routes require Tailscale authentication. Every route names its
	// method, and a pattern with GET also matches HEAD, so a handler only ever
	// sees the methods its routes list. The catch-all below answers the rest.
	authChain := ms.CreateAuthenticatedChain().Append(boardDataMiddleware)
	adminChain := ms.CreateAdminChain().Append(boardDataMiddleware)

	mux.Handle("GET /{$}", authChain.ThenFunc(dsvc.ListThreads))
	mux.Handle("GET /thread/{tid}", authChain.ThenFunc(dsvc.ListThreadPosts))
	mux.Handle("GET /member/{mid}", authChain.ThenFunc(dsvc.ListMember))
	mux.Handle("GET /thread/new", authChain.ThenFunc(dsvc.NewThread))
	mux.Handle("POST /thread/new", authChain.ThenFunc(dsvc.CreateThread))
	mux.Handle("GET /thread/{tid}/edit", authChain.ThenFunc(dsvc.EditThreadGET))
	mux.Handle("POST /thread/{tid}/edit", authChain.ThenFunc(dsvc.EditThreadPOST))
	mux.Handle("GET /thread/{tid}/{pid}/edit", authChain.ThenFunc(dsvc.EditThreadPostGET))
	mux.Handle("POST /thread/{tid}/{pid}/edit", authChain.ThenFunc(dsvc.EditThreadPostPOST))
	mux.Handle("POST /thread/{tid}", authChain.ThenFunc(dsvc.CreateThreadPost))
	mux.Handle("GET /member/edit", authChain.ThenFunc(dsvc.EditMemberProfileGET))
	mux.Handle("POST /member/edit", authChain.ThenFunc(dsvc.EditMemberProfilePOST))
	mux.Handle("GET /formatting", authChain.ThenFunc(dsvc.FormattingGuide))

	mux.Handle("GET /admin", adminChain.ThenFunc(dsvc.AdminGET))
	mux.Handle("POST /admin", adminChain.ThenFunc(dsvc.AdminPOST))

	// Every request no route above matches. Without it, the mux answers those
	// itself with a plain-text "404 page not found" or 405. It goes through
	// authChain so the page has the member's menu and the board title.
	mux.Handle("/", authChain.ThenFunc(noRoute(dsvc, mux)))

	// ServeContent sets Content-Type from the file extension and answers HEAD,
	// conditional and range requests.
	serveStatic := func(w http.ResponseWriter, r *http.Request, embedPath string) {
		data, err := staticFS.ReadFile(embedPath)
		if err != nil {
			dsvc.renderError(w, r, http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, embedPath, time.Time{}, bytes.NewReader(data))
	}

	// Static files don't need authentication - they're public assets.
	// URL /static/theme.js is embed path static/theme.js.
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		serveStatic(w, r, r.URL.Path[1:])
	})

	// header.html points browsers at /static/favicon.ico, but some clients
	// request /favicon.ico regardless. Serve it here so those requests do not
	// run the catch-all's authenticated chain.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		serveStatic(w, r, "static/favicon.ico")
	})

	healthChain := middleware.NewChain(
		middleware.RequestContextMiddleware(),
		middleware.LoggingMiddleware(dsvc.logger),
	)
	mux.Handle("GET /health", healthChain.ThenFunc(dsvc.HealthCheck))

	// Note: the metrics endpoint is not on this mux. It is served on its own
	// tailnet listener by SetupDebugRoutes, so that tailnet ACLs decide who
	// can read it.

	// Add global panic recovery as the outermost middleware
	globalChain := middleware.NewChain(
		RecoveryMiddleware(dsvc.logger),
	)

	return globalChain.Then(mux)
}

// noRoute answers requests that match no route. A path that has a route for
// another method gets a 405 naming the methods it allows; anything else gets
// a 404. This is the one place that handles a wrong method: handlers never see
// one.
func noRoute(dsvc *DiscussService, mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if allow := allowedMethods(mux, r); len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			dsvc.renderError(w, r, http.StatusMethodNotAllowed)
			return
		}
		dsvc.renderError(w, r, http.StatusNotFound)
	}
}

// allowedMethods lists the methods that have a route for r's path. A GET route
// also serves HEAD, so the mux reports HEAD wherever it reports GET.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allow []string
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
	} {
		probe := r.WithContext(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, method)
		}
	}
	return allow
}

// SetupDebugRoutes configures the handler for the debug listener, which serves
// the Prometheus metrics endpoint.
//
// This mux is served on its own tailnet port (debugPort) rather than alongside
// the application routes. Access is therefore governed by the tailnet policy
// file, which is the only place that can actually make the decision: an
// in-process IP allowlist cannot, because on a tsnet listener every peer is a
// tailnet address and none of them are loopback.
func SetupDebugRoutes(dsvc *DiscussService) http.Handler {
	mux := http.NewServeMux()

	debugChain := middleware.NewChain(
		middleware.RequestContextMiddleware(),
		middleware.LoggingMiddleware(dsvc.logger),
	)

	mux.Handle("GET /_/metrics", debugChain.Then(promhttp.Handler()))
	mux.Handle("GET /health", debugChain.ThenFunc(dsvc.HealthCheck))

	globalChain := middleware.NewChain(
		RecoveryMiddleware(dsvc.logger),
	)

	return globalChain.Then(mux)
}

// RecoveryMiddleware recovers from panics and logs them with enhanced error reporting
func RecoveryMiddleware(logger *slog.Logger) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := &recoveryResponseWriter{ResponseWriter: w}

			defer func() {
				if err := recover(); err != nil {
					errorID := generateErrorID()

					requestInfo := []slog.Attr{
						slog.String("error_id", errorID),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("remote_addr", r.RemoteAddr),
						slog.String("user_agent", r.UserAgent()),
						slog.String("referer", r.Referer()),
					}

					if r.URL.RawQuery != "" {
						requestInfo = append(requestInfo, slog.String("query", r.URL.RawQuery))
					}

					if r.Method == "POST" && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
						if err := r.ParseForm(); err == nil {
							// Only log non-sensitive form fields
							formData := make(map[string]string)
							for key, values := range r.Form {
								if !isSensitiveField(key) && len(values) > 0 {
									formData[key] = values[0]
								}
							}
							if len(formData) > 0 {
								requestInfo = append(requestInfo, slog.Any("form_data", formData))
							}
						}
					}

					allArgs := make([]any, 0, len(requestInfo)+1)
					allArgs = append(allArgs, slog.Any("panic_error", err))
					for _, attr := range requestInfo {
						allArgs = append(allArgs, attr)
					}

					logger.ErrorContext(r.Context(), "panic recovered - internal server error",
						slog.Group("panic_details", allArgs...),
					)

					if !wrapped.headersSent {
						// Set security headers even in error responses
						w.Header().Set("X-Content-Type-Options", "nosniff")
						w.Header().Set("X-Frame-Options", "DENY")

						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)

						// errorHTML is a static template populated only with an
						// internally generated error ID (no user input).
						errorHTML := generateErrorHTML(errorID)
						w.Write([]byte(errorHTML))
					} else {
						logger.WarnContext(r.Context(), "cannot send error response - headers already sent",
							slog.String("error_id", errorID))
					}
				}
			}()

			next.ServeHTTP(wrapped, r)
		})
	}
}

// recoveryResponseWriter wraps http.ResponseWriter to track if headers have been sent
type recoveryResponseWriter struct {
	http.ResponseWriter
	headersSent bool
}

func (w *recoveryResponseWriter) WriteHeader(statusCode int) {
	if !w.headersSent {
		w.headersSent = true
		w.ResponseWriter.WriteHeader(statusCode)
	}
}

func (w *recoveryResponseWriter) Write(data []byte) (int, error) {
	if !w.headersSent {
		w.headersSent = true
	}
	return w.ResponseWriter.Write(data)
}

func generateErrorID() string {
	return fmt.Sprintf("ERR-%d", time.Now().UnixNano())
}

func isSensitiveField(fieldName string) bool {
	sensitiveFields := map[string]bool{
		"password":    true,
		"passwd":      true,
		"pwd":         true,
		"secret":      true,
		"token":       true,
		"csrf_token":  true,
		"api_key":     true,
		"private_key": true,
	}

	fieldLower := strings.ToLower(fieldName)
	return sensitiveFields[fieldLower] ||
		strings.Contains(fieldLower, "password") ||
		strings.Contains(fieldLower, "secret") ||
		strings.Contains(fieldLower, "token")
}

func generateErrorHTML(errorID string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Internal Server Error</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
               margin: 0; padding: 40px; background: #f5f5f5; color: #333; }
        .container { max-width: 600px; margin: 0 auto; background: white;
                    padding: 40px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .error-icon { font-size: 48px; color: #e74c3c; margin-bottom: 20px; }
        h1 { color: #e74c3c; margin: 0 0 20px 0; }
        .actions { margin-top: 30px; }
        .btn { display: inline-block; padding: 12px 24px; background: #3498db;
               color: white; text-decoration: none; border-radius: 4px;
               margin-right: 10px; }
        .btn:hover { background: #2980b9; }
    </style>
</head>
<body>
    <div class="container">
        <div class="error-icon">⚠️</div>
        <h1>Internal Server Error</h1>
        <p>We're sorry, but something went wrong on our server.</p>
        <p style="color: #666; font-size: 12px;">Error ID: %s</p>
        <div class="actions">
            <a href="/" class="btn">Return to Home</a>
            <a href="javascript:history.back()" class="btn" style="background: #95a5a6;">Go Back</a>
        </div>
    </div>
</body>
</html>`, errorID)
}
