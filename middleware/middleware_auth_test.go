package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type mockTailscaleClient struct {
	email  string
	tags   []string
	sharer tailcfg.UserID
	capMap tailcfg.PeerCapMap
	noNode bool
	err    error
}

func (m *mockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	resp := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: m.email,
		},
		CapMap: m.capMap,
	}
	if !m.noNode {
		resp.Node = &tailcfg.Node{
			Name:   "node.tailnet.ts.net.",
			Tags:   m.tags,
			Sharer: m.sharer,
		}
	}
	return resp, nil
}

type mockQuerier struct {
	user CreateOrReturnIDRow
	err  error
}

func (m *mockQuerier) CreateOrReturnID(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
	if m.err != nil {
		return CreateOrReturnIDRow{}, m.err
	}
	return m.user, nil
}

func TestAuthMiddleware_BlockedUser(t *testing.T) {
	tests := []struct {
		name           string
		user           CreateOrReturnIDRow
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "blocked user gets 404",
			user: CreateOrReturnIDRow{
				ID:        1,
				IsAdmin:   false,
				IsBlocked: true,
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "404 page not found\n",
		},
		{
			name: "non-blocked user passes through",
			user: CreateOrReturnIDRow{
				ID:        2,
				IsAdmin:   false,
				IsBlocked: false,
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name: "admin user not blocked",
			user: CreateOrReturnIDRow{
				ID:        3,
				IsAdmin:   true,
				IsBlocked: false,
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockTailscaleClient{
				email: "test@example.com",
			}
			mockQueries := &mockQuerier{
				user: tt.user,
			}

			provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger(), TailscaleAuthConfig{})

			middleware := authMiddleware(provider, nil)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, tt.expectedBody, rec.Body.String())
		})
	}
}

func TestAuthMiddleware_Errors(t *testing.T) {
	tests := []struct {
		name           string
		clientErr      error
		querierErr     error
		expectedStatus int
	}{
		{
			name:           "client error returns 401",
			clientErr:      errors.New("whois failed"),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "querier error returns 500",
			querierErr:     errors.New("database error"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockTailscaleClient{
				email: "test@example.com",
				err:   tt.clientErr,
			}
			mockQueries := &mockQuerier{
				user: CreateOrReturnIDRow{
					ID:        1,
					IsAdmin:   false,
					IsBlocked: false,
				},
				err: tt.querierErr,
			}

			provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger(), TailscaleAuthConfig{})

			middleware := authMiddleware(provider, nil)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestTailscaleAuthProvider_ResolvePeer(t *testing.T) {
	tests := []struct {
		name          string
		email         string
		tags          []string
		sharer        tailcfg.UserID
		noNode        bool
		config        TailscaleAuthConfig
		err           error
		expectedLogin string
		expectedErr   string
	}{
		{
			name:          "successful identity resolution",
			email:         "user@example.com",
			expectedLogin: "user@example.com",
		},
		{
			name:        "whois error",
			err:         errors.New("network error"),
			expectedErr: "network error",
		},
		{
			name:        "no user profile",
			email:       "",
			expectedErr: "no user profile",
		},
		{
			// Without a node we cannot check for tags, so fail closed.
			name:        "no node fails closed",
			email:       "user@example.com",
			noNode:      true,
			expectedErr: "no node",
		},
		{
			// A tagged node has no human owner: WhoIs reports a synthetic
			// account shared by every tagged node in the tailnet, so it must
			// never be auto-provisioned as a member.
			name:        "tagged node rejected",
			email:       "tagged-devices",
			tags:        []string{"tag:ci"},
			expectedErr: "is tagged",
		},
		{
			name:        "tagged node rejected regardless of login name",
			email:       "user@example.com",
			tags:        []string{"tag:k8s-operator", "tag:prom"},
			expectedErr: "is tagged",
		},
		{
			name:        "shared node rejected by default",
			email:       "outsider@other.example.com",
			sharer:      tailcfg.UserID(42),
			expectedErr: "shared in from another tailnet",
		},
		{
			name:          "shared node allowed when configured",
			email:         "outsider@other.example.com",
			sharer:        tailcfg.UserID(42),
			config:        TailscaleAuthConfig{AllowSharedNodes: true},
			expectedLogin: "outsider@other.example.com",
		},
		{
			// AllowSharedNodes must not weaken the tagged-node check.
			name:        "shared and tagged node still rejected",
			email:       "tagged-devices",
			tags:        []string{"tag:ci"},
			sharer:      tailcfg.UserID(42),
			config:      TailscaleAuthConfig{AllowSharedNodes: true},
			expectedErr: "is tagged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockTailscaleClient{
				email:  tt.email,
				tags:   tt.tags,
				sharer: tt.sharer,
				noNode: tt.noNode,
				err:    tt.err,
			}

			provider := newTailscaleAuthProvider(mockClient, nil, NewTestLogger(), tt.config)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "100.64.0.1:12345"

			peer, err := provider.ResolvePeer(req)

			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
				assert.Nil(t, peer)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, peer)
			assert.Equal(t, tt.expectedLogin, peer.LoginName)
			assert.Equal(t, "node.tailnet.ts.net", peer.NodeName)
			assert.False(t, peer.IsTagged())
		})
	}
}

