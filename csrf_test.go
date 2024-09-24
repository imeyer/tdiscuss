package main

import (
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateCSRFToken(t *testing.T) {
	token, err := generateCSRFToken()
	if err != nil {
		t.Fatalf("generateCSRFToken() error = %v", err)
	}
	if len(token) == 0 {
		t.Error("generateCSRFToken() returned empty token")
	}

	// Test error condition
	oldRand := rand.Reader
	rand.Reader = strings.NewReader("")
	defer func() { rand.Reader = oldRand }()

	_, err = generateCSRFToken()
	if err == nil {
		t.Error("generateCSRFToken() did not return error when rand.Read fails")
	}
}

func TestSetCSRFToken(t *testing.T) {
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

			token, err := setCSRFToken(r, w)
			if err != nil {
				t.Fatalf("setCSRFToken() error = %v", err)
			}

			cookies := w.Result().Cookies()
			var csrfCookie *http.Cookie
			for _, cookie := range cookies {
				if cookie.Name == csrfCookieName {
					csrfCookie = cookie
					break
				}
			}

			if csrfCookie == nil {
				t.Fatal("CSRF cookie not set")
			}

			if csrfCookie.Value != token {
				t.Errorf("Cookie value (%s) does not match returned token (%s)", csrfCookie.Value, token)
			}

			if !csrfCookie.HttpOnly {
				t.Error("CSRF cookie is not HttpOnly")
			}

			if csrfCookie.SameSite != http.SameSiteStrictMode {
				t.Error("CSRF cookie SameSite is not set to Strict")
			}

			if csrfCookie.Secure != tt.wantSecure {
				t.Errorf("CSRF cookie Secure flag is %v, want %v", csrfCookie.Secure, tt.wantSecure)
			}
		})
	}
}

func TestValidateCSRFToken(t *testing.T) {
	// Setup
	r, _ := http.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()

	token, _ := setCSRFToken(r, w)

	// Test valid token
	r.Header.Set(csrfHeaderName, token)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})

	err := validateCSRFToken(r)
	if err != nil {
		t.Errorf("validateCSRFToken() error = %v", err)
	}

	// Test invalid token
	r.Header.Set(csrfHeaderName, "invalid_token")
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for invalid token")
	}

	// Test missing cookie
	r, _ = http.NewRequest("POST", "/", nil)
	r.Header.Set(csrfHeaderName, token)
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for missing cookie")
	}

	// Test missing header
	r, _ = http.NewRequest("POST", "/", nil)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for missing header")
	}

	// Test expired token
	r.Header.Set(csrfHeaderName, token)
	tokenStoreMu.Lock()
	tokenStore[token] = time.Now().Add(-1 * time.Hour)
	tokenStoreMu.Unlock()
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for expired token")
	}

	// Test token not in store
	delete(tokenStore, token)
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for token not in store")
	}
}

func TestCSRFMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)

	// Test GET request
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Middleware returned wrong status code for GET: got %v want %v", w.Code, http.StatusOK)
	}

	token := w.Header().Get(csrfHeaderName)
	if token == "" {
		t.Error("CSRF token not set in header for GET request")
	}

	// Test POST request with valid token
	r, _ = http.NewRequest("POST", "/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.Header.Set(csrfHeaderName, token)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	middleware.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Middleware returned wrong status code for valid POST: got %v want %v", w.Code, http.StatusOK)
	}

	// Test POST request with invalid token
	r, _ = http.NewRequest("POST", "/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.Header.Set(csrfHeaderName, "invalid_token")
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	middleware.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("Middleware returned wrong status code for invalid POST: got %v want %v", w.Code, http.StatusForbidden)
	}

	// Test error in setCSRFToken
	oldRand := rand.Reader
	rand.Reader = strings.NewReader("")
	defer func() { rand.Reader = oldRand }()

	r, _ = http.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	middleware.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Middleware returned wrong status code for setCSRFToken error: got %v want %v", w.Code, http.StatusInternalServerError)
	}
}

// Mock time.Now for testing token expiration
func mockTimeNow(mockTime time.Time) func() {
	old := timeNow
	timeNow = func() time.Time {
		return mockTime
	}
	return func() {
		timeNow = old
	}
}

func TestTokenExpiration(t *testing.T) {
	originalTimeNow := timeNow
	defer func() { timeNow = originalTimeNow }()

	// Set current time
	now := time.Now()
	timeNow = func() time.Time { return now }

	// Create a request and set the CSRF token
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	token, err := setCSRFToken(r, w)
	if err != nil {
		t.Fatalf("setCSRFToken() error = %v", err)
	}

	// Prepare a new request for validation
	r, _ = http.NewRequest("POST", "/", nil)
	r.Header.Set(csrfHeaderName, token)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})

	// Token should be valid initially
	err = validateCSRFToken(r)
	if err != nil {
		t.Errorf("validateCSRFToken() error = %v, token should be valid", err)
	}

	// Set current time to 23 hours from now
	timeNow = func() time.Time { return now.Add(23 * time.Hour) }

	// Token should still be valid
	err = validateCSRFToken(r)
	if err != nil {
		t.Errorf("validateCSRFToken() error = %v, token should still be valid", err)
	}

	// Set current time to 25 hours from now
	timeNow = func() time.Time { return now.Add(25 * time.Hour) }

	// Token should be expired
	err = validateCSRFToken(r)
	if err == nil {
		t.Error("validateCSRFToken() did not return error for expired token")
	}
}
