package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memberAuthProvider signs each request in as the member whose ID is in its
// X-Test-Member header, so one test can send requests as different members.
// IDs from 100 up are admins. A header that is not a number fails the member
// lookup.
type memberAuthProvider struct{}

func (memberAuthProvider) ResolvePeer(r *http.Request) (*Peer, error) {
	return &Peer{LoginName: r.Header.Get("X-Test-Member")}, nil
}

func (memberAuthProvider) CreateOrGetUser(_ context.Context, peer *Peer) (*ContextUser, error) {
	id, err := strconv.ParseInt(peer.LoginName, 10, 64)
	if err != nil {
		return nil, err
	}
	return &ContextUser{ID: id, IsAdmin: id >= 100}, nil
}

// noRefill is a rate low enough that no bucket refills during a test.
const noRefill = 0.001

func newRateLimitTestSetup(deviceBurst int, limits map[string]EndpointLimit) *MiddlewareSetup {
	return NewMiddlewareSetup(SetupOptions{
		Logger:       slog.New(slog.DiscardHandler),
		Telemetry:    noopTelemetry(),
		AuthProvider: memberAuthProvider{},
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: noRefill,
			Burst:             deviceBurst,
			EndpointLimits:    limits,
		},
	})
}

// newRateLimitTestMux registers routes on a ServeMux the way the application
// does, so requests carry r.Pattern, behind the authenticated chain.
func newRateLimitTestMux(deviceBurst int, limits map[string]EndpointLimit) http.Handler {
	chain := newRateLimitTestSetup(deviceBurst, limits).CreateAuthenticatedChain()
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /", "GET /thread/{tid}", "POST /thread/{tid}", "POST /thread/new"} {
		mux.Handle(pattern, chain.ThenFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}
	return mux
}

// send makes one request as member from the device at addr and returns the
// status.
func send(h http.Handler, method, path, member, addr string) int {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = addr
	req.Header.Set("X-Test-Member", member)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

const (
	deviceA = "100.64.0.1:40000"
	deviceB = "100.64.0.2:40000"
)

// An endpoint limit keyed by a pattern with a wildcard must apply to every
// path that pattern matches, and only to its method.
func TestEndpointLimitMatchesRoutePattern(t *testing.T) {
	h := newRateLimitTestMux(100, map[string]EndpointLimit{
		"POST /thread/{tid}": {Rate: noRefill, Burst: 2},
	})

	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/42", "1", deviceA))
	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/42", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/42", "1", deviceA))
	// One allowance for the route, not one per thread.
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/7", "1", deviceA))
	// Reading a thread is a different route.
	assert.Equal(t, http.StatusOK, send(h, http.MethodGet, "/thread/42", "1", deviceA))
}

// Each limited route has its own bucket, and routes without a limit share
// none of them. Buckets used to be keyed by visitor alone, so the limit of a
// device's first request applied to everything it did afterwards.
func TestEndpointLimitsDoNotLeakAcrossRoutes(t *testing.T) {
	h := newRateLimitTestMux(100, map[string]EndpointLimit{
		"POST /thread/new":   {Rate: noRefill, Burst: 1},
		"POST /thread/{tid}": {Rate: noRefill, Burst: 1},
	})

	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/new", "1", deviceA))
	for range 5 {
		assert.Equal(t, http.StatusOK, send(h, http.MethodGet, "/", "1", deviceA))
	}
	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/42", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/new", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/42", "1", deviceA))
}

// Endpoint limits follow the member, not the device.
func TestEndpointLimitsArePerMember(t *testing.T) {
	h := newRateLimitTestMux(100, map[string]EndpointLimit{
		"POST /thread/new": {Rate: noRefill, Burst: 1},
	})

	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/new", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/new", "1", deviceA))
	// Same member from another device: same allowance, already spent.
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodPost, "/thread/new", "1", deviceB))
	// Another member on the same device has their own.
	assert.Equal(t, http.StatusOK, send(h, http.MethodPost, "/thread/new", "2", deviceA))
}

// The device limit counts every request from an address before sign-in, so
// requests that fail authentication are limited too.
func TestDeviceLimitRunsBeforeSignIn(t *testing.T) {
	h := newRateLimitTestMux(2, nil)

	assert.Equal(t, http.StatusInternalServerError, send(h, http.MethodGet, "/", "not-a-member", deviceA))
	assert.Equal(t, http.StatusOK, send(h, http.MethodGet, "/", "1", deviceA))
	// Any member on this device is now over the limit.
	assert.Equal(t, http.StatusTooManyRequests, send(h, http.MethodGet, "/", "2", deviceA))
	// Another device is not.
	assert.Equal(t, http.StatusOK, send(h, http.MethodGet, "/", "1", deviceB))
}

