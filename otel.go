package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/processors/minsev"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

type TelemetryConfig struct {
	LogHandler        slog.Handler
	LogHTTPOptions    []otlploghttp.Option
	Meter             metric.Meter
	MetricHTTPOptions []otlpmetrichttp.Option
	Metrics           struct {
		ErrorCounter    metric.Int64Counter
		RequestCounter  metric.Int64Counter
		VersionGauge    metric.Int64Gauge
		RequestDuration metric.Float64Histogram
		DBQueryDuration metric.Float64Histogram
	}
	TraceHTTPOptions []otlptracehttp.Option
	Tracer           trace.Tracer
}

var currentBufferSize int64

// SetBufferSize updates the current buffer size for metrics
func SetBufferSize(size int64) {
	atomic.StoreInt64(&currentBufferSize, size)
}

// GetBufferSize returns the current buffer size
func GetBufferSize() int64 {
	return atomic.LoadInt64(&currentBufferSize)
}

// SetupTelemetry initializes OTEL tracing, metrics, and logging
func setupTelemetry(ctx context.Context, config *Config) (*TelemetryConfig, func(context.Context) error, error) {
	telemetryConfig := &TelemetryConfig{}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNamespace("tdiscuss"),
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTEL resource: %w", err)
	}

	var meterProvider *sdkmetric.MeterProvider

	if !config.OTLP {
		prometheusExporter, err := prometheus.New()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create Prometheus exporter: %w", err)
		}

		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(
				prometheusExporter,
			),
		)
	} else {
		// Configure metric/meter
		metricExporter, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithInsecure())
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create OTEL metrics exporter: %w", err)
		}

		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(metricExporter),
			),
		)
	}

	otel.SetMeterProvider(meterProvider)
	telemetryConfig.Meter = meterProvider.Meter(config.ServiceName)

	// Configure OTLP log handler
	logExporter, err := otlploghttp.New(ctx,
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create log exporter: %w", err)
	}

	var processor sdklog.Processor = sdklog.NewBatchProcessor(logExporter, sdklog.WithExportBufferSize(512))

	processor = minsev.NewLogProcessor(processor, minsev.SeverityInfo)

	if config.LogDebug {
		processor = minsev.NewLogProcessor(processor, minsev.SeverityDebug)
	}

	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(processor),
	)

	otlpLogHandler := otelslog.NewHandler(
		config.ServiceName,
		otelslog.WithLoggerProvider(logProvider),
	)

	telemetryConfig.LogHandler = otlpLogHandler

	// Configure tracer with compression
	traceExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	var sampler sdktrace.Sampler

	// We'll always sample errors
	alwaysOnError := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(config.TraceSampleRate),
		sdktrace.WithRemoteParentSampled(sdktrace.AlwaysSample()),
		sdktrace.WithRemoteParentNotSampled(sdktrace.TraceIDRatioBased(config.TraceSampleRate)),
		sdktrace.WithLocalParentSampled(sdktrace.AlwaysSample()),
		sdktrace.WithLocalParentNotSampled(sdktrace.TraceIDRatioBased(config.TraceSampleRate)),
	)

	// Configure the sampler
	if config.TraceSampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if config.TraceSampleRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = alwaysOnError
	}

	config.Logger.Info("configured tracer with sampling",
		slog.Float64("rate", config.TraceSampleRate))

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithMaxExportBatchSize(config.TraceMaxBatchSize),
		),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(traceProvider)
	telemetryConfig.Tracer = traceProvider.Tracer(config.ServiceName)

	//
	// Initialize metrics
	//
	initializeMetrics(telemetryConfig.Meter, telemetryConfig)

	cleanup := func(ctx context.Context) error {
		if err := meterProvider.Shutdown(ctx); err != nil {
			return err
		}

		if err := traceProvider.Shutdown(ctx); err != nil {
			return err
		}

		if err := logProvider.Shutdown(ctx); err != nil {
			return err
		}

		return nil
	}

	return telemetryConfig, cleanup, nil
}
