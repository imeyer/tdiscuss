package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// UserMiddleware is a test helper that simulates user authentication for tests.
// It wraps the handler and ensures GetUser() will return a test user.
func UserMiddleware(ds *DiscussService, next http.Handler) http.Handler {
	// For tests, we create a simple middleware chain that sets up the context
	// and authentication state needed by the handlers
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handlers expect to be able to call GetUser(r) which relies on
		// the middleware having set up the context properly.
		// Since we can't easily replicate the full middleware stack in tests,
		// we'll mock the WhoIs call to return a test user.

		// Set up the mocked TailscaleClient to return a test user
		// Only set defaults if not already set by the test
		if mockClient, ok := ds.tailClient.(*MockTailscaleClient); ok {
			if mockClient.WhoIsFunc == nil {
				mockClient.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
			}
		}

		// Also set up the mock queries to return a user when CreateOrReturnID is called
		// Only set defaults if not already set by the test
		if mockQueries, ok := ds.queries.(*MockQueries); ok {
			if mockQueries.CreateOrReturnIDFunc == nil {
				mockQueries.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: false,
					}, nil
				}
			}
		}

		// Now we need to simulate what the real middleware chain does
		// The simplest approach is to create a minimal middleware chain
		chain := middleware.NewChain(
			middleware.RequestContextMiddleware(),
			middleware.AuthMiddleware(
				middleware.NewTailscaleAuthProvider(
					NewTailscaleClientAdapter(ds.tailClient),
					NewQuerierAdapter(ds.queries),
					ds.logger,
				),
				ds.telemetry.Tracer,
			),
		)

		// Apply the chain and then call the actual handler
		chain.Then(next).ServeHTTP(w, r)
	})
}

func TestEditThread(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()

	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		method             string
		url                string
		tid                string
		formData           url.Values
		setupMocks         func(*MockQueries, *MockTailscaleClient)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:     "Valid GET request",
			method:   "GET",
			url:      "/thread/1/edit",
			tid:      "1",
			formData: nil,
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadForEditFunc = func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
					return GetThreadForEditRow{
						Email:        pgtype.Text{String: "test@example.com", Valid: true},
						Body:         pgtype.Text{String: "Original bbnody", Valid: true},
						ThreadPostID: pgtype.Int8{Valid: true, Int64: 2},
						Subject:      "Test SSSSASASubject",
					}, nil
				}
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: true,
					}, nil
				}
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Original body",
		},
		{
			name:   "Valid POST request",
			method: "POST",
			url:    "/thread/1/edit",
			tid:    "1",
			formData: url.Values{
				"subject":     {"Updated subject for testing"},
				"thread_body": {"Updated body with enough content to pass validation"},
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: true,
					}, nil
				}
				mq.GetThreadForEditFunc = func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
					return GetThreadForEditRow{
						Email:        pgtype.Text{String: "test@example.com", Valid: true},
						Body:         pgtype.Text{String: "Original body", Valid: true},
						ThreadPostID: pgtype.Int8{Valid: true, Int64: 2},
						Subject:      "Test Subject",
					}, nil
				}
				mq.UpdateThreadPostFunc = func(ctx context.Context, arg UpdateThreadPostParams) error {
					return nil
				}
				mq.UpdateThreadFunc = func(ctx context.Context, arg UpdateThreadParams) error {
					return nil
				}
			},
			expectedStatusCode: http.StatusSeeOther,
			expectedBody:       "",
		},
		{
			name:               "Invalid method",
			method:             "PUT",
			url:                "/thread/1/edit",
			tid:                "1",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusMethodNotAllowed,
			expectedBody:       http.StatusText(http.StatusMethodNotAllowed),
		},
		{
			name:               "Invalid path",
			method:             "POST",
			url:                "/thread/1/",
			tid:                "1",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:               "Invalid thread ID",
			method:             "POST",
			url:                "/thread/invalid/edit",
			tid:                "invalid",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:   "Parse form error",
			method: "POST",
			url:    "/thread/1/edit",
			tid:    "1",
			formData: url.Values{
				"thread_body": {"Valid body"},
				"subject":     {"Valid subject"},
			},
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries and tailscale client for each test
			mockQueries.CreateOrReturnIDFunc = nil
			mockQueries.UpdateThreadFunc = nil
			mockQueries.UpdateThreadPostFunc = nil
			mockQueries.GetThreadForEditFunc = nil
			mockTailscaleClient.WhoIsFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries, mockTailscaleClient)

			mockQueries.GetThreadForEditFunc = func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
				return GetThreadForEditRow{
					Email:        pgtype.Text{String: "test@example.com", Valid: true},
					Body:         pgtype.Text{String: "Original body", Valid: true},
					ThreadPostID: pgtype.Int8{Valid: true, Int64: 2},
					Subject:      "Test Subject",
					ThreadID:     arg.ID,
				}, nil
			}

			var req *http.Request
			var err error
			// Add form data if present
			if tt.formData != nil {
				req, err = http.NewRequest(tt.method, tt.url, strings.NewReader(tt.formData.Encode()))
				assert.Nil(t, err)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req, err = http.NewRequest(tt.method, tt.url, nil)
				assert.Nil(t, err)
			}

			// Corrupt the form data to trigger a parse error
			if tt.name == "Parse form error" {
				req.Body = io.NopCloser(strings.NewReader("%gh&%ij"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}

			req.SetPathValue("tid", tt.tid)

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the handler
			handler := UserMiddleware(ds, http.HandlerFunc(ds.EditThread))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			if tt.expectedBody != "" {
				t.Log(tt)
				assert.Contains(t, rr.Body.String(), tt.expectedBody)
			}

			t.Logf("Test: %s, Status: %d, Body: %s", tt.name, rr.Code, rr.Body.String())
		})
	}
}

