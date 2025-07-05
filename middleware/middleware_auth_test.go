package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations for testing
type mockTailscaleClient struct {
	email string
	err   error
}

func (m *mockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &WhoIsResponse{
		UserProfile: &UserProfile{
			LoginName: m.email,
		},
	}, nil
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
			// Setup mocks
			mockClient := &mockTailscaleClient{
				email: "test@example.com",
			}
			mockQueries := &mockQuerier{
				user: tt.user,
			}

			// Create auth provider
			provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger())

			// Create middleware
			middleware := authMiddleware(provider, nil)

			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			// Wrap handler with middleware
			wrapped := middleware(handler)

			// Create request and recorder
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			// Execute request
			wrapped.ServeHTTP(rec, req)

			// Assert response
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
			// Setup mocks
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

			// Create auth provider
			provider := newTailscaleAuthProvider(mockClient, mockQueries, NewTestLogger())

			// Create middleware
			middleware := authMiddleware(provider, nil)

			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			// Wrap handler with middleware
			wrapped := middleware(handler)

			// Create request and recorder
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			// Execute request
			wrapped.ServeHTTP(rec, req)

			// Assert response
			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestTailscaleAuthProvider_GetUserEmail(t *testing.T) {
	tests := []struct {
		name          string
		email         string
		err           error
		expectedEmail string
		expectedErr   bool
	}{
		{
			name:          "successful email retrieval",
			email:         "user@example.com",
			expectedEmail: "user@example.com",
			expectedErr:   false,
		},
		{
			name:        "whois error",
			err:         errors.New("network error"),
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockTailscaleClient{
				email: tt.email,
				err:   tt.err,
			}

			provider := newTailscaleAuthProvider(mockClient, nil, NewTestLogger())

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"

			email, err := provider.GetUserEmail(req)

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedEmail, email)
			}
		})
	}
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

			provider := newTailscaleAuthProvider(nil, mockQueries, NewTestLogger())

			email := "test@example.com"
			if tt.name == "blocked user" {
				email = "blocked@example.com"
			}

			user, err := provider.CreateOrGetUser(context.Background(), email)

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
		// Check if logger was enriched
		logger := getLogger(r.Context())
		require.NotNil(t, logger)

		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(handler)

	// Test with user in context
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
