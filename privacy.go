package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// maskEmail masks an email address for logging purposes
// Example: "user@example.com" -> "u***@example.com"
func maskEmail(email string) string {
	if email == "" {
		return ""
	}

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		// Not a valid email format, return masked string
		return "***"
	}

	localPart := parts[0]
	domain := parts[1]

	// If local part is very short, just mask it entirely
	if len(localPart) <= 2 {
		return "***@" + domain
	}

	// Show first character and mask the rest
	return localPart[0:1] + "***@" + domain
}

// hashEmail creates a consistent hash of an email for correlation without exposing PII
// Useful for debugging and tracking issues for specific users
func hashEmail(email string) string {
	if email == "" {
		return ""
	}

	h := sha256.New()
	h.Write([]byte(email))
	hash := h.Sum(nil)

	// Return first 8 characters of hex hash for brevity
	return fmt.Sprintf("%x", hash)[:8]
}

// maskUserID returns a shortened hash suitable for logging
// This allows correlation of logs without exposing the actual user ID
func maskUserID(userID int64) string {
	return hashEmail(fmt.Sprintf("user:%d", userID))
}