func TestEditThreadPost(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()

	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()

	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		method             string
		url                string
		tid                string
		pid                string
		formData           url.Values
		setupMocks         func(*MockQueries, *MockTailscaleClient)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:   "Valid request",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: url.Values{
				"thread_body": {"Updated body"},
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: true,
					}, nil
				}
				mq.GetThreadPostForEditFunc = func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
					return GetThreadPostForEditRow{
						ID:   2,
						Body: pgtype.Text{String: "Original body", Valid: true},
					}, nil
				}
				mq.UpdateThreadPostFunc = func(ctx context.Context, arg UpdateThreadPostParams) error {
					return nil
				}
			},
			expectedStatusCode: http.StatusSeeOther,
			expectedBody:       "",
		},
		{
			name:               "Invalid method",
			method:             "PUT",
			url:                "/thread/1/2/edit",
			tid:                "1",
			pid:                "2",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusMethodNotAllowed,
			expectedBody:       http.StatusText(http.StatusMethodNotAllowed),
		},
		{
			name:               "Invalid path",
			method:             "POST",
			url:                "/thread/1/edit",
			tid:                "1",
			pid:                "edit",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:               "Invalid thread ID",
			method:             "POST",
			url:                "/thread/invalid/2/edit",
			tid:                "invalid",
			pid:                "2",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:               "No thread ID",
			method:             "POST",
			url:                "/thread//2/edit",
			tid:                "",
			pid:                "2",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:               "No thread post ID",
			method:             "POST",
			url:                "/thread/1//edit",
			tid:                "1",
			pid:                "",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:               "Invalid post ID",
			method:             "POST",
			url:                "/thread/1/invalid/edit",
			tid:                "1",
			pid:                "invalid",
			formData:           nil,
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:   "Parse form error",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: url.Values{
				"thread_body": {""}, // Empty body to trigger validation error
			},
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "body: is required",
		},
		{
			name:   "Authentication failure returns 401",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: url.Values{
				"thread_body": {"Updated body"},
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return nil, errors.New("WhoIs error")
				}
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Authentication required",
		},
		{
			name:   "Database error during user lookup returns 500",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: url.Values{
				"thread_body": {"Updated body"},
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{ID: 0, IsAdmin: false}, errors.New("CreateOrReturnID error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       "Internal error",
		},
		{
			name:   "UpdateThreadPost error",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: url.Values{
				"thread_body": {"Updated body"},
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: true,
					}, nil
				}
				mq.UpdateThreadPostFunc = func(ctx context.Context, arg UpdateThreadPostParams) error {
					return errors.New("UpdateThreadPost error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries and tailscale client for each test
			mockQueries.CreateOrReturnIDFunc = nil
			mockQueries.UpdateThreadPostFunc = nil
			mockTailscaleClient.WhoIsFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries, mockTailscaleClient)

			mockQueries.GetThreadPostForEditFunc = func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
				return GetThreadPostForEditRow{
					ID:   arg.ID,
					Body: pgtype.Text{String: "Original body", Valid: true},
				}, nil
			}

			// Create a new HTTP request
			req, err := http.NewRequest(tt.method, tt.url, nil)
			assert.Nil(t, err)

			req.SetPathValue("tid", tt.tid)
			req.SetPathValue("pid", tt.pid)

			// Add form data if present
			if tt.formData != nil {
				req.PostForm = tt.formData
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the handler
			handler := UserMiddleware(ds, http.HandlerFunc(ds.EditThreadPost))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			if tt.expectedBody != "" {
				assert.Contains(t, rr.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestEditThreadPostGET(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()

	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		threadID           int64
		postID             int64
		setupMocks         func(*MockQueries, *MockTailscaleClient)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:     "Valid request",
			threadID: 1,
			postID:   2,
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadPostForEditFunc = func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
					return GetThreadPostForEditRow{
						ID:   arg.ID,
						Body: pgtype.Text{String: "Mock Body", Valid: true},
					}, nil
				}
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Mock Body",
		},
		{
			name:     "Authentication failure returns 401",
			threadID: 1,
			postID:   2,
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return nil, errors.New("WhoIs error")
				}
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Authentication required",
		},
		{
			name:     "GetThreadPostForEdit error",
			threadID: 1,
			postID:   2,
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadPostForEditFunc = func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
					return GetThreadPostForEditRow{}, errors.New("GetThreadPostForEdit error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries and tailscale client for each test
			mockQueries.GetThreadPostForEditFunc = nil
			mockTailscaleClient.WhoIsFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries, mockTailscaleClient)

			// Create a new HTTP request
			req, err := http.NewRequest("GET", fmt.Sprintf("/thread/%d/%d/edit", tt.threadID, tt.postID), nil)
			assert.Nil(t, err)

			req.SetPathValue("tid", strconv.FormatInt(tt.threadID, 10))
			req.SetPathValue("pid", strconv.FormatInt(tt.postID, 10))

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the handler
			handler := UserMiddleware(ds, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ds.editThreadPostGET(w, r)
			}))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			if tt.expectedBody != "" {
				assert.Contains(t, rr.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestGetTailscaleUserEmail(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		mockWhoIsFunc  func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
		expectedEmail  string
		expectedErr    error
		expectedLogMsg string
	}{
		{
			name:       "Valid email",
			remoteAddr: "127.0.0.1:12345",
			mockWhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return &apitype.WhoIsResponse{
					UserProfile: &tailcfg.UserProfile{
						LoginName: "test@example.com",
					},
				}, nil
			},
			expectedEmail:  "test@example.com",
			expectedErr:    nil,
			expectedLogMsg: "get tailscale user email",
		},
		{
			name:       "WhoIs error",
			remoteAddr: "127.0.0.1:12345",
			mockWhoIsFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return nil, errors.New("WhoIs error")
			},
			expectedEmail:  "",
			expectedErr:    errors.New("WhoIs error"),
			expectedLogMsg: "get tailscale user email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTailscaleClient := &MockTailscaleClient{
				WhoIsFunc: tt.mockWhoIsFunc,
			}
			var logLevel slog.Level = slog.LevelInfo
			logger := newLogger(io.Discard, &logLevel)
			config, err := LoadConfig()
			if err != nil {
				t.Error(err)
			}
			config.TraceSampleRate = 0.0
			config.Logger = logger
			telemetry, cleanup, err := setupTelemetry(t.Context(), config)
			if err != nil {
				logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := cleanup(cleanupCtx); err != nil {
					logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
				}
			}()
			ds := &DiscussService{
				tailClient: mockTailscaleClient,
				logger:     logger,
				telemetry:  telemetry,
			}

			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.RemoteAddr = tt.remoteAddr

			email, err := ds.GetTailscaleUserEmail(req)

			assert.Equal(t, tt.expectedEmail, email)
			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetUser is commented out because it tests old implementation details
// that are no longer relevant with the new middleware system
/*
func TestGetUser(t *testing.T) {
	tests := []struct {
		name          string
		contextValues map[string]interface{}
		expectedUser  User
		expectedError error
	}{
		{
			name: "Valid user ID, email, and is_admin",
			contextValues: map[string]interface{}{
				"user_id":  int64(1),
				"email":    "test@example.com",
				"is_admin": true,
			},
			expectedUser: User{
				ID:      1,
				Email:   "test@example.com",
				IsAdmin: true,
			},
			expectedError: nil,
		},
		{
			name: "Missing user ID",
			contextValues: map[string]interface{}{
				"email": "test@example.com",
			},
			expectedUser:  User{},
			expectedError: errors.New("user_id not found in context or invalid type"),
		},
		{
			name: "Invalid user ID type",
			contextValues: map[string]interface{}{
				"user_id": "invalid",
				"email":   "test@example.com",
			},
			expectedUser:  User{},
			expectedError: errors.New("user_id not found in context or invalid type"),
		},
		{
			name: "Missing email",
			contextValues: map[string]interface{}{
				"user_id": int64(1),
			},
			expectedUser:  User{},
			expectedError: errors.New("email not found in context or invalid type"),
		},
		{
			name: "Invalid email type",
			contextValues: map[string]interface{}{
				"user_id": int64(1),
				"email":   123,
			},
			expectedUser:  User{},
			expectedError: errors.New("email not found in context or invalid type"),
		},
		{
			name: "Missing is_admin",
			contextValues: map[string]interface{}{
				"user_id": int64(1),
				"email":   "test@example.com",
			},
			expectedUser:  User{},
			expectedError: errors.New("is_admin not found in context or invalid type"),
		},
		{
			name: "Invalid is_admin type",
			contextValues: map[string]interface{}{
				"user_id":  int64(1),
				"email":    "test@example.com",
				"is_admin": "invalid",
			},
			expectedUser:  User{},
			expectedError: errors.New("is_admin not found in context or invalid type"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/", nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			req = req.WithContext(context.Background())

			for key, value := range tt.contextValues {
				req = req.WithContext(context.WithValue(req.Context(), key, value))
			}

			user, err := GetUser(req)

			assert.ObjectsAreEqual(tt.expectedUser, user)

			// If we expect an error, but the test fails or vice versa, print the error
			if (err != nil && tt.expectedError == nil) || (err == nil && tt.expectedError != nil) || (err != nil && tt.expectedError != nil && err.Error() != tt.expectedError.Error()) {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}
		})
	}
}
*/

func TestListMember(t *testing.T) {
	// Create mock instances
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		method             string
		url                string
		mid                string
		setupMocks         func(*MockQueries)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:   "Valid request",
			method: "GET",
			url:    "/member/1",
			mid:    "1",
			setupMocks: func(mq *MockQueries) {
				mq.GetMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
					return GetMemberRow{
						Email:    "test@example.com",
						Location: pgtype.Text{String: "Test Location", Valid: true},
						ID:       id,
					}, nil
				}
				mq.ListMemberThreadsFunc = func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
					return []ListMemberThreadsRow{
						{
							ThreadID:       1,
							DateLastPosted: pgtype.Timestamptz{Time: time.Now(), Valid: true},
							ID:             pgtype.Int8{Valid: true, Int64: 1},
							Email:          pgtype.Text{Valid: true, String: "test@example.com"},
							Subject:        "Test Subject",
						},
					}, nil
				}
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Test Location",
		},
		{
			name:   "Invalid member ID",
			method: "GET",
			url:    "/member/invalid",
			setupMocks: func(mq *MockQueries) {
				// No setup needed
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:   "Method not allowed",
			method: "POST",
			url:    "/member/1",
			mid:    "1",
			setupMocks: func(mq *MockQueries) {
				// No setup needed
			},
			expectedStatusCode: http.StatusMethodNotAllowed,
			expectedBody:       "Method Not Allowed\n",
		},
		{
			name:   "GetMember query error",
			method: "GET",
			url:    "/member/1",
			mid:    "1",
			setupMocks: func(mq *MockQueries) {
				mq.GetMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
					return GetMemberRow{}, errors.New("database error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
		{
			name:   "ListMemberThreads query error",
			method: "GET",
			url:    "/member/1",
			mid:    "1",
			setupMocks: func(mq *MockQueries) {
				mq.GetMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
					return GetMemberRow{
						Email:    "test@example.com",
						Location: pgtype.Text{String: "Test Location", Valid: true},
						ID:       id,
					}, nil
				}
				mq.ListMemberThreadsFunc = func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
					return nil, errors.New("database error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries for each test
			mockQueries.GetMemberFunc = nil
			mockQueries.ListMemberThreadsFunc = nil
			mockQueries.CreateOrReturnIDFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries)

			mockQueries.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
				return CreateOrReturnIDRow{
					ID:      1,
					IsAdmin: true,
				}, nil
			}

			// Create a new HTTP request
			req, err := http.NewRequest(tt.method, tt.url, nil)
			assert.Nil(t, err)

			req.SetPathValue("mid", tt.mid)

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the handler
			handler := UserMiddleware(ds, http.HandlerFunc(ds.ListMember))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			assert.Contains(t, rr.Body.String(), tt.expectedBody)
		})
	}
}

func TestListThreadPosts(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		method             string
		url                string
		tid                string
		setupMocks         func(*MockQueries, *MockTailscaleClient)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:   "Valid request",
			method: "GET",
			url:    "/thread/1",
			tid:    "1",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadSubjectByIdFunc = func(ctx context.Context, threadID int64) (string, error) {
					return "Test Subject", nil
				}
				mq.ListThreadPostsFunc = func(ctx context.Context, params ListThreadPostsParams) ([]ListThreadPostsRow, error) {
					return []ListThreadPostsRow{
						{
							ID:         1,
							DatePosted: pgtype.Timestamptz{Time: time.Now(), Valid: true},
							MemberID:   pgtype.Int8{Valid: true, Int64: 1},
							Email:      pgtype.Text{Valid: true, String: "test@example.com"},
							Body:       pgtype.Text{Valid: true, String: "Test Body"},
							ThreadID:   pgtype.Int8{Valid: true, Int64: 1},
							IsAdmin:    pgtype.Bool{Valid: true, Bool: false},
							CanEdit:    true,
						},
					}, nil
				}
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Test Body",
		},
		{
			name:   "Invalid thread ID",
			method: "GET",
			url:    "/thread/invalid",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				// No setup needed
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:   "Authentication failure returns 401",
			method: "GET",
			url:    "/thread/1",
			tid:    "1",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return nil, errors.New("WhoIs error")
				}
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Authentication required",
		},
		{
			name:   "GetThreadSubjectById query error",
			method: "GET",
			url:    "/thread/1",
			tid:    "1",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadSubjectByIdFunc = func(ctx context.Context, threadID int64) (string, error) {
					return "", errors.New("database error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
		{
			name:   "ListThreadPosts query error",
			method: "GET",
			url:    "/thread/1",
			tid:    "1",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mq.GetThreadSubjectByIdFunc = func(ctx context.Context, threadID int64) (string, error) {
					return "Test Subject", nil
				}
				mq.ListThreadPostsFunc = func(ctx context.Context, params ListThreadPostsParams) ([]ListThreadPostsRow, error) {
					return nil, errors.New("database error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries and tailscale client for each test
			mockQueries.GetThreadSubjectByIdFunc = nil
			mockQueries.ListThreadPostsFunc = nil
			mockQueries.CreateOrReturnIDFunc = nil
			mockTailscaleClient.WhoIsFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries, mockTailscaleClient)

			// Create a new HTTP request
			req, err := http.NewRequest(tt.method, tt.url, nil)
			assert.Nil(t, err)

			req.SetPathValue("tid", tt.tid)

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the handler
			handler := UserMiddleware(ds, http.HandlerFunc(ds.ListThreadPosts))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			assert.Contains(t, rr.Body.String(), tt.expectedBody)
		})
	}
}

func TestListThreads(t *testing.T) {
	// Create a mock DiscussService
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     &pgxpool.Pool{}, // Can be nil if not used directly
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	// Create a new HTTP request
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Add a remote address to simulate a client
	req.RemoteAddr = "127.0.0.1:12345"

	// Create a ResponseRecorder to capture the response
	rr := httptest.NewRecorder()

	// Call the handler
	handler := UserMiddleware(ds, http.HandlerFunc(ds.ListThreads))
	handler.ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code)

	assert.Contains(t, rr.Body.String(), ds.version)
}

func TestNewDiscussService(t *testing.T) {
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dbconn := &pgxpool.Pool{} // Mock or use a real connection if needed
	queries := &MockQueries{}
	tmpl := template.New("test")
	hostname := "example.com"
	version := "1.0"
	gitSha := "abc123"

	ds := NewDiscussService(mockTailscaleClient, logger, dbconn, queries, tmpl, hostname, version, gitSha, nil)

	assert.Equal(t, mockTailscaleClient, ds.tailClient, "expected tailClient to be %v, got %v", mockTailscaleClient, ds.tailClient)
	assert.Equal(t, logger, ds.logger, "expected logger to be %v, got %v", logger, ds.logger)
	assert.Equal(t, dbconn, ds.dbconn, "expected dbconn to be %v, got %v", dbconn, ds.dbconn)
	assert.Equal(t, queries, ds.queries, "expected queries to be %v, got %v", queries, ds.queries)
	assert.Equal(t, tmpl, ds.tmpls, "expected tmpls to be %v, got %v", tmpl, ds.tmpls)
	assert.Equal(t, version, ds.version, "expected version to be %v, got %v", version, ds.version)
	assert.Equal(t, gitSha, ds.gitSha, "expected gitSha to be %v, got %v", gitSha, ds.gitSha)
}

func TestUserMiddleware(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	config, err := LoadConfig()
	if err != nil {
		t.Error(err)
	}
	config.TraceSampleRate = 0.0
	config.Logger = logger
	telemetry, cleanup, err := setupTelemetry(t.Context(), config)
	if err != nil {
		logger.ErrorContext(t.Context(), "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(t.Context(), "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		version:    "1.0",
		gitSha:     "abc123",
		telemetry:  telemetry,
	}

	tests := []struct {
		name               string
		setupMocks         func(*MockQueries, *MockTailscaleClient)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name: "Valid request",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{
						ID:      1,
						IsAdmin: true,
					}, nil
				}
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "",
		},
		{
			name: "Tailscale authentication failure returns 401",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return nil, errors.New("WhoIs error")
				}
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Authentication required",
		},
		{
			name: "Database error during user lookup returns 500",
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (CreateOrReturnIDRow, error) {
					return CreateOrReturnIDRow{ID: 0, IsAdmin: false}, errors.New("CreateOrReturnID error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       "Internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock queries and tailscale client for each test
			mockQueries.CreateOrReturnIDFunc = nil
			mockTailscaleClient.WhoIsFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries, mockTailscaleClient)

			// Create a new HTTP request
			req, err := http.NewRequest("GET", "/", nil)
			assert.Nil(t, err)

			// Add a remote address to simulate a client
			req.RemoteAddr = "127.0.0.1:12345"

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Call the middleware
			handler := UserMiddleware(ds, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			handler.ServeHTTP(rr, req)

			// Check the status code
			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			// Check the response body
			if tt.expectedBody != "" {
				assert.Contains(t, rr.Body.String(), tt.expectedBody)
			}
		})
	}
}

