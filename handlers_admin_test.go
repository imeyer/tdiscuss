package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// newTestTelemetry returns a TelemetryConfig backed by no-op providers.
func newTestTelemetry() *TelemetryConfig {
	return &TelemetryConfig{
		Tracer: tracenoop.NewTracerProvider().Tracer("test"),
		Meter:  metricnoop.NewMeterProvider().Meter("test"),
	}
}

// whoIsFor builds an untagged WhoIs response for a login name, which is what
// the auth middleware requires to resolve a member identity.
func whoIsFor(login string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{Name: "member.tailnet.ts.net."},
		UserProfile: &tailcfg.UserProfile{LoginName: login},
	}
}

// newAdminTestHandler wires the real admin middleware chain in front of the
// real Admin handler, so these tests exercise the authorization path rather
// than calling the handler directly.
func newAdminTestHandler(t *testing.T, login string, queries ExtendedQuerier) http.Handler {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	telemetry := newTestTelemetry()

	tailClient := &MockTailscaleClient{
		WhoIsFunc: func(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
			return whoIsFor(login), nil
		},
	}

	dsvc := NewDiscussService(
		tailClient,
		logger,
		nil, // the admin handlers use queries, not the pool directly
		queries,
		setupTemplates(),
		"test-host",
		"test",
		"testsha",
		telemetry,
	)

	authProvider := middleware.NewTailscaleAuthProvider(
		dsvc.tailClient,
		NewQuerierAdapter(dsvc.queries),
		logger,
		middleware.TailscaleAuthConfig{},
	)

	ms := middleware.NewMiddlewareSetup(logger, ConvertTelemetryConfig(telemetry), authProvider)
	ms.RateLimitConfig.Meter = telemetry.Meter

	return ms.CreateAdminChain().ThenFunc(dsvc.Admin)
}

// adminPost issues a POST to /admin through the chain.
func adminPost(t *testing.T, h http.Handler, form string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.RemoteAddr = "100.64.0.10:12345"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminPOST_SetMemberAdmin(t *testing.T) {
	tests := []struct {
		name            string
		form            string
		expectedStatus  int
		expectedCalled  bool
		expectedIsAdmin bool
		expectedID      int64
	}{
		{
			name:            "promote another member",
			form:            "action=set_admin&member_id=42",
			expectedStatus:  http.StatusSeeOther,
			expectedCalled:  true,
			expectedIsAdmin: true,
			expectedID:      42,
		},
		{
			name:            "demote another member",
			form:            "action=unset_admin&member_id=42",
			expectedStatus:  http.StatusSeeOther,
			expectedCalled:  true,
			expectedIsAdmin: false,
			expectedID:      42,
		},
		{
			// The acting admin is member 1 (see CreateOrReturnID below).
			// Demoting yourself can strand a board with no database admin.
			name:           "refuses to change own admin status",
			form:           "action=unset_admin&member_id=1",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "refuses to promote self",
			form:           "action=set_admin&member_id=1",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "rejects a missing member id",
			form:           "action=set_admin",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "rejects a non-numeric member id",
			form:           "action=set_admin&member_id=abc",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "rejects a zero member id",
			form:           "action=set_admin&member_id=0",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			var got SetMemberAdminParams

			queries := &MockQueries{
				CreateOrReturnIDFunc: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{ID: 1, IsAdmin: true}, nil
				},
				SetMemberAdminFunc: func(_ context.Context, arg SetMemberAdminParams) error {
					called = true
					got = arg
					return nil
				},
			}

			h := newAdminTestHandler(t, "admin@example.com", queries)
			rec := adminPost(t, h, tt.form)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			require.Equal(t, tt.expectedCalled, called, "SetMemberAdmin call")

			if tt.expectedCalled {
				assert.Equal(t, tt.expectedID, got.ID)
				assert.Equal(t, tt.expectedIsAdmin, got.IsAdmin)
			}
		})
	}
}

