package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestListThreads(t *testing.T) {
	// Create a mock DiscussService
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()

	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     &pgxpool.Pool{}, // Can be nil if not used directly
		queries:    mockQueries,
		tmpls:      tmpl,
		httpsURL:   "https://example.com",
		version:    "1.0",
		gitSha:     "abc123",
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
	handler := http.HandlerFunc(UserMiddleware(ds, http.HandlerFunc(ds.ListThreads)))
	handler.ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code)

	assert.Contains(t, rr.Body.String(), ds.version)
}

func TestListMember(t *testing.T) {
	// Create mock instances
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpl := setupTemplates()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		httpsURL:   "https://example.com",
		version:    "1.0",
		gitSha:     "abc123",
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
				mq.getMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
					return GetMemberRow{
						Email:    "test@example.com",
						Location: pgtype.Text{String: "Test Location", Valid: true},
						ID:       id,
					}, nil
				}
				mq.listMemberThreadsFunc = func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
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
				mq.getMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
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
				mq.getMemberFunc = func(ctx context.Context, id int64) (GetMemberRow, error) {
					return GetMemberRow{
						Email:    "test@example.com",
						Location: pgtype.Text{String: "Test Location", Valid: true},
						ID:       id,
					}, nil
				}
				mq.listMemberThreadsFunc = func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
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
			mockQueries.getMemberFunc = nil
			mockQueries.listMemberThreadsFunc = nil
			mockQueries.CreateOrReturnIDFunc = nil

			// Setup mocks
			tt.setupMocks(mockQueries)

			mockQueries.CreateOrReturnIDFunc = func(ctx context.Context, email string) (int64, error) {
				return 1, nil
			}

			// Create a new HTTP request
			req, err := http.NewRequest(tt.method, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}

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
			ds := &DiscussService{
				tailClient: mockTailscaleClient,
				logger:     logger,
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

func TestEditThreadPost(t *testing.T) {
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	tmpl := setupTemplates()
	ds := &DiscussService{
		tailClient: mockTailscaleClient,
		logger:     logger,
		dbconn:     nil, // Not needed for this test
		queries:    mockQueries,
		tmpls:      tmpl,
		httpsURL:   "https://example.com",
		version:    "1.0",
		gitSha:     "abc123",
	}

	tests := []struct {
		name               string
		method             string
		url                string
		tid                string
		pid                string
		formData           map[string]string
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
			formData: map[string]string{
				"thread_body": "Updated body",
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (int64, error) {
					return 1, nil
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
			formData: map[string]string{
				"invalid": string([]byte{0xff}),
			},
			setupMocks:         func(mq *MockQueries, mtc *MockTailscaleClient) {},
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       http.StatusText(http.StatusBadRequest),
		},
		{
			name:   "GetTailscaleUserEmail error",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: map[string]string{
				"thread_body": "Updated body",
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return nil, errors.New("WhoIs error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
		{
			name:   "CreateOrReturnID error",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: map[string]string{
				"thread_body": "Updated body",
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (int64, error) {
					return 0, errors.New("CreateOrReturnID error")
				}
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       http.StatusText(http.StatusInternalServerError),
		},
		{
			name:   "UpdateThreadPost error",
			method: "POST",
			url:    "/thread/1/2/edit",
			tid:    "1",
			pid:    "2",
			formData: map[string]string{
				"thread_body": "Updated body",
			},
			setupMocks: func(mq *MockQueries, mtc *MockTailscaleClient) {
				mtc.WhoIsFunc = func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
					return &apitype.WhoIsResponse{
						UserProfile: &tailcfg.UserProfile{
							LoginName: "test@example.com",
						},
					}, nil
				}
				mq.CreateOrReturnIDFunc = func(ctx context.Context, email string) (int64, error) {
					return 1, nil
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
			if err != nil {
				t.Fatal(err)
			}

			req.SetPathValue("tid", tt.tid)
			req.SetPathValue("pid", tt.pid)

			// Add form data if present
			if tt.formData != nil {
				form := url.Values{}
				for key, value := range tt.formData {
					form.Add(key, value)
				}
				req.PostForm = form
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
	inTransaction           bool
	getMemberFunc           func(ctx context.Context, id int64) (GetMemberRow, error)
	listMemberThreadsFunc   func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error)
	CreateOrReturnIDFunc    func(ctx context.Context, email string) (int64, error)
	GetThreadSequenceIdFunc func(ctx context.Context) (int64, error)
	CreateThreadFunc        func(ctx context.Context, arg CreateThreadParams) error
	UpdateThreadFunc        func(ctx context.Context, arg UpdateThreadParams) error
	UpdateThreadPostFunc    func(ctx context.Context, arg UpdateThreadPostParams) error
	// arg contains the parameters required to fetch the thread for editing.
	GetThreadForEditFunc     func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error)
	GetThreadPostForEditFunc func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error)
}

func (m *MockQueries) CreateOrReturnID(ctx context.Context, pEmail string) (int64, error) {
	if m.CreateOrReturnIDFunc != nil {
		return m.CreateOrReturnIDFunc(ctx, pEmail)
	}
	return 1, nil
}

func (m *MockQueries) CreateThread(ctx context.Context, arg CreateThreadParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) CreateThreadPost(ctx context.Context, arg CreateThreadPostParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) GetMember(ctx context.Context, id int64) (GetMemberRow, error) {
	if m.getMemberFunc != nil {
		return m.getMemberFunc(ctx, id)
	}

	// Mock implementation
	return GetMemberRow{
		Email:      "test@example.com",
		Location:   pgtype.Text{String: "Test Location", Valid: true},
		ID:         id,
		DateJoined: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PhotoUrl:   pgtype.Text{String: "http://example.com/photo.jpg", Valid: true},
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
	// Mock implementation
	return "Mock Thread Subject", nil
}

func (m *MockQueries) GetThreadForEdit(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
	if m.GetThreadForEditFunc != nil {
		return m.GetThreadForEditFunc(ctx, arg)
	}

	// Mock implementation
	return GetThreadForEditRow{
		Email:        pgtype.Text{String: "test@example.com", Valid: true},
		Body:         pgtype.Text{String: "Test Body", Valid: true},
		ThreadPostID: pgtype.Int8{Valid: true, Int64: arg.ID},
		Subject:      "Test Subject",
		ThreadID:     arg.ID,
	}, nil
}

func (m *MockQueries) GetThreadPostForEdit(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
	if m.GetThreadForEditFunc != nil {
		return m.GetThreadPostForEditFunc(ctx, arg)
	}

	// Mock implementation
	return GetThreadPostForEditRow{
		ID:   arg.ID,
		Body: pgtype.Text{String: "Test Body", Valid: true},
	}, nil
}

func (m *MockQueries) ListMemberThreads(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
	if m.listMemberThreadsFunc != nil {
		return m.listMemberThreadsFunc(ctx, memberID)
	}

	// Mock implementation
	return []ListMemberThreadsRow{
		// Populate with mock data
	}, nil
}

func (m *MockQueries) ListThreadPosts(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error) {
	// Mock implementation
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
			Email:          pgtype.Text{String: "test@example.com", Valid: true},
			Lastid:         pgtype.Int8{Int64: 2, Valid: true},
			Lastname:       pgtype.Text{String: "last@example.com", Valid: true},
			Subject:        "Test Subject",
			Posts:          pgtype.Int4{Int32: 5, Valid: true},
			Views:          pgtype.Int4{Int32: 100, Valid: true},
			Body:           pgtype.Text{String: "Test Body", Valid: true},
			LastViewPosts:  0,
			Dot:            false,
			Sticky:         pgtype.Bool{Bool: false, Valid: true},
			Locked:         pgtype.Bool{Bool: false, Valid: true},
		},
	}, nil
}

func (m *MockQueries) UpdateMemberByEmail(ctx context.Context, arg UpdateMemberByEmailParams) error {
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
			LoginName: "test@example.com",
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