// MockAddr is a mock implementation of the net.Addr interface
type MockAddr struct{}

func (m *MockAddr) Network() string {
	return "tcp"
}

func (m *MockAddr) String() string {
	return "mock address"
}

// osExit is a variable to allow os.Exit to be mocked in tests
var osExit = os.Exit

//
// Mocks
//

type MockTx struct{}

func (m *MockTx) Commit(ctx context.Context) error {
	return nil
}

func (m *MockTx) Rollback(ctx context.Context) error {
	return nil
}

type MockQueries struct {
	inTransaction             bool
	CreateOrReturnIDFunc      func(ctx context.Context, email string) (CreateOrReturnIDRow, error)
	CreateThreadFunc          func(ctx context.Context, arg CreateThreadParams) error
	GetBoardDataFunc          func(ctx context.Context) (GetBoardDataRow, error)
	GetMemberFunc             func(ctx context.Context, id int64) (GetMemberRow, error)
	GetThreadForEditFunc      func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error)
	GetThreadPostForEditFunc  func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error)
	GetThreadSequenceIdFunc   func(ctx context.Context) (int64, error)
	GetThreadSubjectByIdFunc  func(ctx context.Context, id int64) (string, error)
	ListMemberThreadsFunc     func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error)
	ListThreadPostsFunc       func(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error)
	UpdateBoardEditWindowFunc func(ctx context.Context, arg pgtype.Int4) error
	UpdateBoardTitleFunc      func(ctx context.Context, arg string) error
	UpdateThreadFunc          func(ctx context.Context, arg UpdateThreadParams) error
	UpdateThreadPostFunc      func(ctx context.Context, arg UpdateThreadPostParams) error
	BlockMemberFunc           func(ctx context.Context, id int64) error
}