// TestAuthMiddleware_TaggedNodeUnauthorized covers the whole chain: a tagged
// node must get a 401 and must never reach CreateOrReturnID, which would
// auto-provision the synthetic tagged-devices account as a member.
func TestAuthMiddleware_TaggedNodeUnauthorized(t *testing.T) {
	mockClient := &mockTailscaleClient{
		email: "tagged-devices",
		tags:  []string{"tag:prom"},
	}
	mockQueries := &countingQuerier{}

	provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger(), TailscaleAuthConfig{})
	wrapped := authMiddleware(provider, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be reached by a tagged node")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.2:12345"
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Zero(t, mockQueries.calls, "tagged node must not be provisioned as a member")
}

// countingQuerier records how many times a member lookup was attempted.
type countingQuerier struct {
	calls int
}

func (m *countingQuerier) CreateOrReturnID(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
	m.calls++
	return CreateOrReturnIDRow{ID: 1}, nil
}

func TestTailscaleAuthProvider_CreateOrGetUser(t *testing.T) {
	tests := []struct {
		name         string
		user         CreateOrReturnIDRow
		err          error
		expectedUser *ContextUser
		expectedErr  bool
	}{
		{
			name: "successful user creation",
			user: CreateOrReturnIDRow{
				ID:        1,
				IsAdmin:   false,
				IsBlocked: false,
			},
			expectedUser: &ContextUser{
				ID:        1,
				Email:     "test@example.com",
				IsAdmin:   false,
				IsBlocked: false,
			},
			expectedErr: false,
		},
		{
			name: "blocked user",
			user: CreateOrReturnIDRow{
				ID:        2,
				IsAdmin:   false,
				IsBlocked: true,
			},
			expectedUser: &ContextUser{
				ID:        2,
				Email:     "blocked@example.com",
				IsAdmin:   false,
				IsBlocked: true,
			},
			expectedErr: false,
		},
		{
			name:        "database error",
			err:         errors.New("database error"),
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockQueries := &mockQuerier{
				user: tt.user,
				err:  tt.err,
			}

			provider := newTailscaleAuthProvider(nil, mockQueries, NewTestLogger(), TailscaleAuthConfig{})

			email := "test@example.com"
			if tt.name == "blocked user" {
				email = "blocked@example.com"
			}

			user, err := provider.CreateOrGetUser(context.Background(), &Peer{LoginName: email})

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedUser, user)
			}
		})
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	middleware := requireAuthMiddleware()

	tests := []struct {
		name           string
		setupContext   func(context.Context) context.Context
		expectedStatus int
	}{
		{
			name: "authenticated user passes",
			setupContext: func(ctx context.Context) context.Context {
				rc := newRequestContext()
				rc.User = &ContextUser{
					ID:    1,
					Email: "test@example.com",
				}
				return withRequestContext(ctx, rc)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "unauthenticated user blocked",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(tt.setupContext(req.Context()))
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestRequireAdminMiddleware(t *testing.T) {
	middleware := requireAdminMiddleware()

	tests := []struct {
		name           string
		setupContext   func(context.Context) context.Context
		expectedStatus int
	}{
		{
			name: "admin user passes",
			setupContext: func(ctx context.Context) context.Context {
				rc := newRequestContext()
				rc.User = &ContextUser{
					ID:      1,
					Email:   "admin@example.com",
					IsAdmin: true,
				}
				return withRequestContext(ctx, rc)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "non-admin user blocked",
			setupContext: func(ctx context.Context) context.Context {
				rc := newRequestContext()
				rc.User = &ContextUser{
					ID:      2,
					Email:   "user@example.com",
					IsAdmin: false,
				}
				return withRequestContext(ctx, rc)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "unauthenticated user blocked",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(tt.setupContext(req.Context()))
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestUserEnrichmentMiddleware(t *testing.T) {
	middleware := userEnrichmentMiddleware()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := getLogger(r.Context())
		require.NotNil(t, logger)

		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rc := newRequestContext()
	rc.User = &ContextUser{
		ID:      1,
		Email:   "test@example.com",
		IsAdmin: true,
	}
	req = req.WithContext(withRequestContext(req.Context(), rc))
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
