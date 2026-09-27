package main

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/client/tailscale/apitype"
)

// newBoardTestHandler wires the same authenticated + board-data chain and route
// patterns that SetupRoutes uses, so the tests render the real templates through
// real routing. That is what proves a template executes with row data rather
// than merely parsing at startup.
func newBoardTestHandler(t *testing.T, login string, queries ExtendedQuerier) http.Handler {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	telemetry := newTestTelemetry()

	tailClient := &MockTailscaleClient{
		WhoIsFunc: func(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
			return whoIsFor(login), nil
		},
	}

	dsvc := NewDiscussService(tailClient, logger, nil, queries, setupTemplates(), "test-host", "test", "testsha", telemetry)
	querierAdapter := NewQuerierAdapter(dsvc.queries)

	authProvider := middleware.NewTailscaleAuthProvider(dsvc.tailClient, querierAdapter, logger, middleware.TailscaleAuthConfig{})
	ms := middleware.NewMiddlewareSetup(middleware.SetupOptions{
		Logger:        logger,
		Telemetry:     ConvertTelemetryConfig(telemetry),
		AuthProvider:  authProvider,
		ErrorRenderer: dsvc.renderErrorMessage,
	})

	authChain := ms.CreateAuthenticatedChain().Append(middleware.BoardDataMiddleware(querierAdapter))

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", authChain.ThenFunc(dsvc.ListThreads))
	mux.Handle("GET /member/{mid}", authChain.ThenFunc(dsvc.ListMember))

	return mux
}

func TestListMemberShowsViewedMemberNotViewer(t *testing.T) {
	const viewerEmail = "mock1@gmail.com"
	const viewedEmail = "mock22@gmail.com"

	queries := &MockQueries{
		// PreferredName is left invalid on purpose: that is the state that
		// decides which email the profile card falls back to.
		GetMemberFunc: func(_ context.Context, id int64) (GetMemberRow, error) {
			return GetMemberRow{
				ID:         id,
				Email:      viewedEmail,
				Location:   pgtype.Text{String: "Mock Location", Valid: true},
				DateJoined: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			}, nil
		},
		ListMemberThreadsFunc: func(_ context.Context, _ int64) ([]ListMemberThreadsRow, error) {
			return nil, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/member/42", nil)
	req.RemoteAddr = "100.64.0.10:12345"
	rec := httptest.NewRecorder()
	newBoardTestHandler(t, viewerEmail, queries).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, viewedEmail)
	assert.NotContains(t, body, viewerEmail)
}

func TestListMemberUnknownIDShowsNotFoundPage(t *testing.T) {
	queries := &MockQueries{
		GetMemberFunc: func(_ context.Context, _ int64) (GetMemberRow, error) {
			return GetMemberRow{}, pgx.ErrNoRows
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/member/999", nil)
	req.RemoteAddr = "100.64.0.10:12345"
	rec := httptest.NewRecorder()
	newBoardTestHandler(t, "viewer@example.com", queries).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "<title>Mock Board Title</title>")
	assert.Contains(t, body, `<h1 class="page-title">Not Found</h1>`)
	assert.Contains(t, body, template.HTMLEscapeString(errorMessage(http.StatusNotFound)))
}

func TestIndexShowsLastPosterNotThreadCreator(t *testing.T) {
	const viewerEmail = "viewer@example.com"
	const creatorEmail = "creator@example.com"
	const lastPosterEmail = "lastposter@example.com"

	queries := &MockQueries{
		// The thread's creator (Email) differs from the author of the newest
		// post (Lastid/Lastname), which is exactly the distinction the
		// "last post by" column has to render.
		ListThreadsFunc: func(_ context.Context, _ ListThreadsParams) ([]ListThreadsRow, error) {
			return []ListThreadsRow{
				{
					ThreadID:       1,
					Subject:        "Mock Subject",
					Email:          pgtype.Text{String: creatorEmail, Valid: true},
					Lastid:         pgtype.Int8{Int64: 8, Valid: true},
					Lastname:       pgtype.Text{String: lastPosterEmail, Valid: true},
					DateLastPosted: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				},
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.10:12345"
	rec := httptest.NewRecorder()
	newBoardTestHandler(t, viewerEmail, queries).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `<a href="/member/8">`+lastPosterEmail+`</a>`)
	assert.NotContains(t, body, creatorEmail)
}
