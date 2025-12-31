package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test helpers
func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type mockAuthProvider struct {
	shouldAuth bool
	user       *ContextUser
	err        error
}

func (m *mockAuthProvider) Authenticate(ctx context.Context, r *http.Request) (*ContextUser, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.shouldAuth && m.user != nil {
		return m.user, nil
	}
	return nil, nil
}

func (m *mockAuthProvider) CreateOrGetUser(ctx context.Context, email string) (*ContextUser, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.user != nil {
		return m.user, nil
	}
	return &ContextUser{
		ID:      1,
		Email:   email,
		IsAdmin: false,
	}, nil
}

func (m *mockAuthProvider) GetUserEmail(r *http.Request) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if m.user != nil {
		return m.user.Email, nil
	}
	return "test@example.com", nil
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		handler      http.Handler
		checkHeaders func(t *testing.T, headers http.Header)
	}{
		{
			name: "all security headers are set",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			checkHeaders: func(t *testing.T, headers http.Header) {
				// Check all security headers
				assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
				assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
				assert.Equal(t, "0", headers.Get("X-XSS-Protection"))
				assert.Equal(t, "strict-origin-when-cross-origin", headers.Get("Referrer-Policy"))
				// Check Permissions-Policy contains all required features (order doesn't matter)
				permPolicy := headers.Get("Permissions-Policy")
				assert.Contains(t, permPolicy, "camera=()")
				assert.Contains(t, permPolicy, "payment=()")
				assert.Contains(t, permPolicy, "usb=()")
				assert.Contains(t, permPolicy, "geolocation=()")
				assert.Contains(t, permPolicy, "microphone=()")

				// Check HSTS header - only set for HTTPS
				// Since test request doesn't have TLS, it shouldn't be set
				assert.Empty(t, headers.Get("Strict-Transport-Security"))

				// Check CSP header
				csp := headers.Get("Content-Security-Policy")
				assert.Contains(t, csp, "default-src 'self'")
				assert.Contains(t, csp, "script-src 'self'")
				assert.Contains(t, csp, "style-src 'self' 'unsafe-inline'")
				assert.Contains(t, csp, "img-src 'self' data: https:")
				assert.Contains(t, csp, "font-src 'self'")
				assert.Contains(t, csp, "connect-src 'self'")
				assert.Contains(t, csp, "frame-ancestors 'none'")
				assert.Contains(t, csp, "base-uri 'self'")
				assert.Contains(t, csp, "form-action 'self'")
				assert.Contains(t, csp, "upgrade-insecure-requests")
			},
		},
		{
			name: "headers are set before handler execution",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Try to override a security header
				w.Header().Set("X-Frame-Options", "SAMEORIGIN")
				w.WriteHeader(http.StatusOK)
			}),
			checkHeaders: func(t *testing.T, headers http.Header) {
				// The middleware sets headers before the handler, so handler can override
				// This test documents this behavior
				assert.Equal(t, "SAMEORIGIN", headers.Get("X-Frame-Options"))
			},
		},
		{
			name: "HSTS header set for HTTPS requests",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			checkHeaders: func(t *testing.T, headers http.Header) {
				// Should have HSTS header
				assert.Equal(t, "max-age=63072000; includeSubDomains; preload", headers.Get("Strict-Transport-Security"))
			},
		},
		{
			name: "middleware passes through response body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("test response"))
			}),
			checkHeaders: func(t *testing.T, headers http.Header) {
				// Just verify headers are still set
				assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the middleware chain
			config := DefaultSecurityConfig()
			middleware := SecurityHeadersMiddleware(config)
			handler := middleware(tt.handler)

			// Create a test request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)

			// For HSTS test, simulate HTTPS
			if tt.name == "HSTS header set for HTTPS requests" {
				req.Header.Set("X-Forwarded-Proto", "https")
			}

			rec := httptest.NewRecorder()

			// Execute the handler
			handler.ServeHTTP(rec, req)

			// Check headers
			tt.checkHeaders(t, rec.Header())
		})
	}
}

func TestRequestSizeLimitMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		bodySize       int
		maxSize        int64
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET request not limited",
			method:         http.MethodGet,
			bodySize:       1024 * 1024 * 2, // 2MB
			maxSize:        1024 * 1024,     // 1MB limit
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name:           "POST request within limit",
			method:         http.MethodPost,
			bodySize:       1024 * 512,  // 512KB
			maxSize:        1024 * 1024, // 1MB limit
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name:           "POST request exceeds limit",
			method:         http.MethodPost,
			bodySize:       1024 * 1024 * 2, // 2MB
			maxSize:        1024 * 1024,     // 1MB limit
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "",
		},
		{
			name:           "PUT request within limit",
			method:         http.MethodPut,
			bodySize:       1024 * 100,  // 100KB
			maxSize:        1024 * 1024, // 1MB limit
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name:           "PUT request exceeds limit",
			method:         http.MethodPut,
			bodySize:       1024 * 1024 * 5, // 5MB
			maxSize:        1024 * 1024,     // 1MB limit
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "",
		},
		{
			name:           "PATCH request within limit",
			method:         http.MethodPatch,
			bodySize:       1024,        // 1KB
			maxSize:        1024 * 1024, // 1MB limit
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name:           "PATCH request exceeds limit",
			method:         http.MethodPatch,
			bodySize:       1024 * 1024 * 3, // 3MB
			maxSize:        1024 * 1024,     // 1MB limit
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "",
		},
		{
			name:           "DELETE request not limited",
			method:         http.MethodDelete,
			bodySize:       1024 * 1024 * 2, // 2MB
			maxSize:        1024 * 1024,     // 1MB limit
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
		{
			name:           "Very small limit",
			method:         http.MethodPost,
			bodySize:       100,
			maxSize:        10, // 10 bytes limit
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "",
		},
		{
			name:           "Exact size limit",
			method:         http.MethodPost,
			bodySize:       1024,
			maxSize:        1024,
			expectedStatus: http.StatusOK,
			expectedBody:   "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a handler that reads the body
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					// This happens when body exceeds limit
					http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
					return
				}

				// Verify we received the expected amount of data
				if len(body) != tt.bodySize && tt.expectedStatus == http.StatusOK {
					t.Errorf("Expected body size %d, got %d", tt.bodySize, len(body))
				}

				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			// Create the middleware
			middleware := RequestSizeLimitMiddleware(tt.maxSize)(handler)

			// Create request with body
			body := bytes.Repeat([]byte("a"), tt.bodySize)
			req := httptest.NewRequest(tt.method, "/test", bytes.NewReader(body))
			req.ContentLength = int64(tt.bodySize)

			rec := httptest.NewRecorder()

			// Execute the middleware
			middleware.ServeHTTP(rec, req)

			// Check response
			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedBody != "" {
				assert.Equal(t, tt.expectedBody, rec.Body.String())
			}
		})
	}
}

func TestRequestSizeLimitMiddleware_ReadPartially(t *testing.T) {
	// Test that partial reads work correctly
	maxSize := int64(1024) // 1KB limit

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read only 100 bytes
		buf := make([]byte, 100)
		n, err := r.Body.Read(buf)

		if err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("read " + strconv.Itoa(n) + " bytes"))
	})

	middleware := RequestSizeLimitMiddleware(maxSize)(handler)

	// Create request with 500 bytes (within limit)
	body := bytes.Repeat([]byte("a"), 500)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "read")
}

// TestCSRFProtectionMiddleware tests the Go 1.25 built-in CSRF protection
func TestCSRFProtectionMiddleware(t *testing.T) {
	config := defaultSecurityConfig()
	middleware := csrfProtectionMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrapped := middleware(handler)

	t.Run("allows safe methods", func(t *testing.T) {
		safeMethods := []string{"GET", "HEAD", "OPTIONS"}

		for _, method := range safeMethods {
			req := httptest.NewRequest(method, "/", nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "Should allow %s without additional checks", method)
		}
	})

	t.Run("blocks cross-origin requests for unsafe methods", func(t *testing.T) {
		unsafeMethods := []string{"POST", "PUT", "PATCH", "DELETE"}

		for _, method := range unsafeMethods {
			req := httptest.NewRequest(method, "/", nil)
			// Simulate a cross-origin request
			req.Header.Set("Origin", "https://evil.com")
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			// The exact status depends on Go 1.25's implementation
			// but it should reject cross-origin requests
			assert.NotEqual(t, http.StatusOK, w.Code, "Should block cross-origin %s", method)
		}
	})
}


