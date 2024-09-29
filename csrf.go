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
	csrfHeaderName  = "X-CSRF-Token"
	csrfContextKey  = "CSRFToken"
	cleanupInterval = 1 * time.Hour
	tokenExpiryTime = 12 * time.Hour
)

var (
	tokenStore   = make(map[string]time.Time)
	tokenStoreMu sync.RWMutex
	timeNow      = time.Now
	csrfLogger   *slog.Logger
)

func initCSRFLogger(logger *slog.Logger) {
	csrfLogger = logger
}

func generateCSRFToken() string {
	b := make([]byte, csrfTokenLength)
	if _, err := rand.Read(b); err != nil {
		if csrfLogger != nil {
			csrfLogger.Error("Failed to generate CSRF token", "error", err)
		}
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func setCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	token := generateCSRFToken()
	if token == "" {
		return "", errors.New("Failed to generate CSRF token")
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

func GetCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(csrfContextKey).(string); ok {
		return token
	}
	return ""
}

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

func CSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
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

func init() {
	go startCleanupRoutine(context.Background())
}
