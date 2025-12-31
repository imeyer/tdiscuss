package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/imeyer/tdiscuss/middleware"
)

// TailscaleClientAdapter adapts the actual Tailscale client to the middleware interface
type TailscaleClientAdapter struct {
	client TailscaleClient
}

// NewTailscaleClientAdapter creates a new adapter
func NewTailscaleClientAdapter(client TailscaleClient) *TailscaleClientAdapter {
	return &TailscaleClientAdapter{client: client}
}

// WhoIs implements the middleware.TailscaleClient interface
func (a *TailscaleClientAdapter) WhoIs(ctx context.Context, remoteAddr string) (*middleware.WhoIsResponse, error) {
	resp, err := a.client.WhoIs(ctx, remoteAddr)
	if err != nil {
		return nil, err
	}

	// Convert the actual response to the middleware interface
	if resp.UserProfile == nil {
		return &middleware.WhoIsResponse{}, nil
	}

	return &middleware.WhoIsResponse{
		UserProfile: &middleware.UserProfile{
			LoginName: resp.UserProfile.LoginName,
		},
	}, nil
}

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
		ID:        user.ID,
		Email:     user.Email,
		IsAdmin:   user.IsAdmin,
		IsBlocked: user.IsBlocked,
	}, nil
}