func (m *MockQueries) CreateOrReturnID(ctx context.Context, pEmail string) (CreateOrReturnIDRow, error) {
	if m.CreateOrReturnIDFunc != nil {
		return m.CreateOrReturnIDFunc(ctx, pEmail)
	}

	return CreateOrReturnIDRow{
		ID:        1,
		IsAdmin:   true,
		IsBlocked: false,
	}, nil
}

func (m *MockQueries) CreateThread(ctx context.Context, arg CreateThreadParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) CreateThreadPost(ctx context.Context, arg CreateThreadPostParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) GetBoardData(ctx context.Context) (GetBoardDataRow, error) {
	if m.GetBoardDataFunc != nil {
		return m.GetBoardDataFunc(ctx)
	}

	// Mock implementation
	// Default to 900 second edit window
	return GetBoardDataRow{
		EditWindow: pgtype.Int4{Int32: 900, Valid: true},
		Title:      "Mock Board Title",
		ID:         1,
	}, nil
}

func (m *MockQueries) GetMember(ctx context.Context, id int64) (GetMemberRow, error) {
	if m.GetMemberFunc != nil {
		return m.GetMemberFunc(ctx, id)
	}

	// Mock implementation
	return GetMemberRow{
		Email:      "mock@example.com",
		Location:   pgtype.Text{String: "Mock Location", Valid: true},
		ID:         id,
		DateJoined: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PhotoUrl:   pgtype.Text{String: "http://mockxample.com/photo.jpg", Valid: true},
	}, nil
}

