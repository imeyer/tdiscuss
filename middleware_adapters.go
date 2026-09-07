package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/imeyer/tdiscuss/middleware"
)

// Note: there is no adapter for the Tailscale client. middleware.TailscaleClient
// takes the LocalAPI response type directly, so TailscaleClient satisfies it as
// is. The adapter that used to live here narrowed the WhoIs response to a login
// name, which discarded the node and its capability grants - and with them any
// way for the auth code to tell a person from a tagged machine.

// QuerierAdapter adapts the actual database querier to the middleware interface
type QuerierAdapter struct {
	queries Querier
}

// NewQuerierAdapter creates a new adapter
func NewQuerierAdapter(queries Querier) *QuerierAdapter {
	return &QuerierAdapter{queries: queries}
}

// CreateOrReturnID implements the middleware.Querier interface
func (a *QuerierAdapter) CreateOrReturnID(ctx context.Context, email string) (middleware.CreateOrReturnIDRow, error) {
	row, err := a.queries.CreateOrReturnID(ctx, email)
	if err != nil {
		return middleware.CreateOrReturnIDRow{}, err
	}

	return middleware.CreateOrReturnIDRow{
		ID:        row.ID,
		IsAdmin:   row.IsAdmin,
		IsBlocked: row.IsBlocked,
	}, nil
}

// GetBoardData implements the middleware.BoardDataQuerier interface
func (a *QuerierAdapter) GetBoardData(ctx context.Context) (interface{}, error) {
	boardData, err := a.queries.GetBoardData(ctx)
	if err != nil {
		return nil, err
	}
	// Return the board data as a value, not a pointer
	return boardData, nil
}

// ConvertTelemetryConfig converts the main TelemetryConfig to middleware.TelemetryConfig
func ConvertTelemetryConfig(tc *TelemetryConfig) *middleware.TelemetryConfig {
	if tc == nil {
		return nil
	}

	// Pass the actual OpenTelemetry types directly
	return &middleware.TelemetryConfig{
		Tracer: tc.Tracer,
		Meter:  tc.Meter,
		Metrics: middleware.TelemetryMetrics{
			RequestCounter:  tc.Metrics.RequestCounter,
			RequestDuration: tc.Metrics.RequestDuration,
			ErrorCounter:    tc.Metrics.ErrorCounter,
		},
	}
}

// GetUser is an adapter function that retrieves the user from the request context
// This maintains compatibility with the old code while using the new middleware
func GetUser(r *http.Request) (User, error) {
	user, ok := middleware.GetUser(r.Context())
	if !ok || user == nil {
		return User{}, errors.New("user not found in context")
	}

	return User{
		ID:             user.ID,
		Email:          user.Email,
		IsAdmin:        user.IsAdmin,
		IsAdminByGrant: user.IsAdminByGrant,
		IsBlocked:      user.IsBlocked,
	}, nil
}

