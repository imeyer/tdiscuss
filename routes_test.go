package main

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/client/tailscale/apitype"
)

// noRefill is a rate low enough that no bucket refills while a test runs, so
// tests that exhaust a limit do not depend on how fast they run.
const noRefill = 0.001

// routesTestLogin is the tailnet login the routes tests sign in with. It is
// the mock GetMember's email, so the member owns the profile they edit.
const routesTestLogin = "mock@example.com"

// newRoutesTestHandler builds the application's real routes. whoIsErr fails
// every WhoIs; member is CreateOrReturnID for the requesting member, and nil
// means the mock's default, an admin; limits nil means the production limits.
func newRoutesTestHandler(t *testing.T, whoIsErr error, member func(context.Context, string) (CreateOrReturnIDRow, error), limits *middleware.RateLimitConfig) http.Handler {
	t.Helper()

	tailClient := &MockTailscaleClient{
		WhoIsFunc: func(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
			if whoIsErr != nil {
				return nil, whoIsErr
			}
			return whoIsFor(routesTestLogin), nil
		},
	}
	return newRoutesTestHandlerFor(t, tailClient, &MockQueries{CreateOrReturnIDFunc: member}, limits)
}

// newRoutesTestHandlerFor is newRoutesTestHandler with the clients supplied.
func newRoutesTestHandlerFor(t *testing.T, tailClient TailscaleClient, queries ExtendedQuerier, limits *middleware.RateLimitConfig) http.Handler {
	t.Helper()

	dsvc := NewDiscussService(tailClient, slog.New(slog.DiscardHandler), nil, queries,
		setupTemplates(), "test-host", "test", "testsha", newTestTelemetry())
	if limits == nil {
		return SetupRoutes(dsvc, staticFiles)
	}
	return setupRoutes(dsvc, staticFiles, *limits)
}

// routesRequest is a request from one member's device. A POST carries form as
// its body and the header a browser sends with a same-origin form.
func routesRequest(method, path, form string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(form))
	req.RemoteAddr = "100.64.0.10:12345"
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	return req
}

