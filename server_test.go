package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

type MockTx struct{}

func (m *MockTx) Commit(ctx context.Context) error {
	return nil
}

func (m *MockTx) Rollback(ctx context.Context) error {
	return nil
}

type MockQueries struct {
	inTransaction bool
}

func (m *MockQueries) CreateOrReturnID(ctx context.Context, pEmail string) (int64, error) {
	// Mock implementation
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
	// Mock implementation
	return GetMemberRow{
		Email:         "test@example.com",
		Location:      pgtype.Text{String: "Test Location", Valid: true},
		ID:            id,
		DateJoined:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		DateFirstPost: pgtype.Date{Time: time.Now(), Valid: true},
		PhotoUrl:      pgtype.Text{String: "http://example.com/photo.jpg", Valid: true},
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

func (m *MockQueries) ListMemberThreads(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
	// Mock implementation
	return []ListMemberThreadsRow{
		// Populate with mock data
	}, nil
}

func (m *MockQueries) ListThreadPosts(ctx context.Context, threadID int64) ([]ListThreadPostsRow, error) {
	// Mock implementation
	return []ListThreadPostsRow{
		// Populate with mock data
	}, nil
}

func (m *MockQueries) ListThreads(ctx context.Context, memberID int64) ([]ListThreadsRow, error) {
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
			Dot:            "f",
			Sticky:         pgtype.Bool{Bool: false, Valid: true},
			Locked:         pgtype.Bool{Bool: false, Valid: true},
		},
	}, nil
}

func (m *MockQueries) UpdateMemberByEmail(ctx context.Context, arg UpdateMemberByEmailParams) error {
	// Mock implementation
	return nil
}

func (m *MockQueries) WithTx(pgx.Tx) ExtendedQuerier {
	return &MockQueries{
		inTransaction: true,
	}
}

type MockTailscaleClient struct{}

func (m *MockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	return &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "test@example.com",
		},
	}, nil
}

func (m *MockTailscaleClient) ExpandSNIName(ctx context.Context, remoteAddr string) (fqdn string, ok bool) {
	return "example.com", true
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

func TestListThreads(t *testing.T) {
	// Create a mock DiscussService
	mockQueries := &MockQueries{}
	mockTailscaleClient := &MockTailscaleClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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
	handler := http.HandlerFunc(ds.ListThreads)
	handler.ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code)

	assert.Contains(t, rr.Body.String(), "tdiscuss version")
}
