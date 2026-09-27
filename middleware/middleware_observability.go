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
	if len(config.SensitiveHeaders) == 0 {
		config.SensitiveHeaders = []string{
			"authorization",
			"cookie",
			"x-csrf-token",
		}
	}

	if len(config.SensitivePaths) == 0 {
		config.SensitivePaths = []string{
			"/admin",
			"/auth",
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := getOrCreateRequestContext(r.Context())

			ctx, span := config.Tracer.Start(r.Context(),
				methodLabel(r.Method)+" "+routeLabel(r),
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

			if spanCtx := span.SpanContext(); spanCtx.IsValid() {
				rc.TraceID = spanCtx.TraceID().String()
			}

			wrapped := newResponseWriter(w)

			if config.ActiveRequests != nil {
				config.ActiveRequests.Add(ctx, 1)
				defer config.ActiveRequests.Add(ctx, -1)
			}

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

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			duration := time.Since(rc.StartTime)

			attrs := []attribute.KeyValue{
				attribute.String("method", methodLabel(r.Method)),
				attribute.String("route", routeLabel(r)),
				attribute.Int("status_code", wrapped.Status()),
				attribute.String("status_class", fmt.Sprintf("%dxx", wrapped.Status()/100)),
			}

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

			if wrapped.Status() >= 400 && config.ErrorCounter != nil {
				errorAttrs := append(attrs, attribute.String("error_type", getErrorType(wrapped.Status())))
				config.ErrorCounter.Add(ctx, 1, metric.WithAttributes(errorAttrs...))
			}

			span.SetAttributes(
				semconv.HTTPStatusCodeKey.Int(wrapped.Status()),
				attribute.Int64("http.response_content_length", wrapped.BytesWritten()),
				attribute.Float64("http.request.duration_ms", float64(duration.Milliseconds())),
			)

			if wrapped.Status() >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.Status()))
			} else {
				span.SetStatus(codes.Ok, "")
			}

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

// routeLabel names the route a request matched, for metric labels and span
// names: its ServeMux pattern without the method, or "unmatched" when only
// the catch-all "/" matched (or no ServeMux routed it). Unlike the raw path it
// has one value per route, so requests for made-up URLs cannot create new
// metric series, and a rise in "unmatched" shows someone probing.
func routeLabel(r *http.Request) string {
	pattern := r.Pattern
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	switch pattern {
	case "", "/":
		return "unmatched"
	case "/{$}":
		return "/"
	}
	return pattern
}

// methodLabel is method for the standard HTTP methods and "other" for
// anything else, since a client can send any token as a method.
func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	}
	return "other"
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

			requestLogger := logger.With(
				slog.String("request_id", rc.RequestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			ctx := r.Context()
			ctx = context.WithValue(ctx, contextKey("logger"), requestLogger)

			requestLogger.DebugContext(ctx, "request_received",
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)

			next.ServeHTTP(wrapped, r.WithContext(ctx))

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

			attrs := []attribute.KeyValue{
				attribute.String("method", methodLabel(r.Method)),
				attribute.String("route", routeLabel(r)),
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
