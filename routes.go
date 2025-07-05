package main

import (
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

// SetupRoutes configures all HTTP routes with their appropriate middleware chains
func SetupRoutes(dsvc *DiscussService, staticFS embed.FS) http.Handler {
	// Create auth provider with adapters
	tailscaleAdapter := NewTailscaleClientAdapter(dsvc.tailClient)
	querierAdapter := NewQuerierAdapter(dsvc.queries)
	authProvider := middleware.NewTailscaleAuthProvider(tailscaleAdapter, querierAdapter, dsvc.logger)

	// Create middleware setup with converted telemetry config
	telemetryConfig := ConvertTelemetryConfig(dsvc.telemetry)
	ms := middleware.NewMiddlewareSetup(dsvc.logger, telemetryConfig, authProvider)

	// Configure rate limiting
	// We need to use the actual metric.Meter from the original config
	ms.RateLimitConfig.Meter = dsvc.telemetry.Meter
	
	// Check if we're in dev mode based on debug flag
	isDevMode := dsvc.logger.Enabled(context.Background(), slog.LevelDebug)
	
	// Configure rate limits with more permissive admin rate in dev mode
	adminRate := 0.2 // Default: 1 request per 5 seconds
	if isDevMode {
		adminRate = 10.0 // Dev mode: 10 requests per second
	}
	
	ms.RateLimitConfig.EndpointLimits = map[string]middleware.EndpointLimit{
		"/thread/new":        {Pattern: "/thread/new", Rate: 0.5, Burst: 2},      // 1 thread per 2 seconds
		"/thread/{tid}":      {Pattern: "/thread/{tid}", Rate: 2, Burst: 5},      // 2 posts per second
		"/thread/{tid}/edit": {Pattern: "/thread/{tid}/edit", Rate: 1, Burst: 3}, // 1 edit per second
		"/member/edit":       {Pattern: "/member/edit", Rate: 0.5, Burst: 2},     // 1 profile update per 2 seconds
		"/admin":             {Pattern: "/admin", Rate: adminRate, Burst: 1},      // Varies based on dev mode
	}

	// Configure observability
	// Use the actual OpenTelemetry types from the original config
	ms.ObservabilityConfig = &middleware.ObservabilityConfig{
		ServiceName:     "tdiscuss",
		Logger:          dsvc.logger,
		Tracer:          dsvc.telemetry.Tracer,
		Meter:           dsvc.telemetry.Meter,
		RequestCounter:  dsvc.telemetry.Metrics.RequestCounter,
		RequestDuration: dsvc.telemetry.Metrics.RequestDuration,
		ErrorCounter:    dsvc.telemetry.Metrics.ErrorCounter,
		SampleRate:      1.0, // TODO: Get from config
	}

	// Create router
	mux := http.NewServeMux()

	// Create middleware chains
	// Add board data middleware to all authenticated chains
	boardDataMiddleware := middleware.BoardDataMiddleware(querierAdapter)
	
	// All routes require Tailscale authentication
	authChain := ms.CreateAuthenticatedChain().Append(boardDataMiddleware)
	adminChain := ms.CreateAdminChain().Append(boardDataMiddleware)

	// Routes accessible to all authenticated Tailscale users
	mux.Handle("GET /{$}", authChain.ThenFunc(dsvc.ListThreads))
	mux.Handle("GET /thread/{tid}", authChain.ThenFunc(dsvc.ListThreadPosts))
	mux.Handle("GET /member/{mid}", authChain.ThenFunc(dsvc.ListMember))
	mux.Handle("GET /thread/new", authChain.ThenFunc(dsvc.NewThread))
	mux.Handle("POST /thread/new", authChain.ThenFunc(dsvc.CreateThread))
	mux.Handle("GET /thread/{tid}/edit", authChain.ThenFunc(dsvc.EditThread))
	mux.Handle("POST /thread/{tid}/edit", authChain.ThenFunc(dsvc.EditThread))
	mux.Handle("GET /thread/{tid}/{pid}/edit", authChain.ThenFunc(dsvc.EditThreadPost))
	mux.Handle("POST /thread/{tid}/{pid}/edit", authChain.ThenFunc(dsvc.EditThreadPost))
	mux.Handle("POST /thread/{tid}", authChain.ThenFunc(dsvc.CreateThreadPost))
	mux.Handle("GET /member/edit", authChain.ThenFunc(dsvc.EditMemberProfile))
	mux.Handle("POST /member/edit", authChain.ThenFunc(dsvc.EditMemberProfile))

	// Admin routes
	mux.Handle("GET /admin", adminChain.ThenFunc(dsvc.Admin))
	mux.Handle("POST /admin", adminChain.ThenFunc(dsvc.Admin))

	// Static files - serve directly from embed.FS
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get the file path
		path := r.URL.Path

		// The embed path needs to include "static/" prefix
		// URL: /static/theme.js -> embed path: static/theme.js
		embedPath := path[1:] // Remove leading slash: /static/theme.js -> static/theme.js

		// Read the file from embed.FS
		data, err := staticFS.ReadFile(embedPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Set content type based on file extension
		switch {
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript")
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css")
		case strings.HasSuffix(path, ".html"):
			w.Header().Set("Content-Type", "text/html")
		case strings.HasSuffix(path, ".json"):
			w.Header().Set("Content-Type", "application/json")
		}

		// Write the file
		w.Write(data)
	})

	// Static files don't need authentication - they're public assets
	mux.Handle("/static/", staticHandler)

	// Health check endpoint
	healthChain := middleware.NewChain(
		middleware.RequestContextMiddleware(),
		middleware.LoggingMiddleware(dsvc.logger),
	)
	mux.Handle("GET /health", healthChain.ThenFunc(dsvc.HealthCheck))

	// Metrics endpoint
	metricsChain := middleware.NewChain(
		middleware.RequestContextMiddleware(),
		// No auth required for metrics, but you might want to add IP whitelist
		middleware.When(middleware.HasPathPrefix("/_/metrics"), middleware.IPWhitelistMiddleware([]string{"127.0.0.1", "::1"})),
	)
	mux.Handle("GET /_/metrics", metricsChain.Then(promhttp.Handler()))

	// Add global panic recovery as the outermost middleware
	globalChain := middleware.NewChain(
		RecoveryMiddleware(dsvc.logger),
	)

	return globalChain.Then(mux)
}

