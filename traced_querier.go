package main

import (
	"context"
	"fmt"
	"time"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// TracedQueriesWrapper implements the ExtendedQuerier interface and adds tracing functionality
type TracedQueriesWrapper struct {
	wrapped   ExtendedQuerier
	telemetry *TelemetryConfig
}

// NewTracedQueriesWrapper creates a new TracedQueriesWrapper that decorates an existing ExtendedQuerier
func NewTracedQueriesWrapper(wrapped ExtendedQuerier, telemetry *TelemetryConfig) ExtendedQuerier {
	return &TracedQueriesWrapper{
		wrapped:   wrapped,
		telemetry: telemetry,
	}
}

// WithTx creates a new TracedQueriesWrapper with a transaction
func (t *TracedQueriesWrapper) WithTx(tx pgx.Tx) ExtendedQuerier {
	return &TracedQueriesWrapper{
		wrapped:   t.wrapped.WithTx(tx),
		telemetry: t.telemetry,
	}
}

// recordMetrics is a helper method to record query duration metrics
func (t *TracedQueriesWrapper) recordMetrics(ctx context.Context, queryName string, duration float64) {
	if t.telemetry.Metrics.DBQueryDuration != nil {
		t.telemetry.Metrics.DBQueryDuration.Record(ctx, duration,
			metric.WithAttributes(
				attribute.String("query", queryName),
			),
		)
	}
}

// CreateOrReturnID implements the Querier interface with tracing
func (t *TracedQueriesWrapper) CreateOrReturnID(ctx context.Context, pEmail string) (CreateOrReturnIDRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "CreateOrReturnID(query)")
	defer span.End()

	start := time.Now()
	row, err := t.wrapped.CreateOrReturnID(ctx, pEmail)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return row, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Bool("user.isAdmin", row.IsAdmin),
		attribute.Int64("user.id", row.ID),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "CreateOrReturnID", duration)
	span.SetStatus(codes.Ok, "")

	return row, nil
}

// CreateThread implements the Querier interface with tracing
func (t *TracedQueriesWrapper) CreateThread(ctx context.Context, arg CreateThreadParams) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "CreateThread(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.CreateThread(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.String("thread.subject", arg.Subject),
		attribute.Int64("member.id", arg.MemberID),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "CreateThread", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// CreateThreadPost implements the Querier interface with tracing
func (t *TracedQueriesWrapper) CreateThreadPost(ctx context.Context, arg CreateThreadPostParams) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "CreateThreadPost(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.CreateThreadPost(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.id", arg.ThreadID),
		attribute.Int64("member.id", arg.MemberID),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "CreateThreadPost", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// GetBoardData implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetBoardData(ctx context.Context) (GetBoardDataRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetBoardData(query)")
	defer span.End()

	start := time.Now()
	row, err := t.wrapped.GetBoardData(ctx)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return row, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int("board.id", int(row.ID)),
		attribute.String("board.title", row.Title),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetBoardData", duration)
	span.SetStatus(codes.Ok, "")

	return row, nil
}

// GetMember implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetMember(ctx context.Context, id int64) (GetMemberRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetMember(query)")
	defer span.End()

	start := time.Now()
	row, err := t.wrapped.GetMember(ctx, id)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return row, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("member.id", id),
		attribute.String("member.email_hash", middleware.HashEmail(row.Email)),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetMember", duration)
	span.SetStatus(codes.Ok, "")

	return row, nil
}

// GetMemberId implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetMemberId(ctx context.Context, email string) (int64, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetMemberId(query)")
	defer span.End()

	start := time.Now()
	id, err := t.wrapped.GetMemberId(ctx, email)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return id, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.String("member.email_hash", middleware.HashEmail(email)),
		attribute.Int64("member.id", id),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetMemberId", duration)
	span.SetStatus(codes.Ok, "")

	return id, nil
}

// GetThreadForEdit implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetThreadForEdit(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetThreadForEdit(query)")
	defer span.End()

	start := time.Now()
	row, err := t.wrapped.GetThreadForEdit(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return row, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.id", arg.ID),
		attribute.Int64("member.id", arg.ID_2),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetThreadForEdit", duration)
	span.SetStatus(codes.Ok, "")

	return row, nil
}

// GetThreadPostForEdit implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetThreadPostForEdit(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetThreadPostForEdit(query)")
	defer span.End()

	start := time.Now()
	row, err := t.wrapped.GetThreadPostForEdit(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return row, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("threadpost.id", arg.ID),
		attribute.Int64("member.id", arg.ID_2),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetThreadPostForEdit", duration)
	span.SetStatus(codes.Ok, "")

	return row, nil
}

// GetThreadPostSequenceId implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetThreadPostSequenceId(ctx context.Context) (int64, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetThreadPostSequenceId(query)")
	defer span.End()

	start := time.Now()
	id, err := t.wrapped.GetThreadPostSequenceId(ctx)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return id, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("threadpost.seq.id", id),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetThreadPostSequenceId", duration)
	span.SetStatus(codes.Ok, "")

	return id, nil
}

