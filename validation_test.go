package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidator(t *testing.T) {
	t.Run("ValidateRequired", func(t *testing.T) {
		v := NewValidator()

		assert.False(t, v.ValidateRequired("field", ""))
		assert.False(t, v.ValidateRequired("field", "   "))
		assert.True(t, v.ValidateRequired("field", "value"))
		assert.True(t, v.HasErrors())
		assert.Len(t, v.Errors(), 2)
	})

	t.Run("ValidateMaxLength", func(t *testing.T) {
		v := NewValidator()

		assert.True(t, v.ValidateMaxLength("field", "hello", 10))
		assert.False(t, v.ValidateMaxLength("field", "hello world", 5))
		assert.True(t, v.ValidateMaxLength("field", "👋🌍", 2)) // Unicode handling
	})

	t.Run("ValidateMinLength", func(t *testing.T) {
		v := NewValidator()

		assert.True(t, v.ValidateMinLength("field", "hello", 3))
		assert.False(t, v.ValidateMinLength("field", "hi", 3))
		assert.True(t, v.ValidateMinLength("field", "👋🌍", 2)) // Unicode handling
	})

	t.Run("ValidateURL", func(t *testing.T) {
		v := NewValidator()

		// Valid URLs
		assert.True(t, v.ValidateURL("url", ""))
		assert.True(t, v.ValidateURL("url", "https://example.com"))
		assert.True(t, v.ValidateURL("url", "http://example.com/path"))
		assert.True(t, v.ValidateURL("url", "https://example.com:8080/path?query=1"))

		// Invalid URLs
		assert.False(t, v.ValidateURL("url", "not a url"))
		assert.False(t, v.ValidateURL("url", "ftp://example.com"))
		assert.False(t, v.ValidateURL("url", "//example.com"))
		assert.False(t, v.ValidateURL("url", "https://"))
	})

	t.Run("ValidateNoHTML", func(t *testing.T) {
		v := NewValidator()

		assert.True(t, v.ValidateNoHTML("field", "plain text"))
		assert.True(t, v.ValidateNoHTML("field", "text with < and >"))
		assert.False(t, v.ValidateNoHTML("field", "<script>alert('xss')</script>"))
		assert.False(t, v.ValidateNoHTML("field", "text with <b>html</b>"))
	})

	t.Run("ValidateInteger", func(t *testing.T) {
		v := NewValidator()

		val, ok := v.ValidateInteger("num", "42", 0, 100)
		assert.True(t, ok)
		assert.Equal(t, int64(42), val)

		_, ok = v.ValidateInteger("num", "150", 0, 100)
		assert.False(t, ok)

		_, ok = v.ValidateInteger("num", "-5", 0, 100)
		assert.False(t, ok)

		_, ok = v.ValidateInteger("num", "not a number", 0, 100)
		assert.False(t, ok)
	})

	t.Run("ValidateAlphanumeric", func(t *testing.T) {
		v := NewValidator()

		assert.True(t, v.ValidateAlphanumeric("field", "abc123", ""))
		assert.True(t, v.ValidateAlphanumeric("field", "test-name", "-"))
		assert.False(t, v.ValidateAlphanumeric("field", "test@email", ""))
		assert.False(t, v.ValidateAlphanumeric("field", "test space", ""))
	})
}

func TestValidateThreadForm(t *testing.T) {
	tests := []struct {
		name      string
		subject   string
		body      string
		wantError bool
	}{
		{
			name:      "valid input",
			subject:   "Test Subject",
			body:      "This is a test body with enough content",
			wantError: false,
		},
		{
			name:      "empty subject",
			subject:   "",
			body:      "This is a test body",
			wantError: true,
		},
		{
			name:      "short subject",
			subject:   "Hi",
			body:      "This is a test body",
			wantError: true,
		},
		{
			name:      "empty body",
			subject:   "Test Subject",
			body:      "",
			wantError: true,
		},
		{
			name:      "short body",
			subject:   "Test Subject",
			body:      "Too short",
			wantError: true,
		},
		{
			name:      "subject too long",
			subject:   strings.Repeat("a", 256),
			body:      "This is a test body",
			wantError: true,
		},
		{
			name:      "body too long",
			subject:   "Test Subject",
			body:      strings.Repeat("a", 10001),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateThreadForm(tt.subject, tt.body)
			if tt.wantError {
				assert.NotEmpty(t, errors)
			} else {
				assert.Empty(t, errors)
			}
		})
	}
}

func TestValidateProfileForm(t *testing.T) {
	tests := []struct {
		name          string
		photoURL      string
		location      string
		preferredName string
		bio           string
		pronouns      string
		wantError     bool
	}{
		{
			name:          "all empty (valid)",
			photoURL:      "",
			location:      "",
			preferredName: "",
			bio:           "",
			pronouns:      "",
			wantError:     false,
		},
		{
			name:          "all valid",
			photoURL:      "https://example.com/photo.jpg",
			location:      "San Francisco",
			preferredName: "John",
			bio:           "Software developer",
			pronouns:      "he/him",
			wantError:     false,
		},
		{
			name:          "invalid URL",
			photoURL:      "not a url",
			location:      "San Francisco",
			preferredName: "John",
			bio:           "Software developer",
			pronouns:      "he/him",
			wantError:     true,
		},
		{
			name:          "location too long",
			photoURL:      "",
			location:      strings.Repeat("a", 101),
			preferredName: "",
			bio:           "",
			pronouns:      "",
			wantError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateProfileForm(tt.photoURL, tt.location, tt.preferredName, tt.bio, tt.pronouns)
			if tt.wantError {
				assert.NotEmpty(t, errors)
			} else {
				assert.Empty(t, errors)
			}
		})
	}
}

func TestValidateAdminForm(t *testing.T) {
	tests := []struct {
		name       string
		boardTitle string
		editWindow string
		wantTitle  string
		wantWindow int64
		wantError  bool
	}{
		{
			name:       "valid input",
			boardTitle: "My Forum",
			editWindow: "3600",
			wantTitle:  "My Forum",
			wantWindow: 3600,
			wantError:  false,
		},
		{
			name:       "empty title",
			boardTitle: "",
			editWindow: "3600",
			wantTitle:  "",
			wantWindow: 0,
			wantError:  true,
		},
		{
			name:       "invalid edit window",
			boardTitle: "My Forum",
			editWindow: "not a number",
			wantTitle:  "My Forum",
			wantWindow: 0,
			wantError:  true,
		},
		{
			name:       "edit window too large",
			boardTitle: "My Forum",
			editWindow: "100000",
			wantTitle:  "My Forum",
			wantWindow: 0,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, window, errors := ValidateAdminForm(tt.boardTitle, tt.editWindow)
			if tt.wantError {
				assert.NotEmpty(t, errors)
			} else {
				assert.Empty(t, errors)
				assert.Equal(t, tt.wantTitle, title)
				assert.Equal(t, tt.wantWindow, window)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal text",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "extra spaces",
			input:    "  Hello   World  ",
			expected: "Hello World",
		},
		{
			name:     "newlines and tabs",
			input:    "Hello\n\t\rWorld",
			expected: "Hello World",
		},
		{
			name:     "null bytes",
			input:    "Hello\x00World",
			expected: "HelloWorld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeInput(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
