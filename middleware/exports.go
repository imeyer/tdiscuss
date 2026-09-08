// Package middleware provides a comprehensive HTTP middleware system with
// built-in observability, security, rate limiting, and authentication.
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Re-export commonly used types and functions for convenience

// Chain operations
var (
	// NewChain creates a new middleware chain
	NewChain = newChain
)

// Context helpers - these are the primary way to access request data
var (
	// GetUser retrieves the authenticated user from context
	GetUser = getUser

	// GetPeer retrieves the authenticated tailnet identity from context,
	// including its capability grants
	GetPeer = getPeer

	// GetRequestID retrieves the request ID from context
	GetRequestID = getRequestID

	// GetTraceID retrieves the trace ID from context
	GetTraceID = getTraceID

	// GetLogger retrieves the request-scoped logger from context
	GetLogger = getLogger

	// GetCSPNonce retrieves the CSP nonce from context
	GetCSPNonce = getCSPNonce

)

// Conditional middleware helpers
var (
	// When applies middleware only when condition is true
	When = when

	// Unless applies middleware only when condition is false
	Unless = unless

	// IsAuthenticated checks if the request has an authenticated user
	IsAuthenticated = isAuthenticated

	// IsAdmin checks if the authenticated user is an admin
	IsAdmin = isAdmin

	// HasMethod returns a condition function that checks HTTP method
	HasMethod = hasMethod

	// HasPathPrefix returns a condition function that checks path prefix
	HasPathPrefix = hasPathPrefix
)

// Standard middleware constructors
var (
	// RequestContextMiddleware initializes the request context
	RequestContextMiddleware = requestContextMiddleware

	// SecurityHeadersMiddleware adds security headers
	SecurityHeadersMiddleware = securityHeadersMiddleware


	// CSRFProtectionMiddleware validates CSRF tokens
	CSRFProtectionMiddleware = csrfProtectionMiddleware

	// RequestSizeLimitMiddleware limits request body size
	RequestSizeLimitMiddleware = requestSizeLimitMiddleware

	// LoggingMiddleware provides structured logging
	LoggingMiddleware = loggingMiddleware

	// MetricsMiddleware provides basic metrics collection
	MetricsMiddleware = metricsMiddleware

	// TracingMiddleware provides distributed tracing
	TracingMiddleware = tracingMiddleware

	// StaticFileMiddleware handles static file serving
	StaticFileMiddleware = staticFileMiddleware
)

// Default configurations
var (
	// DefaultSecurityConfig returns secure defaults for security middleware
	DefaultSecurityConfig = defaultSecurityConfig

	// DefaultRateLimitConfig returns sensible defaults for rate limiting
	DefaultRateLimitConfig = defaultRateLimitConfig
)

// HashEmail creates a consistent hash of an email for privacy-preserving logging.
// Returns the first 8 bytes of the SHA256 hash as a hex string.
func HashEmail(email string) string {
	if email == "" {
		return ""
	}
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:8])
}

// NewTailscaleAuthProvider creates a new Tailscale auth provider
func NewTailscaleAuthProvider(client TailscaleClient, queries Querier, logger *slog.Logger, config TailscaleAuthConfig) AuthProvider {
	return newTailscaleAuthProvider(client, queries, logger, config)
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config *RateLimitConfig, logger *slog.Logger) *RateLimiter {
	return newRateLimiter(config, logger)
}

// NewObservabilityMiddleware creates comprehensive observability middleware
func NewObservabilityMiddleware(config *ObservabilityConfig) Middleware {
	return newObservabilityMiddleware(config)
}

// AuthMiddleware creates authentication middleware
func AuthMiddleware(provider AuthProvider, tracer trace.Tracer) Middleware {
	return authMiddleware(provider, tracer)
}

// RequireAuthMiddleware ensures the user is authenticated
func RequireAuthMiddleware() Middleware {
	return requireAuthMiddleware()
}

// RequireAdminMiddleware ensures the user is an admin
func RequireAdminMiddleware() Middleware {
	return requireAdminMiddleware()
}