func TestCSRFMiddlewareIntegration(t *testing.T) {
	// Test CSRF protection with a simpler approach
	config := defaultSecurityConfig()
	csrfMiddleware := csrfProtectionMiddleware(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("success"))
	})

	t.Run("CSRF protection blocks cross-origin requests", func(t *testing.T) {
		wrapped := csrfMiddleware(handler)

		// Test same-origin POST - should pass
		req := httptest.NewRequest("POST", "/", nil)
		w := httptest.NewRecorder()

		wrapped.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Same-origin POST should pass CSRF check")

		// Test cross-origin POST - should be blocked
		req = httptest.NewRequest("POST", "/", nil)
		req.Header.Set("Origin", "https://evil.com")
		w = httptest.NewRecorder()

		wrapped.ServeHTTP(w, req)

		// Should be blocked by CSRF protection
		assert.Equal(t, http.StatusForbidden, w.Code, "Cross-origin POST should be blocked by CSRF")
		assert.Contains(t, w.Body.String(), "Cross-origin request rejected")
	})
}

func TestMiddlewareChaining(t *testing.T) {
	// Test that multiple middlewares work correctly together
	var executionOrder []string

	// Create a handler that records execution
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executionOrder = append(executionOrder, "handler")

		// Try to read body to trigger size limit
		io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})

	// Create a custom middleware to track execution order
	trackingMiddleware := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executionOrder = append(executionOrder, name+"-before")
				next.ServeHTTP(w, r)
				executionOrder = append(executionOrder, name+"-after")
			})
		}
	}

	// Chain middlewares
	config := DefaultSecurityConfig()
	securityHeadersMiddleware := SecurityHeadersMiddleware(config)
	sizeLimitMiddleware := RequestSizeLimitMiddleware(1024)

	// Build the chain
	chain := trackingMiddleware("outer")(
		sizeLimitMiddleware(
			securityHeadersMiddleware(handler),
		),
	)

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("test body"))
	rec := httptest.NewRecorder()

	// Execute
	executionOrder = []string{} // Reset
	chain.ServeHTTP(rec, req)

	// Verify execution order
	expected := []string{"outer-before", "handler", "outer-after"}
	assert.Equal(t, expected, executionOrder)

	// Verify headers are still set
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSecurityHeadersMiddleware_VariousContentTypes(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "JSON response",
			contentType: "application/json",
			body:        `{"message":"test"}`,
		},
		{
			name:        "HTML response",
			contentType: "text/html",
			body:        "<html><body>test</body></html>",
		},
		{
			name:        "Plain text response",
			contentType: "text/plain",
			body:        "plain text",
		},
		{
			name:        "No content type",
			contentType: "",
			body:        "no content type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.Write([]byte(tt.body))
			})

			config := DefaultSecurityConfig()
			middleware := SecurityHeadersMiddleware(config)
			chain := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			chain.ServeHTTP(rec, req)

			// Security headers should be set regardless of content type
			assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))

			// Content type should be preserved
			if tt.contentType != "" {
				assert.Equal(t, tt.contentType, rec.Header().Get("Content-Type"))
			}

			// Body should be unchanged
			assert.Equal(t, tt.body, rec.Body.String())
		})
	}
}

func BenchmarkSecurityHeadersMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	config := DefaultSecurityConfig()
	middleware := SecurityHeadersMiddleware(config)
	chain := middleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
	}
}

func BenchmarkRequestSizeLimitMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("OK"))
	})

	middleware := RequestSizeLimitMiddleware(1024 * 1024)(handler)
	body := bytes.Repeat([]byte("a"), 1024) // 1KB body

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}
}

func TestHashEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{name: "normal email", email: "user@example.com"},
		{name: "empty email", email: ""},
		{name: "different email", email: "other@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HashEmail(tt.email)

			if tt.email == "" {
				assert.Equal(t, "", result)
			} else {
				// Hash should be 16 characters long (8 bytes as hex)
				assert.Equal(t, 16, len(result))

				// Same email should produce same hash
				result2 := HashEmail(tt.email)
				assert.Equal(t, result, result2)
			}
		})
	}

	// Different emails should produce different hashes
	hash1 := HashEmail("user1@example.com")
	hash2 := HashEmail("user2@example.com")
	assert.NotEqual(t, hash1, hash2)
}
