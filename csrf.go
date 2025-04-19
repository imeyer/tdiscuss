package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	csrfTokenLength = 32
	csrfCookieName  = "csrf_token"
	csrfHeaderName  = "X-Csrf-Token"
	csrfContextKey  = "CSRFToken"
	cleanupInterval = 1 * time.Hour
	tokenExpiryTime = 12 * time.Hour
)

var (
	tokenStore   = make(map[string]time.Time)
	tokenStoreMu sync.RWMutex
	timeNow      = time.Now
	csrfLogger   *slog.Logger

	// For tests to control the cleanup routine
	cleanupCtx, cleanupCancel = context.WithCancel(context.Background())
	cleanupInitialized        = false
	cleanupInitMu             sync.Mutex

	// For testing - if true, generateCSRFToken will return an error
	generateTokenError bool
	generateTokenMu    sync.RWMutex
)

// SetGenerateTokenError sets the flag to simulate an error in token generation
// This is for testing purposes only
func SetGenerateTokenError(shouldError bool) {
	generateTokenMu.Lock()
	generateTokenError = shouldError
	generateTokenMu.Unlock()
}

// initCSRFLogger sets the logger for CSRF operations
func initCSRFLogger(logger *slog.Logger) {
	csrfLogger = logger
}

// generateCSRFToken creates a new random token for CSRF protection
func generateCSRFToken() (string, error) {
	// Check test flag first
	generateTokenMu.RLock()
	if generateTokenError {
		generateTokenMu.RUnlock()
		return "", errors.New("simulated token generation error")
	}
	generateTokenMu.RUnlock()

	b := make([]byte, csrfTokenLength)
	_, err := rand.Read(b)
	if err != nil {
		if csrfLogger != nil {
			csrfLogger.Error("Failed to generate CSRF token", "error", err)
		}
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// setCSRFToken creates a new CSRF token, adds it to the response as a cookie,
// and stores it in the token store
func setCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	token, err := generateCSRFToken()
	if err != nil {
		return "", err
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(tokenExpiryTime.Seconds()),
	})

	tokenStoreMu.Lock()
	tokenStore[token] = timeNow().Add(tokenExpiryTime)
	tokenStoreMu.Unlock()

	return token, nil
}

// GetCSRFToken retrieves the CSRF token from the request context
func GetCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(csrfContextKey).(string); ok {
		return token
	}
	return ""
}

// validateCSRFToken ensures that the request has a valid CSRF token
func validateCSRFToken(r *http.Request) error {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		if csrfLogger != nil {
			csrfLogger.Debug("CSRF cookie not found", "error", err)
		}
		return errors.New("CSRF cookie not found")
	}

	token := r.Header.Get(csrfHeaderName)
	if token == "" {
		token = r.FormValue("csrf_token")
		if token == "" {
			if csrfLogger != nil {
				csrfLogger.Debug("CSRF token not found in header or form")
			}
			return errors.New("CSRF token not found")
		}
	}

	if cookie.Value != token {
		if csrfLogger != nil {
			csrfLogger.Debug("CSRF token mismatch")
		}
		return errors.New("CSRF token mismatch")
	}

	tokenStoreMu.RLock()
	expiry, exists := tokenStore[token]
	tokenStoreMu.RUnlock()

	if !exists {
		if csrfLogger != nil {
			csrfLogger.Debug("CSRF token not found in store")
		}
		return errors.New("CSRF token not found in store")
	}

	if timeNow().After(expiry) {
		if csrfLogger != nil {
			csrfLogger.Debug("CSRF token expired")
		}
		return errors.New("CSRF token expired")
	}

	if csrfLogger != nil {
		csrfLogger.Debug("CSRF validation successful")
	}
	return nil
}

// CSRFMiddleware provides protection against CSRF attacks
func CSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
	ensureCleanupRoutineStarted()

	return func(w http.ResponseWriter, r *http.Request) {
		var token string
		var err error

		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			token, err = setCSRFToken(w, r)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			w.Header().Set(csrfHeaderName, token)
		} else {
			if err := validateCSRFToken(r); err != nil {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
			token = r.Header.Get(csrfHeaderName)
			if token == "" {
				token = r.FormValue("csrf_token")
			}
		}

		ctx := context.WithValue(r.Context(), csrfContextKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// cleanupExpiredTokens removes expired tokens from the token store
func cleanupExpiredTokens() {
	tokenStoreMu.Lock()
	defer tokenStoreMu.Unlock()
	now := timeNow()
	for token, expiry := range tokenStore {
		if now.After(expiry) {
			delete(tokenStore, token)
		}
	}

	if csrfLogger != nil {
		csrfLogger.Debug("Cleaned up expired CSRF tokens")
	}
}

// startCleanupRoutine starts a goroutine that periodically cleans up expired tokens
func startCleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cleanupExpiredTokens()
		case <-ctx.Done():
			return
		}
	}
}

// ensureCleanupRoutineStarted makes sure the cleanup routine is running
func ensureCleanupRoutineStarted() {
	cleanupInitMu.Lock()
	defer cleanupInitMu.Unlock()

	if !cleanupInitialized {
		go startCleanupRoutine(cleanupCtx)
		cleanupInitialized = true
	}
}

// StopCleanupRoutine stops the token cleanup routine (mainly for tests)
func StopCleanupRoutine() {
	cleanupInitMu.Lock()
	defer cleanupInitMu.Unlock()

	if cleanupInitialized {
		cleanupCancel()
		// Create new context for future use
		cleanupCtx, cleanupCancel = context.WithCancel(context.Background())
		cleanupInitialized = false
	}
}

// ResetTokenStore clears the token store (mainly for tests)
func ResetTokenStore() {
	tokenStoreMu.Lock()
	defer tokenStoreMu.Unlock()

	// Clear the map
	for k := range tokenStore {
		delete(tokenStore, k)
	}
}
