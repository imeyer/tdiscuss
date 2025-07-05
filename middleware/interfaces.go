package middleware

import (
	"context"
	"net/http"
)

// TailscaleClient interface for Tailscale operations
type TailscaleClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResponse, error)
}

// WhoIsResponse represents the Tailscale WhoIs response
type WhoIsResponse struct {
	UserProfile *UserProfile
}

// UserProfile represents a Tailscale user profile
type UserProfile struct {
	LoginName string
}

// Querier interface for database operations
type Querier interface {
	CreateOrReturnID(ctx context.Context, email string) (CreateOrReturnIDRow, error)
}

// CreateOrReturnIDRow represents a user row from the database
type CreateOrReturnIDRow struct {
	ID        int64
	IsAdmin   bool
	IsBlocked bool
}

// Logger interface for structured logging
type Logger interface {
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	With(args ...any) Logger
}

// TelemetryConfig represents telemetry configuration
// This is a simplified version that references the actual types from the main package
type TelemetryConfig struct {
	ServiceName string
	Tracer      interface{} // Will be trace.Tracer
	Meter       interface{} // Will be metric.Meter
	Metrics     TelemetryMetrics
}

// TelemetryMetrics holds telemetry metrics
type TelemetryMetrics struct {
	RequestCounter  interface{} // Will be metric.Int64Counter
	RequestDuration interface{} // Will be metric.Float64Histogram
	ErrorCounter    interface{} // Will be metric.Int64Counter
}

// DiscussService interface for the main service
type DiscussService interface {
	ListThreads(w http.ResponseWriter, r *http.Request)
	ListThreadPosts(w http.ResponseWriter, r *http.Request)
	ListMember(w http.ResponseWriter, r *http.Request)
	NewThread(w http.ResponseWriter, r *http.Request)
	CreateThread(w http.ResponseWriter, r *http.Request)
	EditMemberProfile(w http.ResponseWriter, r *http.Request)
	EditThread(w http.ResponseWriter, r *http.Request)
	EditThreadPost(w http.ResponseWriter, r *http.Request)
	CreateThreadPost(w http.ResponseWriter, r *http.Request)
	Admin(w http.ResponseWriter, r *http.Request)
	ServeStatic(w http.ResponseWriter, r *http.Request)
	HealthCheck(w http.ResponseWriter, r *http.Request)
	MetricsHandler(w http.ResponseWriter, r *http.Request)
}
