package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// AuthProvider handles authentication logic
type AuthProvider interface {
	GetUserEmail(r *http.Request) (string, error)
	CreateOrGetUser(ctx context.Context, email string) (*ContextUser, error)
}

// TailscaleAuthProvider implements AuthProvider for Tailscale
type TailscaleAuthProvider struct {
	client  TailscaleClient
	queries Querier
	logger  *slog.Logger
}

// newTailscaleAuthProvider creates a new Tailscale auth provider
func newTailscaleAuthProvider(client TailscaleClient, queries Querier, logger *slog.Logger) *TailscaleAuthProvider {
	return &TailscaleAuthProvider{
		client:  client,
		queries: queries,
		logger:  logger,
	}
}

// GetUserEmail gets the user's email from Tailscale
func (p *TailscaleAuthProvider) GetUserEmail(r *http.Request) (string, error) {
	who, err := p.client.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get WhoIs: %w", err)
	}

	if who.UserProfile == nil || who.UserProfile.LoginName == "" {
		return "", fmt.Errorf("no user profile in WhoIs response")
	}

	return who.UserProfile.LoginName, nil
}

// CreateOrGetUser creates or retrieves a user from the database
func (p *TailscaleAuthProvider) CreateOrGetUser(ctx context.Context, email string) (*ContextUser, error) {
	user, err := p.queries.CreateOrReturnID(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to create or get user: %w", err)
	}

	return &ContextUser{
		ID:        user.ID,
		Email:     email,
		IsAdmin:   user.IsAdmin,
		IsBlocked: user.IsBlocked,
	}, nil
}

// authMiddleware provides authentication using the given provider
func authMiddleware(provider AuthProvider, tracer trace.Tracer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Start span for auth
			if tracer != nil {
				var span trace.Span
				ctx, span = tracer.Start(ctx, "auth.middleware",
					trace.WithAttributes(
						attribute.String("auth.provider", "tailscale"),
					),
				)
				defer span.End()
			}

			// Get user email
			email, err := provider.GetUserEmail(r)
			if err != nil {
				logger := getLogger(ctx)
				logger.ErrorContext(ctx, "failed to get user email",
					slog.String("error", err.Error()),
					slog.String("remote_addr", r.RemoteAddr),
				)

				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.RecordError(err)
					span.SetStatus(codes.Error, "authentication failed")
				}

				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}

			// Get or create user
			user, err := provider.CreateOrGetUser(ctx, email)
			if err != nil {
				logger := getLogger(ctx)
				logger.ErrorContext(ctx, "failed to create or get user",
					slog.String("error", err.Error()),
					slog.String("email_hash", hashEmail(email)),
				)

				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.RecordError(err)
					span.SetStatus(codes.Error, "user lookup failed")
				}

				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}

			// Check if user is blocked
			if user.IsBlocked {
				logger := getLogger(ctx)
				logger.WarnContext(ctx, "blocked user attempted access",
					slog.Int64("user_id", user.ID),
					slog.String("email_hash", hashEmail(email)),
				)

				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.SetStatus(codes.Error, "user is blocked")
					span.SetAttributes(
						attribute.Bool("user.is_blocked", true),
					)
				}

				// Return 404 as requested
				http.NotFound(w, r)
				return
			}

			// Add user to context
			rc := getOrCreateRequestContext(ctx)
			rc.User = user

			// Add attributes to span
			if span := trace.SpanFromContext(ctx); span.IsRecording() {
				span.SetAttributes(
					attribute.Int64("user.id", user.ID),
					attribute.Bool("user.is_admin", user.IsAdmin),
				)
			}

			// Log successful auth
			logger := getLogger(ctx)
			logger.DebugContext(ctx, "user authenticated",
				slog.Int64("user_id", user.ID),
				slog.Bool("is_admin", user.IsAdmin),
			)

			next.ServeHTTP(w, r)
		})
	}
}

// requireAuthMiddleware ensures the user is authenticated
func requireAuthMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAuthenticated(r) {
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAdminMiddleware ensures the user is an admin
func requireAdminMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAdmin(r) {
				logger := getLogger(r.Context())
				user, _ := getUser(r.Context())
				if user != nil {
					logger.WarnContext(r.Context(), "non-admin user attempted admin action",
						slog.Int64("user_id", user.ID),
						slog.String("path", r.URL.Path),
					)
				}
				http.Error(w, "Admin access required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SessionMiddleware provides session management
type SessionMiddleware struct {
	cookieName string
	secure     bool
	sameSite   http.SameSite
}

// newSessionMiddleware creates a new session middleware
func newSessionMiddleware(cookieName string, secure bool) *SessionMiddleware {
	return &SessionMiddleware{
		cookieName: cookieName,
		secure:     secure,
		sameSite:   http.SameSiteLaxMode,
	}
}

// Middleware returns the session middleware function
func (s *SessionMiddleware) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get or create session ID
			var sessionID string
			if cookie, err := r.Cookie(s.cookieName); err == nil {
				sessionID = cookie.Value
			} else {
				// Generate new session ID
				sessionID = generateSessionID()

				// Set cookie
				http.SetCookie(w, &http.Cookie{
					Name:     s.cookieName,
					Value:    sessionID,
					Path:     "/",
					HttpOnly: true,
					Secure:   s.secure || isHTTPS(r),
					SameSite: s.sameSite,
					MaxAge:   86400 * 30, // 30 days
				})
			}

			// Add session ID to context
			rc := getOrCreateRequestContext(r.Context())
			rc.Set("session_id", sessionID)

			next.ServeHTTP(w, r)
		})
	}
}

func generateSessionID() string {
	return generateCSRFToken() // Reuse the secure random generation
}

// userEnrichmentMiddleware adds user information to all handlers
// This should run after authMiddleware
func userEnrichmentMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user from context
			user, ok := getUser(r.Context())
			if ok && user != nil {
				// Add user info to logger
				logger := getLogger(r.Context())
				enrichedLogger := logger.With(
					slog.Int64("user_id", user.ID),
					slog.Bool("is_admin", user.IsAdmin),
				)

				// Update logger in context
				ctx := context.WithValue(r.Context(), contextKey("logger"), enrichedLogger)
				r = r.WithContext(ctx)

				// Add user info to response headers (for debugging)
				if isDebugMode() {
					w.Header().Set("X-User-ID", fmt.Sprintf("%d", user.ID))
					w.Header().Set("X-User-Admin", fmt.Sprintf("%t", user.IsAdmin))
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isDebugMode() bool {
	// TODO: Check debug mode from config
	return false
}

// hashEmail creates a hash of an email for privacy-preserving logging
func hashEmail(email string) string {
	if email == "" {
		return "empty"
	}
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:8]) // First 8 bytes for brevity
}
