package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("allows requests within limit", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, logger) // 10 req/s, burst 10
		handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// Make 5 requests - all should succeed
		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		rl := NewRateLimiter(1, 2, logger) // 1 req/s, burst 2
		handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// First 2 requests should succeed (burst)
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		// Third request should be rate limited
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
		assert.Contains(t, rr.Body.String(), "Rate limit exceeded")
	})

	t.Run("different IPs have separate limits", func(t *testing.T) {
		rl := NewRateLimiter(1, 1, logger) // 1 req/s, burst 1
		handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// First IP - one request
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.RemoteAddr = "127.0.0.1:12345"
		rr1 := httptest.NewRecorder()
		handler.ServeHTTP(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		// Second request from same IP should fail
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.RemoteAddr = "127.0.0.1:12345"
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusTooManyRequests, rr2.Code)

		// Different IP should succeed
		req3 := httptest.NewRequest("GET", "/", nil)
		req3.RemoteAddr = "192.168.1.1:12345"
		rr3 := httptest.NewRecorder()
		handler.ServeHTTP(rr3, req3)
		assert.Equal(t, http.StatusOK, rr3.Code)
	})

	t.Run("rate limit headers are set", func(t *testing.T) {
		rl := NewRateLimiter(10, 20, logger)
		handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, "20", rr.Header().Get("X-RateLimit-Limit"))
		assert.NotEmpty(t, rr.Header().Get("X-RateLimit-Reset"))
	})
}

func TestEndpointRateLimiter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("applies endpoint-specific limits", func(t *testing.T) {
		erl := NewEndpointRateLimiter(logger)
		erl.AddEndpoint("/api/*", 0.5, 1) // 1 request per 2 seconds

		handler := erl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// First request to /api/test should succeed
		req1 := httptest.NewRequest("GET", "/api/test", nil)
		req1.RemoteAddr = "127.0.0.1:12345"
		rr1 := httptest.NewRecorder()
		handler.ServeHTTP(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		// Second immediate request should fail
		req2 := httptest.NewRequest("GET", "/api/test", nil)
		req2.RemoteAddr = "127.0.0.1:12345"
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusTooManyRequests, rr2.Code)

		// Request to different endpoint should succeed
		req3 := httptest.NewRequest("GET", "/other", nil)
		req3.RemoteAddr = "127.0.0.1:12345"
		rr3 := httptest.NewRecorder()
		handler.ServeHTTP(rr3, req3)
		assert.Equal(t, http.StatusOK, rr3.Code)
	})
}

func TestWaitRateLimiter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("waits for rate limit", func(t *testing.T) {
		rl := NewRateLimiter(2, 1, logger) // 2 req/s, burst 1
		handler := rl.WaitRateLimitMiddleware(1 * time.Second)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

		start := time.Now()

		// First request should be immediate
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.RemoteAddr = "127.0.0.1:12345"
		rr1 := httptest.NewRecorder()
		handler.ServeHTTP(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		// Second request should wait but succeed
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.RemoteAddr = "127.0.0.1:12345"
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusOK, rr2.Code)

		// Should have waited approximately 500ms (1/2 second)
		elapsed := time.Since(start)
		assert.Greater(t, elapsed.Milliseconds(), int64(400))
	})

	t.Run("times out if wait too long", func(t *testing.T) {
		rl := NewRateLimiter(0.1, 1, logger) // 0.1 req/s (1 per 10s), burst 1
		handler := rl.WaitRateLimitMiddleware(50 * time.Millisecond)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

		// First request succeeds
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.RemoteAddr = "127.0.0.1:12345"
		rr1 := httptest.NewRecorder()
		handler.ServeHTTP(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		// Second request should timeout (would need to wait 10s, but timeout is 50ms)
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.RemoteAddr = "127.0.0.1:12345"
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusTooManyRequests, rr2.Code)
	})
}