// Every way a page request can fail must reach the browser as the board's
// error page. These go through setupRoutes itself, so they also cover the
// wiring there: the middleware's ErrorRenderer and the catch-all route.
func TestSetupRoutesRendersEveryErrorAsErrorPage(t *testing.T) {
	const boardTitle = "<title>Mock Board Title</title>"
	const defaultTitle = "<title>tdiscuss</title>"

	member := func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
		return CreateOrReturnIDRow{ID: 1}, nil
	}

	tests := []struct {
		name     string
		whoIsErr error
		// CreateOrReturnID for the requesting member; nil is the mock's
		// default, an admin.
		member      func(context.Context, string) (CreateOrReturnIDRow, error)
		limits      *middleware.RateLimitConfig // nil is the production limits
		method      string
		path        string
		headers     map[string]string
		bodySize    int64
		sends       int // requests sent; the last one is checked
		wantStatus  int
		wantMessage string
		wantAllow   string
		// Rejections before authentication have no member, so no menu.
		wantMenu bool
		// Handler errors have the board title. Middleware rejections happen
		// before the board data loads, so they get the default title.
		wantTitle string
	}{
		{
			name:        "unknown URL",
			method:      http.MethodGet,
			path:        "/no/such/page",
			wantStatus:  http.StatusNotFound,
			wantMessage: errorMessage(http.StatusNotFound),
			wantMenu:    true,
			wantTitle:   boardTitle,
		},
		{
			name:        "known path, wrong method",
			method:      http.MethodPost,
			path:        "/member/42",
			wantStatus:  http.StatusMethodNotAllowed,
			wantMessage: errorMessage(http.StatusMethodNotAllowed),
			wantAllow:   "GET, HEAD",
			wantMenu:    true,
			wantTitle:   boardTitle,
		},
		{
			// Static files are served without sign-in, so there is no
			// member and no board data.
			name:        "missing static file",
			whoIsErr:    errors.New("not on the tailnet"),
			method:      http.MethodGet,
			path:        "/static/no-such-file.css",
			wantStatus:  http.StatusNotFound,
			wantMessage: errorMessage(http.StatusNotFound),
			wantTitle:   defaultTitle,
		},
		{
			name:        "static file, wrong method",
			method:      http.MethodPost,
			path:        "/static/style.css",
			wantStatus:  http.StatusMethodNotAllowed,
			wantMessage: errorMessage(http.StatusMethodNotAllowed),
			wantAllow:   "GET, HEAD",
			wantMenu:    true,
			wantTitle:   boardTitle,
		},
		{
			name:        "invalid thread ID in a reply",
			method:      http.MethodPost,
			path:        "/thread/abc",
			wantStatus:  http.StatusBadRequest,
			wantMessage: errorMessage(http.StatusBadRequest),
			wantMenu:    true,
			wantTitle:   boardTitle,
		},
		{
			name:        "invalid thread ID in a post edit",
			method:      http.MethodPost,
			path:        "/thread/abc/1/edit",
			wantStatus:  http.StatusBadRequest,
			wantMessage: errorMessage(http.StatusBadRequest),
			wantMenu:    true,
			wantTitle:   boardTitle,
		},
		{
			name:        "peer not identified",
			whoIsErr:    errors.New("no such peer"),
			method:      http.MethodGet,
			path:        "/",
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "The board couldn't identify you on the tailnet. Only tailnet members can use it.",
			wantTitle:   defaultTitle,
		},
		{
			name: "blocked member",
			member: func(_ context.Context, _ string) (CreateOrReturnIDRow, error) {
				return CreateOrReturnIDRow{ID: 1, IsBlocked: true}, nil
			},
			method:      http.MethodGet,
			path:        "/",
			wantStatus:  http.StatusNotFound,
			wantMessage: errorMessage(http.StatusNotFound),
			wantTitle:   defaultTitle,
		},
		{
			name:        "not an admin",
			member:      member,
			method:      http.MethodGet,
			path:        "/admin",
			wantStatus:  http.StatusForbidden,
			wantMessage: "Only board admins can open that page.",
			wantMenu:    true,
			wantTitle:   defaultTitle,
		},
		{
			name:        "cross-origin form",
			member:      member,
			method:      http.MethodPost,
			path:        "/thread/1",
			headers:     map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus:  http.StatusForbidden,
			wantMessage: "That form was sent from a different site, so it was rejected. Reload the page and try again.",
			wantMenu:    true,
			wantTitle:   defaultTitle,
		},
		{
			name:        "oversized post",
			method:      http.MethodPost,
			path:        "/thread/1",
			bodySize:    2 << 20,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantMessage: "That submission is too large. The limit is 1 MB.",
			wantTitle:   defaultTitle,
		},
		{
			// The per-device limit runs before sign-in.
			name:        "device rate limited",
			limits:      &middleware.RateLimitConfig{RequestsPerSecond: noRefill, Burst: 3, EndpointLimits: writeRateLimits(false)},
			method:      http.MethodGet,
			path:        "/",
			sends:       4,
			wantStatus:  http.StatusTooManyRequests,
			wantMessage: "You're sending requests too quickly. Wait a few seconds and try again.",
			wantTitle:   defaultTitle,
		},
		{
			// Per-member write limits run after sign-in. Admin actions are
			// one per five seconds outside dev mode.
			name:        "member rate limited",
			method:      http.MethodPost,
			path:        "/admin",
			sends:       2,
			wantStatus:  http.StatusTooManyRequests,
			wantMessage: "You're sending requests too quickly. Wait a few seconds and try again.",
			wantMenu:    true,
			wantTitle:   defaultTitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRoutesTestHandler(t, tt.whoIsErr, tt.member, tt.limits)

			var rec *httptest.ResponseRecorder
			for range max(tt.sends, 1) {
				req := routesRequest(tt.method, tt.path, "")
				for k, v := range tt.headers {
					req.Header.Set(k, v)
				}
				if tt.bodySize > 0 {
					req.ContentLength = tt.bodySize
				}
				rec = httptest.NewRecorder()
				h.ServeHTTP(rec, req)
			}

			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Equal(t, tt.wantAllow, rec.Header().Get("Allow"))
			body := rec.Body.String()
			assert.Contains(t, body, `<h1 class="page-title">`+http.StatusText(tt.wantStatus)+`</h1>`)
			assert.Contains(t, body, template.HTMLEscapeString(tt.wantMessage))
			assert.Contains(t, body, tt.wantTitle)
			if tt.wantMenu {
				assert.Contains(t, body, `<nav id="menu">`)
			} else {
				assert.NotContains(t, body, `<nav id="menu">`)
			}
		})
	}
}