// RecoveryMiddleware recovers from panics and logs them with enhanced error reporting
func RecoveryMiddleware(logger *slog.Logger) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create response wrapper to track write status
			wrapped := &recoveryResponseWriter{ResponseWriter: w}

			defer func() {
				if err := recover(); err != nil {
					// Generate unique error ID for tracking
					errorID := generateErrorID()

					// Collect request information
					requestInfo := []slog.Attr{
						slog.String("error_id", errorID),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("remote_addr", r.RemoteAddr),
						slog.String("user_agent", r.UserAgent()),
						slog.String("referer", r.Referer()),
					}

					// Add query parameters if present
					if r.URL.RawQuery != "" {
						requestInfo = append(requestInfo, slog.String("query", r.URL.RawQuery))
					}

					// Add form data for POST requests (be careful with sensitive data)
					if r.Method == "POST" && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
						if err := r.ParseForm(); err == nil {
							// Only log non-sensitive form fields
							formData := make(map[string]string)
							for key, values := range r.Form {
								// Skip sensitive fields
								if !isSensitiveField(key) && len(values) > 0 {
									formData[key] = values[0]
								}
							}
							if len(formData) > 0 {
								requestInfo = append(requestInfo, slog.Any("form_data", formData))
							}
						}
					}

					// Log the panic with full context
					allArgs := make([]any, 0, len(requestInfo)+1)
					allArgs = append(allArgs, slog.Any("panic_error", err))
					for _, attr := range requestInfo {
						allArgs = append(allArgs, attr)
					}

					logger.ErrorContext(r.Context(), "panic recovered - internal server error",
						slog.Group("panic_details", allArgs...),
					)

					// Return appropriate response if not already sent
					if !wrapped.headersSent {
						// Set security headers even in error responses
						w.Header().Set("X-Content-Type-Options", "nosniff")
						w.Header().Set("X-Frame-Options", "DENY")

						// Return user-friendly error with error ID for support
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)

						errorHTML := generateErrorHTML(errorID)
						w.Write([]byte(errorHTML))
					} else {
						// Response already started, log this fact
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

// Helper functions

func generateErrorID() string {
	// Simple timestamp-based error ID
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
		"credit_card": true,
		"ssn":         true,
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
        .error-id { background: #f8f9fa; padding: 10px; border-radius: 4px; 
                   font-family: monospace; font-size: 14px; margin: 20px 0; 
                   border-left: 4px solid #e74c3c; }
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
        <p>We're sorry, but something went wrong on our server. Our team has been notified and will investigate the issue.</p>
        
        <div class="error-id">
            <strong>Error ID:</strong> %s<br>
            <small>Please include this ID when contacting support.</small>
        </div>
        
        <div class="actions">
            <a href="/" class="btn">Return to Home</a>
            <a href="javascript:history.back()" class="btn" style="background: #95a5a6;">Go Back</a>
        </div>
    </div>
</body>
</html>`, errorID)
}