// Routes on the authenticated and admin chains draw on the same device bucket.
func TestPageChainsShareDeviceLimit(t *testing.T) {
	ms := newRateLimitTestSetup(2, nil)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux := http.NewServeMux()
	mux.Handle("GET /", ms.CreateAuthenticatedChain().ThenFunc(ok))
	mux.Handle("GET /admin", ms.CreateAdminChain().ThenFunc(ok))

	assert.Equal(t, http.StatusOK, send(mux, http.MethodGet, "/", "1", deviceA))
	// Member 1 is not an admin, but the request still counted.
	assert.Equal(t, http.StatusForbidden, send(mux, http.MethodGet, "/admin", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(mux, http.MethodGet, "/", "1", deviceA))
}

// On the admin chain the admin check runs before the write limit, so a
// non-admin is always told they are not an admin, and only admins spend the
// admin allowance.
func TestAdminCheckRunsBeforeWriteLimit(t *testing.T) {
	ms := newRateLimitTestSetup(100, map[string]EndpointLimit{
		"POST /admin": {Rate: noRefill, Burst: 1},
	})
	mux := http.NewServeMux()
	mux.Handle("POST /admin", ms.CreateAdminChain().ThenFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	assert.Equal(t, http.StatusForbidden, send(mux, http.MethodPost, "/admin", "1", deviceA))
	assert.Equal(t, http.StatusForbidden, send(mux, http.MethodPost, "/admin", "1", deviceA))
	assert.Equal(t, http.StatusOK, send(mux, http.MethodPost, "/admin", "100", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(mux, http.MethodPost, "/admin", "100", deviceA))
}

// NewMiddlewareSetup copies the rate limits, so changing the caller's config
// afterwards, including its map, cannot reach chains already configured.
func TestSetupCopiesRateLimitConfig(t *testing.T) {
	config := &RateLimitConfig{
		RequestsPerSecond: noRefill,
		Burst:             1,
		EndpointLimits:    map[string]EndpointLimit{},
	}
	ms := NewMiddlewareSetup(SetupOptions{
		Logger:       slog.New(slog.DiscardHandler),
		Telemetry:    noopTelemetry(),
		AuthProvider: memberAuthProvider{},
		RateLimit:    config,
	})
	config.Burst = 100
	config.EndpointLimits["POST /thread/{tid}"] = EndpointLimit{Rate: noRefill, Burst: 0}

	chain := ms.CreateAuthenticatedChain()
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /", "POST /thread/{tid}"} {
		mux.Handle(pattern, chain.ThenFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}

	assert.Equal(t, http.StatusOK, send(mux, http.MethodGet, "/", "1", deviceA))
	assert.Equal(t, http.StatusTooManyRequests, send(mux, http.MethodGet, "/", "1", deviceA), "device burst is still 1")
	assert.Equal(t, http.StatusOK, send(mux, http.MethodPost, "/thread/42", "1", deviceB), "the added limit never reached the setup")
}

// A config that sets only EndpointLimits must keep the default per-device
// limit. A zero rate and burst used to go straight to the limiter, which then
// rejected every request.
func TestSetupFillsZeroDeviceLimitWithDefault(t *testing.T) {
	ms := NewMiddlewareSetup(SetupOptions{
		Logger:       slog.New(slog.DiscardHandler),
		Telemetry:    noopTelemetry(),
		AuthProvider: memberAuthProvider{},
		RateLimit: &RateLimitConfig{
			EndpointLimits: map[string]EndpointLimit{"POST /thread/{tid}": {Rate: noRefill, Burst: 1}},
		},
	})
	mux := http.NewServeMux()
	mux.Handle("GET /", ms.CreateAuthenticatedChain().ThenFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := range defaultRateLimitConfig().Burst {
		require.Equal(t, http.StatusOK, send(mux, http.MethodGet, "/", "1", deviceA), "request %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, send(mux, http.MethodGet, "/", "1", deviceA), "the request after the default burst")

	// A zero rate also allows one burst and then nothing, so the requests
	// above cannot tell it from the default rate.
	assert.Equal(t, defaultRateLimitConfig().RequestsPerSecond, ms.rateLimitConfig.RequestsPerSecond)
}
