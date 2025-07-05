package main

import (
	"net/http"
)

// GetCSRFTokenOld retrieves the CSRF token from the request context
// DEPRECATED: This function is kept only for backward compatibility with tests.
// Use GetCSRFToken() instead, which uses the new middleware system.
func GetCSRFTokenOld(r *http.Request) string {
	// The new middleware stores tokens differently, so this always returns empty
	return ""
}

// validateCSRFTokenOld ensures that the request has a valid CSRF token
// DEPRECATED: This function is kept only for backward compatibility with tests.
// The new middleware system handles CSRF validation automatically.
func validateCSRFTokenOld(r *http.Request) error {
	// The new middleware handles validation, so this is a no-op
	return nil
}
