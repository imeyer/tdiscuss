package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

// boardCaps builds a CapMap carrying raw JSON grant values for
// BoardCapability.
func boardCaps(values ...string) tailcfg.PeerCapMap {
	raw := make([]tailcfg.RawMessage, 0, len(values))
	for _, v := range values {
		raw = append(raw, tailcfg.RawMessage(v))
	}
	return tailcfg.PeerCapMap{BoardCapability: raw}
}

func TestPeerGrantsAdmin(t *testing.T) {
	tests := []struct {
		name        string
		peer        *Peer
		expected    bool
		expectedErr bool
	}{
		{
			name:     "nil peer",
			peer:     nil,
			expected: false,
		},
		{
			name:     "no capabilities",
			peer:     &Peer{LoginName: "user@example.com"},
			expected: false,
		},
		{
			name:     "admin role granted",
			peer:     &Peer{CapMap: boardCaps(`{"role":"admin"}`)},
			expected: true,
		},
		{
			name:     "role comparison ignores case and space",
			peer:     &Peer{CapMap: boardCaps(`{"role":"  Admin "}`)},
			expected: true,
		},
		{
			// An older binary must not choke on a role a newer policy names.
			name:     "unknown role ignored",
			peer:     &Peer{CapMap: boardCaps(`{"role":"moderator"}`)},
			expected: false,
		},
		{
			name:     "admin among several rules",
			peer:     &Peer{CapMap: boardCaps(`{"role":"moderator"}`, `{"role":"admin"}`)},
			expected: true,
		},
		{
			name:     "empty rule object",
			peer:     &Peer{CapMap: boardCaps(`{}`)},
			expected: false,
		},
		{
			name: "grant for a different capability is ignored",
			peer: &Peer{CapMap: tailcfg.PeerCapMap{
				tailcfg.PeerCapability("example.com/cap/other"): []tailcfg.RawMessage{`{"role":"admin"}`},
			}},
			expected: false,
		},
		{
			name:        "malformed grant reports an error",
			peer:        &Peer{CapMap: boardCaps(`{"role":123}`)},
			expected:    false,
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := peerGrantsAdmin(tt.peer)

			if tt.expectedErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestCreateOrGetUser_AdminSources(t *testing.T) {
	tests := []struct {
		name                 string
		dbAdmin              bool
		capMap               tailcfg.PeerCapMap
		expectedAdmin        bool
		expectedAdminByGrant bool
	}{
		{
			name:          "neither source grants admin",
			expectedAdmin: false,
		},
		{
			name:          "database column alone grants admin",
			dbAdmin:       true,
			expectedAdmin: true,
		},
		{
			name:                 "policy grant alone grants admin",
			capMap:               boardCaps(`{"role":"admin"}`),
			expectedAdmin:        true,
			expectedAdminByGrant: true,
		},
		{
			name:                 "both sources grant admin",
			dbAdmin:              true,
			capMap:               boardCaps(`{"role":"admin"}`),
			expectedAdmin:        true,
			expectedAdminByGrant: true,
		},
		{
			// A policy-file typo must cost the grant, not the request.
			name:          "malformed grant grants nothing",
			capMap:        boardCaps(`{"role":123}`),
			expectedAdmin: false,
		},
		{
			// ...and must not strip an admin who has the database column.
			name:          "malformed grant leaves database admin intact",
			dbAdmin:       true,
			capMap:        boardCaps(`{"role":123}`),
			expectedAdmin: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockQueries := &mockQuerier{
				user: CreateOrReturnIDRow{ID: 7, IsAdmin: tt.dbAdmin},
			}
			provider := newTailscaleAuthProvider(nil, mockQueries, NewTestLogger(), TailscaleAuthConfig{})

			user, err := provider.CreateOrGetUser(context.Background(), &Peer{
				LoginName: "user@example.com",
				CapMap:    tt.capMap,
			})

			require.NoError(t, err)
			require.NotNil(t, user)
			assert.Equal(t, tt.expectedAdmin, user.IsAdmin)
			assert.Equal(t, tt.expectedAdminByGrant, user.IsAdminByGrant)
		})
	}
}

// TestAdminChain_GrantedByPolicy walks the whole chain: a member with no
// is_admin column but an admin grant must pass requireAdminMiddleware.
func TestAdminChain_GrantedByPolicy(t *testing.T) {
	tests := []struct {
		name           string
		capMap         tailcfg.PeerCapMap
		expectedStatus int
	}{
		{
			name:           "admin grant passes the admin chain",
			capMap:         boardCaps(`{"role":"admin"}`),
			expectedStatus: http.StatusOK,
		},
		{
			name:           "no grant is refused by the admin chain",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "non-admin role is refused by the admin chain",
			capMap:         boardCaps(`{"role":"moderator"}`),
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockTailscaleClient{
				email:  "user@example.com",
				capMap: tt.capMap,
			}
			// is_admin is false: the grant is the only possible source.
			mockQueries := &mockQuerier{user: CreateOrReturnIDRow{ID: 7}}

			provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger(), TailscaleAuthConfig{})

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			chain := newChain(
				requestContextMiddleware(),
				authMiddleware(provider, nil),
				requireAdminMiddleware(),
			)

			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.RemoteAddr = "100.64.0.3:12345"
			rec := httptest.NewRecorder()

			chain.Then(handler).ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}
