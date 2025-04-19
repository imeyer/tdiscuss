package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	// Set up
	origTimeNow := timeNow

	// Run tests
	m.Run()

	// Clean up
	timeNow = origTimeNow
	ResetTokenStore()
	StopCleanupRoutine()
	SetGenerateTokenError(false)
}

func TestValidateCSRFToken(t *testing.T) {
	// Reset state before the test
	ResetTokenStore()
	SetGenerateTokenError(false)

	// Setup
	r, _ := http.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	token, err := setCSRFToken(w, r)
	if err != nil {
		t.Fatalf("setCSRFToken() error = %v", err)
	}

	// Test valid token
	r.Header.Set(csrfHeaderName, token)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})

	err = validateCSRFToken(r)
	assert.NoError(t, err, "validateCSRFToken() should not error with valid token")

	// Test invalid token
	r.Header.Set(csrfHeaderName, "invalid_token")
	err = validateCSRFToken(r)
	assert.Error(t, err, "validateCSRFToken() should error with invalid token")

	// Test missing cookie
	r, _ = http.NewRequest("POST", "/", nil)
	r.Header.Set(csrfHeaderName, token)
	err = validateCSRFToken(r)
	assert.Error(t, err, "validateCSRFToken() should error with missing cookie")

	// Test missing header
	r, _ = http.NewRequest("POST", "/", nil)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	err = validateCSRFToken(r)
	assert.Error(t, err, "validateCSRFToken() should error with missing header")

	// Test expired token
	r.Header.Set(csrfHeaderName, token)
	tokenStoreMu.Lock()
	tokenStore[token] = time.Now().Add(-1 * time.Hour)
	tokenStoreMu.Unlock()
	err = validateCSRFToken(r)
	assert.Error(t, err, "validateCSRFToken() should error with expired token")

	// Test token not in store
	tokenStoreMu.Lock()
	delete(tokenStore, token)
	tokenStoreMu.Unlock()
	err = validateCSRFToken(r)
	assert.Error(t, err, "validateCSRFToken() should error with token not in store")
}

func TestCSRFMiddleware(t *testing.T) {
	// Reset state before the test
	ResetTokenStore()
	SetGenerateTokenError(false)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := CSRFMiddleware(handler)

	// Test GET request
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "Middleware returned wrong status code for GET")

	token := w.Header().Get(csrfHeaderName)
	assert.NotEmpty(t, token, "CSRF token not set in header for GET request")

	// Get the token from the cookie
	var cookieToken string
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			cookieToken = cookie.Value
			break
		}
	}
	assert.NotEmpty(t, cookieToken, "CSRF token not set in cookie for GET request")

	// Test POST request with valid token
	r, _ = http.NewRequest("POST", "/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.Header.Set(csrfHeaderName, cookieToken)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieToken})
	middleware.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "Middleware returned wrong status code for valid POST")

	// Test POST request with invalid token
	r, _ = http.NewRequest("POST", "/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.Header.Set(csrfHeaderName, "invalid_token")
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieToken})
	middleware.ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code, "Middleware returned wrong status code for invalid POST")

	// Test error in setCSRFToken in a separate subtest to properly isolate error simulation
	t.Run("Test setCSRFToken error", func(t *testing.T) {
		// Enable the error flag
		SetGenerateTokenError(true)
		// Make sure we reset the flag when we're done
		defer SetGenerateTokenError(false)

		r, _ := http.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code, "Middleware returned wrong status code for setCSRFToken error")
	})
}

func TestTokenExpiration(t *testing.T) {
	// Reset state before the test
	ResetTokenStore()
	SetGenerateTokenError(false)

	originalTimeNow := timeNow
	defer func() { timeNow = originalTimeNow }()

	// Set current time
	now := time.Now()
	timeNow = func() time.Time { return now }

	// Create a request and set the CSRF token
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	token, err := setCSRFToken(w, r)
	assert.NoError(t, err, "setCSRFToken() should not error")

	// Prepare a new request for validation
	r, _ = http.NewRequest("POST", "/", nil)
	r.Header.Set(csrfHeaderName, token)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})

	// Token should be valid initially
	err = validateCSRFToken(r)
	assert.NoError(t, err, "Token should be valid initially")

	// Set current time to 11 hours from now (within expiry)
	timeNow = func() time.Time { return now.Add(11 * time.Hour) }

	// Token should still be valid
	err = validateCSRFToken(r)
	assert.NoError(t, err, "Token should still be valid at 11 hours")

	// Set current time to 15 hours from now (past expiry)
	timeNow = func() time.Time { return now.Add(15 * time.Hour) }

	// Token should be expired
	err = validateCSRFToken(r)
	assert.Error(t, err, "Token should be expired at 15 hours")
}

func TestGetCSRFToken(t *testing.T) {
	tests := []struct {
		name     string
		context  context.Context
		expected string
	}{
		{
			name:     "Token present in context",
			context:  context.WithValue(context.Background(), csrfContextKey, "test_token"),
			expected: "test_token",
		},
		{
			name:     "Token not present in context",
			context:  context.Background(),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r = r.WithContext(tt.context)

			token := GetCSRFToken(r)
			assert.Equal(t, tt.expected, token)
		})
	}
}