func (m *MockQueries) GetMemberId(ctx context.Context, email string) (int64, error) {
	// Mock implementation
	return 1, nil
}

func (m *MockQueries) GetThreadPostSequenceId(ctx context.Context) (int64, error) {
	// Mock implementation
	return 1, nil
}

func (m *MockQueries) GetThreadSequenceId(ctx context.Context) (int64, error) {
	// Mock implementation
	return 1, nil
}

func (m *MockQueries) GetThreadSubjectById(ctx context.Context, id int64) (string, error) {
	if m.GetThreadSubjectByIdFunc != nil {
		return m.GetThreadSubjectByIdFunc(ctx, id)
	}

	// Mock implementation
	return "Mock Thread Subject", nil
}

func (m *MockQueries) GetThreadForEdit(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
	if m.GetThreadForEditFunc != nil {
		return m.GetThreadForEditFunc(ctx, arg)
	}

	// Mock implementation
	return GetThreadForEditRow{
		Email:        pgtype.Text{String: "mock@example.com", Valid: true},
		Body:         pgtype.Text{String: "Mock Body", Valid: true},
		ThreadPostID: pgtype.Int8{Valid: true, Int64: arg.ID},
		Subject:      "Mock Subject",
		ThreadID:     arg.ID,
	}, nil
}

