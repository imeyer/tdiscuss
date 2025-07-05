package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		expected string
	}{
		{
			name:     "normal email",
			email:    "user@example.com",
			expected: "u***@example.com",
		},
		{
			name:     "short local part",
			email:    "ab@example.com",
			expected: "***@example.com",
		},
		{
			name:     "very short local part",
			email:    "a@example.com",
			expected: "***@example.com",
		},
		{
			name:     "empty email",
			email:    "",
			expected: "",
		},
		{
			name:     "invalid email format",
			email:    "notanemail",
			expected: "***",
		},
		{
			name:     "multiple @ signs",
			email:    "user@host@example.com",
			expected: "***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskEmail(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHashEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{
			name:  "normal email",
			email: "user@example.com",
		},
		{
			name:  "empty email",
			email: "",
		},
		{
			name:  "different email",
			email: "other@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hashEmail(tt.email)

			if tt.email == "" {
				assert.Equal(t, "", result)
			} else {
				// Hash should be 8 characters long
				assert.Equal(t, 8, len(result))

				// Same email should produce same hash
				result2 := hashEmail(tt.email)
				assert.Equal(t, result, result2)
			}
		})
	}

	// Different emails should produce different hashes
	hash1 := hashEmail("user1@example.com")
	hash2 := hashEmail("user2@example.com")
	assert.NotEqual(t, hash1, hash2)
}

func TestMaskUserID(t *testing.T) {
	// Test that user IDs are consistently hashed
	id1 := maskUserID(12345)
	id2 := maskUserID(12345)
	id3 := maskUserID(54321)

	assert.Equal(t, id1, id2, "Same user ID should produce same hash")
	assert.NotEqual(t, id1, id3, "Different user IDs should produce different hashes")
	assert.Equal(t, 8, len(id1), "Hash should be 8 characters long")
}
