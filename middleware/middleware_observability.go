package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// ObservabilityConfig holds configuration for observability middleware
type ObservabilityConfig struct {
	ServiceName      string
	Logger           *slog.Logger
	Tracer           trace.Tracer
	Meter            metric.Meter
	RequestCounter   metric.Int64Counter
	RequestDuration  metric.Float64Histogram
	RequestSize      metric.Int64Histogram
	ResponseSize     metric.Int64Histogram
	ErrorCounter     metric.Int64Counter
	ActiveRequests   metric.Int64UpDownCounter
	SampleRate       float64
	LogRequestBody   bool
	LogResponseBody  bool
	SensitiveHeaders []string
	SensitivePaths   []string
}

// newObservabilityMiddleware creates a comprehensive observability middleware
func newObservabilityMiddleware(config *ObservabilityConfig) Middleware {
	// Default sensitive headers if not provided
	if len(config.SensitiveHeaders) == 0 {
		config.SensitiveHeaders = []string{
			"authorization",
			"cookie",
			"x-csrf-token",
		}
	}

	// Default sensitive paths if not provided
	if len(config.SensitivePaths) == 0 {
		config.SensitivePaths = []string{
			"/admin",
			"/auth",
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get or create request context
			rc := getOrCreateRequestContext(r.Context())

			// Start span
			ctx, span := config.Tracer.Start(r.Context(),
				fmt.Sprintf("%s %s", r.Method, r.URL.Path),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPMethodKey.String(r.Method),
					semconv.HTTPTargetKey.String(r.URL.Path),
					semconv.HTTPSchemeKey.String(r.URL.Scheme),
					attribute.String("http.host", r.Host),
					attribute.String("http.user_agent", r.UserAgent()),
					attribute.Int64("http.request_content_length", r.ContentLength),
				),
			)
			defer span.End()

			// Update request context with trace ID
			if spanCtx := span.SpanContext(); spanCtx.IsValid() {
				rc.TraceID = spanCtx.TraceID().String()
			}

			// Wrap response writer
			wrapped := newResponseWriter(w)

			// Track active requests
			if config.ActiveRequests != nil {
				config.ActiveRequests.Add(ctx, 1)
				defer config.ActiveRequests.Add(ctx, -1)
			}

			// Log request start
			if config.Logger != nil && config.SampleRate > 0 {
				config.Logger.InfoContext(ctx, "request_started",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("remote_addr", r.RemoteAddr),
					slog.String("request_id", rc.RequestID),
					slog.String("trace_id", rc.TraceID),
					slog.String("user_agent", r.UserAgent()),
					slog.Int64("content_length", r.ContentLength),
				)
			}

			// Execute handler
			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// Calculate duration
			duration := time.Since(rc.StartTime)

			// Determine route pattern for metrics
			routePattern := getRoutePattern(r.URL.Path)

			// Common attributes for metrics
			attrs := []attribute.KeyValue{
				attribute.String("method", r.Method),
				attribute.String("route", routePattern),
				attribute.Int("status_code", wrapped.Status()),
				attribute.String("status_class", fmt.Sprintf("%dxx", wrapped.Status()/100)),
			}

			// Record metrics
			if config.RequestCounter != nil {
				config.RequestCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
			}

			if config.RequestDuration != nil {
				config.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
			}

			if config.RequestSize != nil && r.ContentLength > 0 {
				config.RequestSize.Record(ctx, r.ContentLength, metric.WithAttributes(attrs...))
			}

			if config.ResponseSize != nil {
				config.ResponseSize.Record(ctx, wrapped.BytesWritten(), metric.WithAttributes(attrs...))
			}

			// Record errors
			if wrapped.Status() >= 400 && config.ErrorCounter != nil {
				errorAttrs := append(attrs, attribute.String("error_type", getErrorType(wrapped.Status())))
				config.ErrorCounter.Add(ctx, 1, metric.WithAttributes(errorAttrs...))
			}

			// Update span with response info
			span.SetAttributes(
				semconv.HTTPStatusCodeKey.Int(wrapped.Status()),
				attribute.Int64("http.response_content_length", wrapped.BytesWritten()),
				attribute.Float64("http.request.duration_ms", float64(duration.Milliseconds())),
			)

			// Set span status based on HTTP status
			if wrapped.Status() >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.Status()))
			} else {
				span.SetStatus(codes.Ok, "")
			}

			// Log request completion
			if config.Logger != nil && config.SampleRate > 0 {
				logLevel := slog.LevelInfo
				if wrapped.Status() >= 500 {
					logLevel = slog.LevelError
				} else if wrapped.Status() >= 400 {
					logLevel = slog.LevelWarn
				}

				config.Logger.LogAttrs(ctx, logLevel, "request_completed",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("remote_addr", r.RemoteAddr),
					slog.String("request_id", rc.RequestID),
					slog.String("trace_id", rc.TraceID),
					slog.Int("status", wrapped.Status()),
					slog.Int64("bytes_written", wrapped.BytesWritten()),
					slog.Duration("duration", duration),
					slog.Float64("duration_ms", float64(duration.Milliseconds())),
				)
			}
		})
	}
}