func (m *MockQueries) GetThreadPostForEdit(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
	if m.GetThreadPostForEditFunc != nil {
		return m.GetThreadPostForEditFunc(ctx, arg)
	}

	// Mock implementation
	return GetThreadPostForEditRow{
		ID:   arg.ID,
		Body: pgtype.Text{String: "Mock Body", Valid: true},
	}, nil
}

func (m *MockQueries) ListMemberThreads(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
	if m.ListMemberThreadsFunc != nil {
		return m.ListMemberThreadsFunc(ctx, memberID)
	}

	// Mock implementation
	return []ListMemberThreadsRow{
		// Populate with mock data
	}, nil
}

func (m *MockQueries) ListThreadPosts(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error) {
	if m.ListThreadPostsFunc != nil {
		return m.ListThreadPostsFunc(ctx, arg)
	}

	return []ListThreadPostsRow{
		// Populate with mock data
	}, nil
}

func (m *MockQueries) ListThreads(ctx context.Context, arg ListThreadsParams) ([]ListThreadsRow, error) {
	// Mock implementation
	return []ListThreadsRow{
		{
			ThreadID:       1,
			DateLastPosted: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ID:             pgtype.Int8{Int64: 1, Valid: true},
			Email:          pgtype.Text{String: "mock@example.com", Valid: true},
			Lastid:         pgtype.Int8{Int64: 2, Valid: true},
			Lastname:       pgtype.Text{String: "mocklast@example.com", Valid: true},
			Subject:        "Mock Subject",
			Posts:          pgtype.Int4{Int32: 5, Valid: true},
			Views:          pgtype.Int4{Int32: 100, Valid: true},
			Body:           pgtype.Text{String: "Mock Body", Valid: true},
			LastViewPosts:  0,
			Dot:            false,
			Sticky:         pgtype.Bool{Bool: false, Valid: true},
			Locked:         pgtype.Bool{Bool: false, Valid: true},
		},
	}, nil
}

