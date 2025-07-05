package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// NewTestLogger creates a logger for testing
func NewTestLogger() *slog.Logger {
	return slog.Default()
}

func TestAdminPOST_BlockMember(t *testing.T) {
	tests := []struct {
		name             string
		formData         string
		setupMock        func(*MockQueries)
		expectedStatus   int
		expectedRedirect string
	}{
		{
			name:     "successful block",
			formData: "action=delete_member&member_id=123",
			setupMock: func(m *MockQueries) {
				m.BlockMemberFunc = func(ctx context.Context, id int64) error {
					if id != 123 {
						return errors.New("unexpected member ID")
					}
					return nil
				}
			},
			expectedStatus:   http.StatusSeeOther,
			expectedRedirect: "/",
		},
		{
			name:     "block member database error",
			formData: "action=delete_member&member_id=456",
			setupMock: func(m *MockQueries) {
				m.BlockMemberFunc = func(ctx context.Context, id int64) error {
					return errors.New("database error")
				}
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "invalid member_id",
			formData:       "action=delete_member&member_id=invalid",
			setupMock:      func(m *MockQueries) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "missing member_id",
			formData:       "action=delete_member",
			setupMock:      func(m *MockQueries) {},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock querier
			mockQ := &MockQueries{}
			tt.setupMock(mockQ)

			// Setup telemetry
			config, err := LoadConfig()
			if err != nil {
				t.Fatalf("failed to load config: %v", err)
			}
			config.TraceSampleRate = 0.0
			config.Logger = NewTestLogger()

			telemetry, cleanup, err := setupTelemetry(context.Background(), config)
			if err != nil {
				t.Fatalf("failed to setup telemetry: %v", err)
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cleanup(cleanupCtx)
			}()

			// Create mock Tailscale client that returns admin user
			mockTailscaleClient := &MockTailscaleClient{
				WhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "admin@example.com",
						},
					}, nil
				},
			}

			// Update the mock querier to return admin user
			mockQ.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
				return CreateOrReturnIDRow{
					ID:        1,
					IsAdmin:   true,
					IsBlocked: false,
				}, nil
			}

			// Create service
			service := &DiscussService{
				tailClient: mockTailscaleClient,
				queries:    mockQ,
				logger:     NewTestLogger(),
				telemetry:  telemetry,
			}

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/admin", strings.NewReader(tt.formData))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.RemoteAddr = "127.0.0.1:12345"

			// Create response recorder
			rec := httptest.NewRecorder()

			// Use UserMiddleware to set up proper authentication context
			handler := UserMiddleware(service, http.HandlerFunc(service.AdminPOST))
			handler.ServeHTTP(rec, req)

			// Assert response
			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedRedirect != "" {
				assert.Equal(t, tt.expectedRedirect, rec.Header().Get("Location"))
			}
		})
	}
}

func TestBlockedUserAccess(t *testing.T) {
	// This is an integration-style test that would test the full flow
	// In a real implementation, you would:
	// 1. Create a user
	// 2. Block the user
	// 3. Try to access the site as the blocked user
	// 4. Verify you get a 404

	// For now, this is a placeholder to show the test structure
	t.Skip("Integration test - requires full middleware setup")
}