func TestAdminPOST_SetMemberBlocked(t *testing.T) {
	tests := []struct {
		name              string
		form              string
		targetIsAdmin     bool
		targetLookupErr   error
		expectedStatus    int
		expectedCalled    bool
		expectedIsBlocked bool
		expectedID        int64
	}{
		{
			name:              "block another member",
			form:              "action=block_member&member_id=42",
			expectedStatus:    http.StatusSeeOther,
			expectedCalled:    true,
			expectedIsBlocked: true,
			expectedID:        42,
		},
		{
			name:              "unblock another member",
			form:              "action=unblock_member&member_id=42",
			expectedStatus:    http.StatusSeeOther,
			expectedCalled:    true,
			expectedIsBlocked: false,
			expectedID:        42,
		},
		{
			// The original action name never deleted anything; it blocked.
			name:              "legacy delete_member still blocks",
			form:              "action=delete_member&member_id=42",
			expectedStatus:    http.StatusSeeOther,
			expectedCalled:    true,
			expectedIsBlocked: true,
			expectedID:        42,
		},
		{
			// A blocked member is refused on every route, so blocking
			// yourself is an immediate, unrecoverable lockout.
			name:           "refuses to block self",
			form:           "action=block_member&member_id=1",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "refuses to unblock self",
			form:           "action=unblock_member&member_id=1",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "rejects a missing member id",
			form:           "action=block_member",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			name:           "rejects a non-numeric member id",
			form:           "action=unblock_member&member_id=abc",
			expectedStatus: http.StatusBadRequest,
			expectedCalled: false,
		},
		{
			// Blocking outranks admin, so this would be an admin-on-admin
			// lockout with no way back short of a SQL update.
			name:           "refuses to block another admin",
			form:           "action=block_member&member_id=42",
			targetIsAdmin:  true,
			expectedStatus: http.StatusForbidden,
			expectedCalled: false,
		},
		{
			name:           "legacy delete_member also refuses an admin",
			form:           "action=delete_member&member_id=42",
			targetIsAdmin:  true,
			expectedStatus: http.StatusForbidden,
			expectedCalled: false,
		},
		{
			// Unblocking an admin stays possible: someone blocked before this
			// rule existed still needs a way back.
			name:              "unblocking an admin is still allowed",
			form:              "action=unblock_member&member_id=42",
			targetIsAdmin:     true,
			expectedStatus:    http.StatusSeeOther,
			expectedCalled:    true,
			expectedIsBlocked: false,
			expectedID:        42,
		},
		{
			name:            "unknown member cannot be blocked",
			form:            "action=block_member&member_id=999",
			targetLookupErr: pgx.ErrNoRows,
			expectedStatus:  http.StatusBadRequest,
			expectedCalled:  false,
		},
		{
			name:            "lookup failure does not block anyone",
			form:            "action=block_member&member_id=42",
			targetLookupErr: errors.New("database is down"),
			expectedStatus:  http.StatusInternalServerError,
			expectedCalled:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			var got SetMemberBlockedParams

			queries := &MockQueries{
				CreateOrReturnIDFunc: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{ID: 1, IsAdmin: true}, nil
				},
				IsMemberAdminFunc: func(_ context.Context, _ int64) (bool, error) {
					return tt.targetIsAdmin, tt.targetLookupErr
				},
				SetMemberBlockedFunc: func(_ context.Context, arg SetMemberBlockedParams) error {
					called = true
					got = arg
					return nil
				},
			}

			h := newAdminTestHandler(t, "admin@example.com", queries)
			rec := adminPost(t, h, tt.form)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			require.Equal(t, tt.expectedCalled, called, "SetMemberBlocked call")

			if tt.expectedCalled {
				assert.Equal(t, tt.expectedID, got.ID)
				assert.Equal(t, tt.expectedIsBlocked, got.IsBlocked)
			}
		})
	}
}

// TestAdminPOST_NonAdminCannotBlock is the negative case for moderation: the
// admin chain must refuse a non-admin before the action is parsed.
func TestAdminPOST_NonAdminCannotBlock(t *testing.T) {
	var called bool

	queries := &MockQueries{
		CreateOrReturnIDFunc: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
			return CreateOrReturnIDRow{ID: 2, IsAdmin: false}, nil
		},
		IsMemberAdminFunc: func(_ context.Context, _ int64) (bool, error) {
			return false, nil
		},
		SetMemberBlockedFunc: func(_ context.Context, _ SetMemberBlockedParams) error {
			called = true
			return nil
		},
	}

	h := newAdminTestHandler(t, "nobody@example.com", queries)
	rec := adminPost(t, h, "action=block_member&member_id=42")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, called, "a non-admin must not be able to block anyone")
}

// TestAdminPOST_NonAdminCannotPromote is the important negative case: the
// admin chain must refuse a non-admin before the action is ever parsed.
func TestAdminPOST_NonAdminCannotPromote(t *testing.T) {
	var called bool

	queries := &MockQueries{
		CreateOrReturnIDFunc: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
			return CreateOrReturnIDRow{ID: 2, IsAdmin: false}, nil
		},
		SetMemberAdminFunc: func(_ context.Context, _ SetMemberAdminParams) error {
			called = true
			return nil
		},
	}

	h := newAdminTestHandler(t, "nobody@example.com", queries)
	rec := adminPost(t, h, "action=set_admin&member_id=42")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, called, "a non-admin must not be able to promote anyone")
}

// TestAdminGET_RendersMemberList also proves admin.html parses and executes
// with real row data, which template.Must would otherwise only reveal at
// startup or on the first request.
func TestAdminGET_RendersMemberList(t *testing.T) {
	queries := &MockQueries{
		CreateOrReturnIDFunc: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
			return CreateOrReturnIDRow{ID: 1, IsAdmin: true}, nil
		},
		ListMembersFunc: func(_ context.Context) ([]ListMembersRow, error) {
			return []ListMembersRow{
				{ID: 1, Email: "admin@example.com", IsAdmin: true},
				{ID: 42, Email: "other-admin@example.com", IsAdmin: true},
				{ID: 43, Email: "blocked@example.com", IsBlocked: true},
			}, nil
		},
	}

	h := newAdminTestHandler(t, "admin@example.com", queries)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.RemoteAddr = "100.64.0.10:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, "other-admin@example.com")
	assert.Contains(t, body, "blocked@example.com")

	// The acting admin's own row is marked and carries no actions at all.
	assert.Contains(t, body, `class="member-tag">you<`)

	// An existing admin is offered demotion, a plain member promotion.
	assert.Contains(t, body, `value="unset_admin">
                    <input type="hidden" name="member_id" value="42"`)
	assert.Contains(t, body, `value="set_admin">
                    <input type="hidden" name="member_id" value="43"`)

	// Member 43 is blocked, so it is offered unblocking.
	assert.Contains(t, body, `value="unblock_member">
                    <input type="hidden" name="member_id" value="43"`)

	// Member 42 is an admin, so no Block button is offered for it at all -
	// the server refuses that, and the UI must not invite it.
	assert.Contains(t, body, "cannot block an admin")
	assert.NotContains(t, body, `value="block_member"`)

	// Three forms total: demote 42, promote 43, unblock 43.
	assert.Equal(t, 3, strings.Count(body, `name="member_id"`),
		"self has no actions and an admin gets no block action")
}