func (m *MockQueries) UpdateBoardEditWindow(ctx context.Context, arg pgtype.Int4) error {
	if m.UpdateBoardEditWindowFunc != nil {
		return m.UpdateBoardEditWindowFunc(ctx, arg)
	}

	// Mock implementation
	return nil
}

func (m *MockQueries) UpdateBoardTitle(ctx context.Context, arg string) error {
	if m.UpdateBoardTitleFunc != nil {
		return m.UpdateBoardTitleFunc(ctx, arg)
	}

	// Mock implementation
	return nil
}

func (m *MockQueries) UpdateMemberProfileByID(ctx context.Context, arg UpdateMemberProfileByIDParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) UpdateThread(ctx context.Context, arg UpdateThreadParams) error {
	if m.UpdateThreadFunc != nil {
		return m.UpdateThreadFunc(ctx, arg)
	}

	// Mock implementation
	return nil
}

func (m *MockQueries) UpdateThreadPost(ctx context.Context, arg UpdateThreadPostParams) error {
	if m.UpdateThreadPostFunc != nil {
		return m.UpdateThreadPostFunc(ctx, arg)
	}

	// Mock implementation
	return nil
}

func (m *MockQueries) BlockMember(ctx context.Context, id int64) error {
	if m.BlockMemberFunc != nil {
		return m.BlockMemberFunc(ctx, id)
	}

	// Mock implementation
	return nil
}

func (m *MockQueries) WithTx(pgx.Tx) ExtendedQuerier {
	return &MockQueries{
		inTransaction: true,
	}
}

type MockTailscaleClient struct {
	WhoIsFunc         func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	ExpandSNINameFunc func(ctx context.Context, hostname string) (string, bool)
}

func (m *MockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.WhoIsFunc != nil {
		return m.WhoIsFunc(ctx, remoteAddr)
	}

	return &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "mock@example.com",
		},
	}, nil
}

func (m *MockTailscaleClient) ExpandSNIName(ctx context.Context, hostname string) (string, bool) {
	if m.ExpandSNINameFunc != nil {
		return m.ExpandSNINameFunc(ctx, hostname)
	}
	return "", false
}

func (m *MockTailscaleClient) Status(ctx context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{
		CertDomains: []string{"tsnet.example.com"},
	}, nil
}

func (m *MockTailscaleClient) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{
		CertDomains: []string{"tsnet.example.com"},
	}, nil
}
