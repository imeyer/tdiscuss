package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// AuthProvider handles authentication logic
type AuthProvider interface {
	// ResolvePeer resolves the identity behind a request, and returns an
	// error if the peer has no identity this board can use.
	ResolvePeer(r *http.Request) (*Peer, error)

	// CreateOrGetUser creates or retrieves the member record for a peer.
	CreateOrGetUser(ctx context.Context, peer *Peer) (*ContextUser, error)
}

// TailscaleAuthConfig is the identity policy for TailscaleAuthProvider.
type TailscaleAuthConfig struct {
	// AllowSharedNodes permits requests from nodes shared into this tailnet
	// from another one.
	//
	// It is off by default. Such a peer's login name is issued by a foreign
	// tailnet's identity provider, its owner is not a member of this tailnet,
	// and accepting it silently enrolls an outsider as a board member.
	AllowSharedNodes bool
}

// TailscaleAuthProvider implements AuthProvider for Tailscale
type TailscaleAuthProvider struct {
	client  TailscaleClient
	queries Querier
	logger  *slog.Logger
	config  TailscaleAuthConfig
}

// newTailscaleAuthProvider creates a new Tailscale auth provider
func newTailscaleAuthProvider(client TailscaleClient, queries Querier, logger *slog.Logger, config TailscaleAuthConfig) *TailscaleAuthProvider {
	return &TailscaleAuthProvider{
		client:  client,
		queries: queries,
		logger:  logger,
		config:  config,
	}
}

// ResolvePeer resolves the tailnet identity behind the request.
//
// r.RemoteAddr is the peer's WireGuard-authenticated tailnet address, since
// every listener is a tsnet listener. It is the only trustworthy thing about
// the request, so it - and never a header - is what we hand to WhoIs.
func (p *TailscaleAuthProvider) ResolvePeer(r *http.Request) (*Peer, error) {
	who, err := p.client.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get WhoIs: %w", err)
	}
	if who == nil {
		return nil, errors.New("empty WhoIs response")
	}

	// The node is what carries the ACL tags, so without it we cannot tell
	// whether this peer has a human owner at all. A successful WhoIs always
	// reports one; fail closed if we somehow get a response that doesn't.
	if who.Node == nil {
		return nil, errors.New("WhoIs response has no node")
	}

	peer := peerFromWhoIs(who)

	// A tagged node has no human owner. WhoIs resolves the owner of the node,
	// and for a tagged node the control plane substitutes a synthetic account
	// that is identical for every tagged node in the tailnet - so accepting
	// one would file all of them under a single shared member, and on an
	// empty board would hand that shared member admin (see createOrReturnID).
	// The tags are the reliable signal here, not the synthetic login name.
	if peer.IsTagged() {
		return nil, fmt.Errorf("node %q is tagged (%s) and has no user identity",
			peer.NodeName, strings.Join(peer.Tags, ","))
	}

	if peer.Shared && !p.config.AllowSharedNodes {
		return nil, fmt.Errorf("node %q is shared in from another tailnet", peer.NodeName)
	}

	if peer.LoginName == "" {
		return nil, errors.New("no user profile in WhoIs response")
	}

	return peer, nil
}

// CreateOrGetUser creates or retrieves a user from the database and resolves
// the member's effective admin status.
//
// Admin is the union of two sources: the member's is_admin column and an admin
// role granted by the tailnet policy file (see BoardCapability). The union is
// deliberate - it lets grants be adopted without stranding an existing
// database admin - but it means revoking admin requires clearing every source
// that grants it.
func (p *TailscaleAuthProvider) CreateOrGetUser(ctx context.Context, peer *Peer) (*ContextUser, error) {
	user, err := p.queries.CreateOrReturnID(ctx, peer.LoginName)
	if err != nil {
		return nil, fmt.Errorf("failed to create or get user: %w", err)
	}

	// A malformed grant grants nothing. Failing the request instead would let
	// one policy-file typo take the whole board down.
	grantedAdmin, err := peerGrantsAdmin(peer)
	if err != nil {
		p.logger.WarnContext(ctx, "ignoring malformed board capability grant",
			slog.String("error", err.Error()),
			slog.String("node", peer.NodeName),
			slog.String("email_hash", HashEmail(peer.LoginName)),
		)
		grantedAdmin = false
	}

	return &ContextUser{
		ID:             user.ID,
		Email:          peer.LoginName,
		IsAdmin:        user.IsAdmin || grantedAdmin,
		IsAdminByGrant: grantedAdmin,
		IsBlocked:      user.IsBlocked,
	}, nil
}

// authMiddleware provides authentication using the given provider
func authMiddleware(provider AuthProvider, tracer trace.Tracer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if tracer != nil {
				var span trace.Span
				ctx, span = tracer.Start(ctx, "auth.middleware",
					trace.WithAttributes(
						attribute.String("auth.provider", "tailscale"),
					),
				)
				defer span.End()
			}

			peer, err := provider.ResolvePeer(r)
			if err != nil {
				logger := getLogger(ctx)
				logger.WarnContext(ctx, "authentication failed",
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

			user, err := provider.CreateOrGetUser(ctx, peer)
			if err != nil {
				logger := getLogger(ctx)
				logger.ErrorContext(ctx, "failed to create or get user",
					slog.String("error", err.Error()),
					slog.String("email_hash", HashEmail(peer.LoginName)),
					slog.String("node", peer.NodeName),
				)

				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.RecordError(err)
					span.SetStatus(codes.Error, "user lookup failed")
				}

				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}

			if user.IsBlocked {
				logger := getLogger(ctx)
				logger.WarnContext(ctx, "blocked user attempted access",
					slog.Int64("user_id", user.ID),
					slog.String("email_hash", HashEmail(peer.LoginName)),
					slog.String("node", peer.NodeName),
				)

				if span := trace.SpanFromContext(ctx); span.IsRecording() {
					span.SetStatus(codes.Error, "user is blocked")
					span.SetAttributes(
						attribute.Bool("user.is_blocked", true),
					)
				}

				http.NotFound(w, r)
				return
			}

			// Add user and peer to context. The peer is kept so that handlers
			// can read capability grants without a second WhoIs round trip.
			rc := getOrCreateRequestContext(ctx)
			rc.User = user
			rc.Peer = peer

			if span := trace.SpanFromContext(ctx); span.IsRecording() {
				span.SetAttributes(
					attribute.Int64("user.id", user.ID),
					attribute.Bool("user.is_admin", user.IsAdmin),
					attribute.Bool("user.is_admin_by_grant", user.IsAdminByGrant),
				)
			}

			logger := getLogger(ctx)
			logger.DebugContext(ctx, "user authenticated",
				slog.Int64("user_id", user.ID),
				slog.Bool("is_admin", user.IsAdmin),
				slog.Bool("is_admin_by_grant", user.IsAdminByGrant),
				slog.String("node", peer.NodeName),
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

// userEnrichmentMiddleware adds user information to all handlers
// This should run after authMiddleware
func userEnrichmentMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := getUser(r.Context())
			if ok && user != nil {
				logger := getLogger(r.Context())
				enrichedLogger := logger.With(
					slog.Int64("user_id", user.ID),
					slog.Bool("is_admin", user.IsAdmin),
				)

				ctx := context.WithValue(r.Context(), contextKey("logger"), enrichedLogger)
				r = r.WithContext(ctx)

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

