package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	csrfTokenLength = 32
	csrfCookieName  = "csrf_token"
	csrfHeaderName  = "X-CSRF-Token"
)

var (
	tokenStore   = make(map[string]time.Time)
	tokenStoreMu sync.Mutex
)

func generateCSRFToken() (string, error) {
	b := make([]byte, csrfTokenLength)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func setCSRFToken(r *http.Request, w http.ResponseWriter) (string, error) {
	token, err := generateCSRFToken()
	if err != nil {
		return "", err
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		HttpOnly: true,
		Secure:   isSecure, // Set to true if using HTTPS
		SameSite: http.SameSiteStrictMode,
	})

	tokenStoreMu.Lock()
	tokenStore[token] = time.Now().Add(24 * time.Hour) // Token expires in 24 hours
	tokenStoreMu.Unlock()

	return token, nil
}

func validateCSRFToken(r *http.Request) error {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return errors.New("CSRF cookie not found")
	}

	token := r.Header.Get(csrfHeaderName)
	if token == "" {
		return errors.New("CSRF token not found in header")
	}

	if cookie.Value != token {
		return errors.New("CSRF token mismatch")
	}

	tokenStoreMu.Lock()
	defer tokenStoreMu.Unlock()

	expiry, exists := tokenStore[token]
	if !exists {
		return errors.New("CSRF token not found in store")
	}

	if time.Now().After(expiry) {
		delete(tokenStore, token)
		return errors.New("CSRF token expired")
	}

	return nil
}

func CSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			token, err := setCSRFToken(r, w)
			if err != nil {
				http.Error(w, "Failed to set CSRF token", http.StatusInternalServerError)
				return
			}
			w.Header().Set(csrfHeaderName, token)
		} else {
			if err := validateCSRFToken(r); err != nil {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	}
}