// getRoutePattern normalizes URL paths for metrics to avoid high cardinality
func getRoutePattern(path string) string {
	// Common patterns to normalize
	patterns := []struct {
		prefix  string
		pattern string
	}{
		{"/thread/", "/thread/{tid}"},
		{"/member/", "/member/{id}"},
		{"/api/v1/thread/", "/api/v1/thread/{tid}"},
		{"/api/v1/member/", "/api/v1/member/{id}"},
	}

	for _, p := range patterns {
		if strings.HasPrefix(path, p.prefix) {
			// Check if there's more path after the prefix
			remaining := path[len(p.prefix):]
			if idx := strings.Index(remaining, "/"); idx > 0 {
				// There's a subpath, so use the pattern + subpath
				return p.pattern + remaining[idx:]
			}
			return p.pattern
		}
	}

	// For root and other exact paths, return as-is
	return path
}

// getErrorType categorizes HTTP errors
func getErrorType(statusCode int) string {
	switch statusCode {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 405:
		return "method_not_allowed"
	case 408:
		return "timeout"
	case 413:
		return "payload_too_large"
	case 429:
		return "too_many_requests"
	case 500:
		return "internal_error"
	case 502:
		return "bad_gateway"
	case 503:
		return "service_unavailable"
	case 504:
		return "gateway_timeout"
	default:
		if statusCode >= 400 && statusCode < 500 {
			return "client_error"
		}
		return "server_error"
	}
}

// loggingMiddleware provides structured logging for requests
func loggingMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := getOrCreateRequestContext(r.Context())
			wrapped := newResponseWriter(w)

			// Add request ID to logger
			requestLogger := logger.With(
				slog.String("request_id", rc.RequestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			// Store logger in context for handlers to use
			ctx := r.Context()
			ctx = context.WithValue(ctx, contextKey("logger"), requestLogger)

			// Log request
			requestLogger.DebugContext(ctx, "request_received",
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)

			// Execute handler
			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// Log response
			duration := time.Since(rc.StartTime)
			requestLogger.InfoContext(ctx, "request_completed",
				slog.Int("status", wrapped.Status()),
				slog.Duration("duration", duration),
				slog.Int64("bytes", wrapped.BytesWritten()),
			)
		})
	}
}

// getLogger retrieves the request-scoped logger from context
func getLogger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(contextKey("logger")).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// metricsMiddleware provides basic metrics collection
func metricsMiddleware(meter metric.Meter) Middleware {
	requestCounter, _ := meter.Int64Counter("http.server.request.count",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("{request}"),
	)

	requestDuration, _ := meter.Float64Histogram("http.server.request.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("s"),
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := newResponseWriter(w)

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			// Record metrics
			attrs := []attribute.KeyValue{
				attribute.String("method", r.Method),
				attribute.String("route", getRoutePattern(r.URL.Path)),
				attribute.Int("status_code", wrapped.Status()),
			}

			requestCounter.Add(r.Context(), 1, metric.WithAttributes(attrs...))
			requestDuration.Record(r.Context(), duration.Seconds(), metric.WithAttributes(attrs...))
		})
	}
}

// tracingMiddleware provides distributed tracing
func tracingMiddleware(tracer trace.Tracer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(),
				fmt.Sprintf("%s %s", r.Method, r.URL.Path),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPMethodKey.String(r.Method),
					semconv.HTTPTargetKey.String(r.URL.Path),
					semconv.HTTPSchemeKey.String(r.URL.Scheme),
				),
			)
			defer span.End()

			wrapped := newResponseWriter(w)

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			span.SetAttributes(
				semconv.HTTPStatusCodeKey.Int(wrapped.Status()),
			)

			if wrapped.Status() >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.Status()))
			}
		})
	}
}