func TestSetCSRFToken(t *testing.T) {
	// Reset state before the test
	ResetTokenStore()
	SetGenerateTokenError(false)

	tests := []struct {
		name           string
		tls            *tls.ConnectionState
		forwardedProto string
		wantSecure     bool
	}{
		{"HTTP request", nil, "", false},
		{"HTTPS request", &tls.ConnectionState{}, "", true},
		{"HTTP request with X-Forwarded-Proto: https", nil, "https", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r.TLS = tt.tls
			if tt.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			w := httptest.NewRecorder()

			token, err := setCSRFToken(w, r)
			assert.NoError(t, err, "setCSRFToken() should not error")
			assert.NotEmpty(t, token, "setCSRFToken() should return a non-empty token")

			cookies := w.Result().Cookies()
			var csrfCookie *http.Cookie
			for _, cookie := range cookies {
				if cookie.Name == csrfCookieName {
					csrfCookie = cookie
					break
				}
			}

			assert.NotNil(t, csrfCookie, "CSRF cookie not set")
			assert.Equal(t, token, csrfCookie.Value, "Cookie value does not match returned token")
			assert.True(t, csrfCookie.HttpOnly, "CSRF cookie is not HttpOnly")
			assert.Equal(t, http.SameSiteStrictMode, csrfCookie.SameSite, "CSRF cookie SameSite is not set to Strict")
			assert.Equal(t, tt.wantSecure, csrfCookie.Secure, "CSRF cookie Secure flag is incorrect")

			tokenStoreMu.RLock()
			expiry, exists := tokenStore[token]
			tokenStoreMu.RUnlock()

			assert.True(t, exists, "Token not found in token store")
			assert.Less(t, time.Now().Add(tokenExpiryTime).Sub(expiry), time.Second)
		})
	}
}

func TestGenerateCSRFToken(t *testing.T) {
	// Reset state before the test
	SetGenerateTokenError(false)

	// Test successful token generation
	token, err := generateCSRFToken()
	assert.NoError(t, err, "generateCSRFToken() should not error")
	assert.NotEmpty(t, token, "generateCSRFToken() returned empty token")
	assert.Equal(t, base64.StdEncoding.EncodedLen(csrfTokenLength), len(token),
		"generateCSRFToken() returned token of incorrect length")

	// Test error condition in a subtest to properly isolate error simulation
	t.Run("Test error in token generation", func(t *testing.T) {
		// Enable the error flag
		SetGenerateTokenError(true)
		// Make sure we reset the flag when we're done
		defer SetGenerateTokenError(false)

		token, err := generateCSRFToken()
		assert.Error(t, err, "Should return error")
		assert.Equal(t, "", token, "Should return empty string on error")
	})
}

func TestInitCSRFLogger(t *testing.T) {
	// Create a new logger
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	// Initialize the CSRF logger
	initCSRFLogger(logger)

	// Check if the logger was set correctly
	assert.Equal(t, logger, csrfLogger)

	// Reset the logger to nil
	initCSRFLogger(nil)

	// Check if the logger was reset correctly
	assert.Nil(t, csrfLogger)
}

func TestCleanupExpiredTokens(t *testing.T) {
	// Reset state before the test
	ResetTokenStore()

	// Set a fixed time for the test
	originalTimeNow := timeNow
	defer func() { timeNow = originalTimeNow }()

	now := time.Now()
	timeNow = func() time.Time { return now }

	// Create some tokens with different expiry times
	tokenStoreMu.Lock()
	tokenStore["expired1"] = now.Add(-1 * time.Hour)
	tokenStore["expired2"] = now.Add(-5 * time.Minute)
	tokenStore["valid1"] = now.Add(1 * time.Hour)
	tokenStore["valid2"] = now.Add(5 * time.Hour)
	tokenStoreMu.Unlock()

	// Run cleanup
	cleanupExpiredTokens()

	// Check that expired tokens are removed
	tokenStoreMu.RLock()
	_, expired1Exists := tokenStore["expired1"]
	_, expired2Exists := tokenStore["expired2"]
	_, valid1Exists := tokenStore["valid1"]
	_, valid2Exists := tokenStore["valid2"]
	tokenStoreMu.RUnlock()

	assert.False(t, expired1Exists, "Expired token 'expired1' should be removed")
	assert.False(t, expired2Exists, "Expired token 'expired2' should be removed")
	assert.True(t, valid1Exists, "Valid token 'valid1' should be kept")
	assert.True(t, valid2Exists, "Valid token 'valid2' should be kept")
}

func TestSetGenerateTokenError(t *testing.T) {
	// Reset state
	SetGenerateTokenError(false)

	// Initially, the error flag should be false
	generateTokenMu.RLock()
	initialValue := generateTokenError
	generateTokenMu.RUnlock()
	assert.False(t, initialValue, "Initial value of generateTokenError should be false")

	// Set the flag to true
	SetGenerateTokenError(true)

	// Check that the flag was set
	generateTokenMu.RLock()
	newValue := generateTokenError
	generateTokenMu.RUnlock()
	assert.True(t, newValue, "Value of generateTokenError should be true after setting")

	// Reset the flag back to false for other tests
	SetGenerateTokenError(false)
}