// Every write limit must name a route that setupRoutes registers. The limiter
// matches keys against r.Pattern, so a misspelled key would limit nothing.
func TestWriteRateLimitsMatchRegisteredRoutes(t *testing.T) {
	// The production bursts, with rates that do not refill during the test.
	writes := writeRateLimits(false)
	for pattern, limit := range writes {
		writes[pattern] = middleware.EndpointLimit{Rate: noRefill, Burst: limit.Burst}
	}
	limits := &middleware.RateLimitConfig{RequestsPerSecond: noRefill, Burst: 100, EndpointLimits: writes}

	for pattern, limit := range writes {
		t.Run(pattern, func(t *testing.T) {
			method, path, _ := strings.Cut(pattern, " ")
			path = strings.NewReplacer("{tid}", "1", "{pid}", "1").Replace(path)
			h := newRoutesTestHandler(t, nil, nil, limits)

			for i := range limit.Burst {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, routesRequest(method, path, ""))
				require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "request %d of a burst of %d", i+1, limit.Burst)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, routesRequest(method, path, ""))
			assert.Equal(t, http.StatusTooManyRequests, rec.Code, "the request after the burst")
		})
	}
}

// An admin action redirects back to /admin. Loading that page must not count
// against the one-action-per-five-seconds admin limit.
func TestAdminActionRedirectIsNotRateLimited(t *testing.T) {
	h := newRoutesTestHandler(t, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, routesRequest(http.MethodPost, "/admin", "action=block_member&member_id=2"))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	location := rec.Header().Get("Location")
	require.Equal(t, "/admin", location)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, routesRequest(http.MethodGet, location, ""))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// Spending a write allowance must not limit reading. Buckets used to be keyed
// by device alone, so the limit on a device's first request applied to
// everything it did afterwards.
func TestWriteLimitsDoNotLimitReads(t *testing.T) {
	h := newRoutesTestHandler(t, nil, nil, nil)

	for range writeRateLimits(false)["POST /thread/new"].Burst + 1 {
		h.ServeHTTP(httptest.NewRecorder(), routesRequest(http.MethodPost, "/thread/new", ""))
	}
	for i := range 5 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, routesRequest(http.MethodGet, "/", ""))
		assert.Equal(t, http.StatusOK, rec.Code, "read %d", i+1)
	}
}

// Every page loads, and the router sends HEAD to GET routes, so a HEAD must be
// answered like the GET. Handlers used to check r.Method == GET themselves and
// turned HEAD away with 405, 404 or 400.
func TestPagesAnswerGetAndHead(t *testing.T) {
	for _, path := range []string{
		"/", "/thread/1", "/member/1", "/thread/new", "/thread/1/edit",
		"/thread/1/1/edit", "/member/edit", "/formatting", "/admin", "/static/style.css",
	} {
		t.Run(path, func(t *testing.T) {
			get := httptest.NewRecorder()
			newRoutesTestHandler(t, nil, nil, nil).ServeHTTP(get, routesRequest(http.MethodGet, path, ""))
			head := httptest.NewRecorder()
			newRoutesTestHandler(t, nil, nil, nil).ServeHTTP(head, routesRequest(http.MethodHead, path, ""))

			assert.Equal(t, http.StatusOK, get.Code)
			assert.Equal(t, get.Code, head.Code)
		})
	}
}