// GetThreadSequenceId implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetThreadSequenceId(ctx context.Context) (int64, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetThreadSequenceId(query)")
	defer span.End()

	start := time.Now()
	id, err := t.wrapped.GetThreadSequenceId(ctx)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return id, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.seq.id", id),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetThreadSequenceId", duration)
	span.SetStatus(codes.Ok, "")

	return id, nil
}

// GetThreadSubjectById implements the Querier interface with tracing
func (t *TracedQueriesWrapper) GetThreadSubjectById(ctx context.Context, id int64) (string, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "GetThreadSubjectById(query)")
	defer span.End()

	start := time.Now()
	subject, err := t.wrapped.GetThreadSubjectById(ctx, id)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return subject, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.id", id),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "GetThreadSubjectById", duration)
	span.SetStatus(codes.Ok, "")

	return subject, nil
}

// ListMemberThreads implements the Querier interface with tracing
func (t *TracedQueriesWrapper) ListMemberThreads(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "ListMemberThreads(query)")
	defer span.End()

	start := time.Now()
	rows, err := t.wrapped.ListMemberThreads(ctx, memberID)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return rows, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("member.id", memberID),
		attribute.Int("result.count", len(rows)),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "ListMemberThreads", duration)
	span.SetStatus(codes.Ok, "")

	return rows, nil
}

// ListThreadPosts implements the Querier interface with tracing
func (t *TracedQueriesWrapper) ListThreadPosts(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "ListThreadPosts(query)")
	defer span.End()

	start := time.Now()
	rows, err := t.wrapped.ListThreadPosts(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return rows, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.id", arg.ThreadID),
		attribute.String("user.email_hash", middleware.HashEmail(arg.Email)),
		attribute.Int("result.count", len(rows)),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "ListThreadPosts", duration)
	span.SetStatus(codes.Ok, "")

	return rows, nil
}

// ListThreads implements the Querier interface with tracing
func (t *TracedQueriesWrapper) ListThreads(ctx context.Context, arg ListThreadsParams) ([]ListThreadsRow, error) {
	ctx, span := t.telemetry.Tracer.Start(ctx, "ListThreads(query)")
	defer span.End()

	start := time.Now()
	rows, err := t.wrapped.ListThreads(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return rows, fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.String("user.email_hash", middleware.HashEmail(arg.Email)),
		attribute.Int64("member.id", arg.MemberID),
		attribute.Int("result.count", len(rows)),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "ListThreads", duration)
	span.SetStatus(codes.Ok, "")

	return rows, nil
}

// UpdateBoardEditWindow implements the Querier interface with tracing
func (t *TracedQueriesWrapper) UpdateBoardEditWindow(ctx context.Context, editWindow pgtype.Int4) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "UpdateBoardEditWindow(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.UpdateBoardEditWindow(ctx, editWindow)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int("board.edit_window", int(editWindow.Int32)),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "UpdateBoardEditWindow", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// UpdateBoardTitle implements the Querier interface with tracing
func (t *TracedQueriesWrapper) UpdateBoardTitle(ctx context.Context, title string) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "UpdateBoardTitle(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.UpdateBoardTitle(ctx, title)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.String("board.title", title),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "UpdateBoardTitle", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// UpdateMemberProfileByID implements the Querier interface with tracing
func (t *TracedQueriesWrapper) UpdateMemberProfileByID(ctx context.Context, arg UpdateMemberProfileByIDParams) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "UpdateMemberProfileByID(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.UpdateMemberProfileByID(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("member.id", arg.MemberID),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "UpdateMemberProfileByID", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// UpdateThread implements the Querier interface with tracing
func (t *TracedQueriesWrapper) UpdateThread(ctx context.Context, arg UpdateThreadParams) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "UpdateThread(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.UpdateThread(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("thread.id", arg.ID),
		attribute.Int64("member.id", arg.MemberID),
		attribute.String("thread.subject", arg.Subject),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "UpdateThread", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// UpdateThreadPost implements the Querier interface with tracing
func (t *TracedQueriesWrapper) UpdateThreadPost(ctx context.Context, arg UpdateThreadPostParams) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "UpdateThreadPost(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.UpdateThreadPost(ctx, arg)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("threadpost.id", arg.ID),
		attribute.Int64("member.id", arg.MemberID),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "UpdateThreadPost", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}

// BlockMember implements the Querier interface with tracing
func (t *TracedQueriesWrapper) BlockMember(ctx context.Context, id int64) error {
	ctx, span := t.telemetry.Tracer.Start(ctx, "BlockMember(query)")
	defer span.End()

	start := time.Now()
	err := t.wrapped.BlockMember(ctx, id)
	duration := time.Since(start).Seconds()

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("query error: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("member.id", id),
		attribute.Float64("request.duration", duration),
	)

	t.recordMetrics(ctx, "BlockMember", duration)
	span.SetStatus(codes.Ok, "")

	return nil
}
