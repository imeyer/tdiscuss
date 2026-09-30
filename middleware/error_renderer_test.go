package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type renderedError struct {
	status  int
	message string
}

// recordingRenderer is an ErrorRenderer that records each rejection, so a test
// can tell which middleware turned a request away and with what message.
type recordingRenderer struct {
	calls []renderedError
}

func (rr *recordingRenderer) render(w http.ResponseWriter, _ *http.Request, status int, message string) {
	rr.calls = append(rr.calls, renderedError{status: status, message: message})
	w.WriteHeader(status)
}

// stubAuthProvider resolves every request to one peer and one member, or
// fails at whichever step has an error set.
type stubAuthProvider struct {
	peerErr error
	userErr error
	user    ContextUser
}

func (p *stubAuthProvider) ResolvePeer(_ *http.Request) (*Peer, error) {
	if p.peerErr != nil {
		return nil, p.peerErr
	}
	return &Peer{LoginName: "member@example.com"}, nil
}

func (p *stubAuthProvider) CreateOrGetUser(_ context.Context, _ *Peer) (*ContextUser, error) {
	if p.userErr != nil {
		return nil, p.userErr
	}
	user := p.user
	return &user, nil
}

// noopTelemetry gives NewMiddlewareSetup the tracer and meter it needs.
func noopTelemetry() *TelemetryConfig {
	return &TelemetryConfig{
		Tracer: tracenoop.NewTracerProvider().Tracer("test"),
		Meter:  metricnoop.NewMeterProvider().Meter("test"),
	}
}

func newErrorRendererSetup(provider AuthProvider, renderer ErrorRenderer) *MiddlewareSetup {
	return NewMiddlewareSetup(SetupOptions{
		Logger:        slog.New(slog.DiscardHandler),
		Telemetry:     noopTelemetry(),
		AuthProvider:  provider,
		ErrorRenderer: renderer,
		RateLimit:     &RateLimitConfig{RequestsPerSecond: noRefill, Burst: 1},
	})
}

// Every rejection in the public, authenticated and admin chains must go
// through the configured ErrorRenderer, or that rejection reaches the browser
// as plain text instead of the board's error page.
func TestPageChainsRejectThroughErrorRenderer(t *testing.T) {
	member := ContextUser{ID: 1, Email: "member@example.com"}

	tests := []struct {
		name        string
		provider    *stubAuthProvider
		chain       func(*MiddlewareSetup) *Chain
		request     func() *http.Request
		sends       int // requests sent; the last one is checked
		wantStatus  int
		wantMessage string
	}{
		{
			name:     "oversized body",
			provider: &stubAuthProvider{user: member},
			chain:    (*MiddlewareSetup).CreatePublicChain,
			request: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/thread/1", nil)
				r.ContentLength = 2 << 20
				return r
			},
			sends:       1,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantMessage: "That submission is too large. The limit is 1 MB.",
		},
		{
			name:        "rate limited",
			provider:    &stubAuthProvider{user: member},
			chain:       (*MiddlewareSetup).CreatePublicChain,
			request:     func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			sends:       2,
			wantStatus:  http.StatusTooManyRequests,
			wantMessage: "You're sending requests too quickly. Wait a few seconds and try again.",
		},
		{
			name:        "peer not identified",
			provider:    &stubAuthProvider{peerErr: errors.New("tagged node")},
			chain:       (*MiddlewareSetup).CreateAuthenticatedChain,
			request:     func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			sends:       1,
			wantStatus:  http.StatusUnauthorized,
			wantMessage: notIdentifiedMessage,
		},
		{
			name:        "member lookup fails",
			provider:    &stubAuthProvider{userErr: errors.New("database down")},
			chain:       (*MiddlewareSetup).CreateAuthenticatedChain,
			request:     func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			sends:       1,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "",
		},
		{
			name:        "blocked member",
			provider:    &stubAuthProvider{user: ContextUser{ID: 1, IsBlocked: true}},
			chain:       (*MiddlewareSetup).CreateAuthenticatedChain,
			request:     func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			sends:       1,
			wantStatus:  http.StatusNotFound,
			wantMessage: "",
		},
		{
			name:     "cross-origin form",
			provider: &stubAuthProvider{user: member},
			chain:    (*MiddlewareSetup).CreateAuthenticatedChain,
			request: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/thread/1", nil)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				return r
			},
			sends:       1,
			wantStatus:  http.StatusForbidden,
			wantMessage: "That form was sent from a different site, so it was rejected. Reload the page and try again.",
		},
		{
			name:        "not an admin",
			provider:    &stubAuthProvider{user: member},
			chain:       (*MiddlewareSetup).CreateAdminChain,
			request:     func() *http.Request { return httptest.NewRequest(http.MethodGet, "/admin", nil) },
			sends:       1,
			wantStatus:  http.StatusForbidden,
			wantMessage: "Only board admins can open that page.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderer := &recordingRenderer{}
			h := tt.chain(newErrorRendererSetup(tt.provider, renderer.render)).
				ThenFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

			var rec *httptest.ResponseRecorder
			for range tt.sends {
				rec = httptest.NewRecorder()
				h.ServeHTTP(rec, tt.request())
			}

			assert.Equal(t, tt.wantStatus, rec.Code)
			require.Len(t, renderer.calls, 1)
			assert.Equal(t, renderedError{status: tt.wantStatus, message: tt.wantMessage}, renderer.calls[0])
		})
	}
}

func TestPageChainsDefaultToPlainText(t *testing.T) {
	h := newErrorRendererSetup(&stubAuthProvider{}, nil).CreatePublicChain().
		ThenFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/thread/1", nil)
	req.ContentLength = 2 << 20
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "That submission is too large. The limit is 1 MB.\n", rec.Body.String())
}