// The favicon is a public asset: it must be served without signing in, at the
// path header.html links and at the root, where some clients look regardless.
func TestFaviconIsServedWithoutSignIn(t *testing.T) {
	want, err := staticFiles.ReadFile("static/favicon.ico")
	require.NoError(t, err)

	h := newRoutesTestHandler(t, errors.New("not on the tailnet"), nil, nil)
	for _, path := range []string{"/favicon.ico", "/static/favicon.ico"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, routesRequest(http.MethodGet, path, ""))

			assert.Equal(t, http.StatusOK, rec.Code)
			// image/x-icon or image/vnd.microsoft.icon, depending on the
			// system's MIME table.
			assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "image/"), rec.Header().Get("Content-Type"))
			assert.Equal(t, want, rec.Body.Bytes())
		})
	}

	rec := httptest.NewRecorder()
	newRoutesTestHandler(t, nil, nil, nil).ServeHTTP(rec, routesRequest(http.MethodGet, "/", ""))
	assert.Contains(t, rec.Body.String(), `<link rel="icon" href="/static/favicon.ico">`)
}

// The 405 must list every method the path has a route for, not only GET and
// POST, which are all the application's routes use today.
func TestAllowedMethodsListsEveryMethodWithARoute(t *testing.T) {
	noop := func(http.ResponseWriter, *http.Request) {}
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /a", "DELETE /a", "PUT /b", "/"} {
		mux.HandleFunc(pattern, noop)
	}

	for path, want := range map[string][]string{
		"/a": {http.MethodGet, http.MethodHead, http.MethodDelete},
		"/b": {http.MethodPut},
		"/c": nil,
	} {
		assert.Equal(t, want, allowedMethods(mux, httptest.NewRequest(http.MethodPost, path, nil)), path)
	}
}

// A post can only be edited under its own thread's URL. The query used to
// match the post and member alone, so /thread/5/99/edit edited post 99 of
// thread 7, logged thread 5 and redirected there.
func TestPostEditChecksThread(t *testing.T) {
	const postThread = 7
	queries := &MockQueries{
		GetThreadPostForEditFunc: func(_ context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
			if arg.ThreadID != postThread {
				return GetThreadPostForEditRow{}, pgx.ErrNoRows
			}
			return GetThreadPostForEditRow{ID: arg.ID}, nil
		},
	}
	tailClient := &MockTailscaleClient{
		WhoIsFunc: func(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
			return whoIsFor(routesTestLogin), nil
		},
	}
	h := newRoutesTestHandlerFor(t, tailClient, queries, nil)

	send := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, routesRequest(method, path, "thread_post_body=edited"))
		return rec
	}

	assert.Equal(t, http.StatusNotFound, send(http.MethodGet, "/thread/5/99/edit").Code)
	assert.Equal(t, http.StatusNotFound, send(http.MethodPost, "/thread/5/99/edit").Code)

	assert.Equal(t, http.StatusOK, send(http.MethodGet, "/thread/7/99/edit").Code)
	rec := send(http.MethodPost, "/thread/7/99/edit")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/thread/7", rec.Header().Get("Location"))
}

// Each save must reach its POST handler and redirect to what it saved. The
// GET and POST handlers are registered separately, so a route wired to the
// wrong one would show the form again instead of saving.
func TestSavesRedirectToWhatTheySaved(t *testing.T) {
	tests := []struct {
		path, form, wantLocation string
	}{
		{"/thread/3/edit", "subject=New+subject&thread_body=New+body", "/thread/3"},
		{"/member/edit", "location=Here", "/member/1"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newRoutesTestHandler(t, nil, nil, nil).ServeHTTP(rec, routesRequest(http.MethodPost, tt.path, tt.form))

			assert.Equal(t, http.StatusSeeOther, rec.Code)
			assert.Equal(t, tt.wantLocation, rec.Header().Get("Location"))
		})
	}
}

// Members can only open and save their own profile: the profile the member
// ID loads must have the signed-in login's email.
func TestProfileEditRequiresOwnProfile(t *testing.T) {
	tailClient := &MockTailscaleClient{
		WhoIsFunc: func(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
			return whoIsFor("someone-else@example.com"), nil
		},
	}
	h := newRoutesTestHandlerFor(t, tailClient, &MockQueries{}, nil)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, routesRequest(method, "/member/edit", "location=Here"))
		assert.Equal(t, http.StatusForbidden, rec.Code, method)
	}
}
