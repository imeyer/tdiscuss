// Package middleware provides a comprehensive HTTP middleware system with
// built-in observability, security, rate limiting, and authentication.
package middleware

import (
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Re-export commonly used types and functions for convenience

// Core middleware types are already defined in middleware_core.go:
// - Middleware as a function type

// Chain operations
var (
	// NewChain creates a new middleware chain
	NewChain = newChain
)

// Context helpers - these are the primary way to access request data
var (
	// GetUser retrieves the authenticated user from context
	GetUser = getUser

	// GetRequestID retrieves the request ID from context
	GetRequestID = getRequestID

	// GetTraceID retrieves the trace ID from context
	GetTraceID = getTraceID

	// GetLogger retrieves the request-scoped logger from context
	GetLogger = getLogger

	// GetCSPNonce retrieves the CSP nonce from context
	GetCSPNonce = getCSPNonce

	// GetCSRFToken retrieves the CSRF token from context
	GetCSRFToken = getCSRFToken
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

	// CSRFTokenMiddleware generates and sets CSRF tokens
	CSRFTokenMiddleware = csrfTokenMiddleware

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

	// RecoveryMiddleware recovers from panics
	// RecoveryMiddleware = recoveryMiddleware // Note: recoveryMiddleware not found in codebase

	// IPWhitelistMiddleware restricts access to specific IPs
	IPWhitelistMiddleware = ipWhitelistMiddleware

	// StaticFileMiddleware handles static file serving
	StaticFileMiddleware = staticFileMiddleware
)

// Configuration types are already exported from their respective files:
// - SecurityConfig from middleware_security.go
// - RateLimitConfig from middleware_ratelimit.go
// - ObservabilityConfig from middleware_observability.go
// - EndpointLimit from middleware_ratelimit.go

// Default configurations
var (
	// DefaultSecurityConfig returns secure defaults for security middleware
	DefaultSecurityConfig = defaultSecurityConfig

	// DefaultRateLimitConfig returns sensible defaults for rate limiting
	DefaultRateLimitConfig = defaultRateLimitConfig
)

// Helper functions for common tasks
// HashEmail is defined in the parent package and should be imported from there

// NewTailscaleAuthProvider creates a new Tailscale auth provider
func NewTailscaleAuthProvider(client TailscaleClient, queries Querier, logger *slog.Logger) AuthProvider {
	return newTailscaleAuthProvider(client, queries, logger)
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

// NewMiddlewareSetup creates a new middleware setup
func NewMiddlewareSetup(logger *slog.Logger, telemetry *TelemetryConfig, authProvider AuthProvider) *MiddlewareSetup {
	return newMiddlewareSetup(logger, telemetry, authProvider)
}
