package main

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

type TracedQuerier struct {
	Querier
	telemetry *TelemetryConfig
}

type TracedExtendedQuerier struct {
	ExtendedQuerier
	telemetry *TelemetryConfig
}

// NewTracedQuerier creates a new TracedQuerier
func NewTracedQuerier(querier Querier, telemetry *TelemetryConfig) *TracedQuerier {
	return &TracedQuerier{
		telemetry: telemetry,
	}
}

// Implement each method of the Querier interface with tracing
func (t *TracedQuerier) GetMemberId(ctx context.Context, email string) (int64, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetMemberId")
	defer span.End()

	start := time.Now()
	id, err := t.GetMemberId(ctx, email)
	duration := time.Since(start).Seconds()

	// Add attributes to the span
	span.SetAttributes(
		attribute.String("email", email),
		attribute.Int64("result.id", id),
		attribute.Float64("duration_seconds", duration),
	)

	if err != nil {
		span.RecordError(err)
	}

	return id, err
}
