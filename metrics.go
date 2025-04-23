package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"go.opentelemetry.io/otel/metric"
)

type MetricDefinition struct {
	Name        string
	Description string
	Unit        string
	Type        string // "counter", "gauge", "histogram"
}

func initializeMetrics(meter metric.Meter, config *TelemetryConfig) error {
	if err := initializeCounters(meter, config); err != nil {
		return fmt.Errorf("failed to initialize counters: %w", err)
	}

	if err := initializeGauges(meter, config); err != nil {
		return fmt.Errorf("failed to initialize gauges: %w", err)
	}

	if err := initializeHistograms(meter, config); err != nil {
		return fmt.Errorf("failed to initialize histograms: %w", err)
	}

	return nil
}

func initializeCounters(meter metric.Meter, config *TelemetryConfig) error {
	counterMetrics := []struct {
		Name        string
		Description string
		Unit        string
		Target      *metric.Int64Counter
	}{
		{
			Name:        "http_requests_total",
			Description: "Total number of HTTP requests",
			Unit:        "{requests}",
			Target:      &config.Metrics.RequestCounter,
		},
		{
			Name:        "http_errors_total",
			Description: "Total number of HTTP errors",
			Unit:        "{errors}",
			Target:      &config.Metrics.ErrorCounter,
		},
	}

	for _, m := range counterMetrics {
		opts := []metric.Int64CounterOption{
			metric.WithDescription(m.Description),
		}

		if m.Unit != "" {
			opts = append(opts, metric.WithUnit(m.Unit))
		}

		counter, err := meter.Int64Counter(m.Name, opts...)
		if err != nil {
			return fmt.Errorf("failed to create counter %s: %w", m.Name, err)
		}

		*m.Target = counter
	}

	return nil
}

func initializeGauges(meter metric.Meter, config *TelemetryConfig) error {
	gaugeMetrics := []struct {
		Name        string
		Description string
		Unit        string
		Target      *metric.Int64Gauge
	}{
		{
			Name:        "tdiscuss_build_info",
			Description: "A gauge with version and git commit information",
			Target:      &config.Metrics.VersionGauge,
		},
	}

	for _, m := range gaugeMetrics {
		opts := []metric.Int64GaugeOption{
			metric.WithDescription(m.Description),
		}

		if m.Unit != "" {
			opts = append(opts, metric.WithUnit(m.Unit))
		}

		gauge, err := meter.Int64Gauge(m.Name, opts...)
		if err != nil {
			return fmt.Errorf("failed to create gauge %s: %w", m.Name, err)
		}

		*m.Target = gauge
	}

	return nil
}

func initializeHistograms(meter metric.Meter, config *TelemetryConfig) error {
	histogramMetrics := []struct {
		Buckets     []float64
		Description string
		Name        string
		Target      *metric.Float64Histogram
		Unit        string
	}{
		{
			Buckets:     []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
			Description: "Histogram of response latency (seconds) for HTTP requests.",
			Name:        "http_request_duration_seconds",
			Target:      &config.Metrics.RequestDuration,
			Unit:        "s",
		},
		{
			Buckets:     []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
			Description: "Histogram of DB Query operations",
			Name:        "db_query_duration_seconds",
			Target:      &config.Metrics.DBQueryDuration,
			Unit:        "s",
		},
	}

	for _, m := range histogramMetrics {
		opts := []metric.Float64HistogramOption{
			metric.WithDescription(m.Description),
		}

		if m.Unit != "" {
			opts = append(opts, metric.WithUnit(m.Unit))
		}

		if len(m.Buckets) > 0 {
			opts = append(opts, metric.WithExplicitBucketBoundaries(m.Buckets...))
		}

		histogram, err := meter.Float64Histogram(m.Name, opts...)
		if err != nil {
			return fmt.Errorf("failed to create histogram %s: %w", m.Name, err)
		}

		*m.Target = histogram
	}

	return nil
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
