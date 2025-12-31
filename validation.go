package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ValidationError represents a validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors represents multiple validation errors
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	var messages []string
	for _, err := range e {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}

// Validator provides input validation functions
type Validator struct {
	errors ValidationErrors
}

// NewValidator creates a new validator
func NewValidator() *Validator {
	return &Validator{
		errors: make(ValidationErrors, 0),
	}
}

// HasErrors returns true if there are validation errors
func (v *Validator) HasErrors() bool {
	return len(v.errors) > 0
}

// Errors returns all validation errors
func (v *Validator) Errors() ValidationErrors {
	return v.errors
}

// AddError adds a validation error
func (v *Validator) AddError(field, message string) {
	v.errors = append(v.errors, ValidationError{
		Field:   field,
		Message: message,
	})
}

// Clear clears all validation errors
func (v *Validator) Clear() {
	v.errors = make(ValidationErrors, 0)
}

// ValidateRequired validates that a field is not empty
func (v *Validator) ValidateRequired(field, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		v.AddError(field, "is required")
		return false
	}
	return true
}

// ValidateMaxLength validates maximum length
func (v *Validator) ValidateMaxLength(field, value string, maxLength int) bool {
	if utf8.RuneCountInString(value) > maxLength {
		v.AddError(field, fmt.Sprintf("must not exceed %d characters", maxLength))
		return false
	}
	return true
}

// ValidateMinLength validates minimum length
func (v *Validator) ValidateMinLength(field, value string, minLength int) bool {
	if utf8.RuneCountInString(value) < minLength {
		v.AddError(field, fmt.Sprintf("must be at least %d characters", minLength))
		return false
	}
	return true
}

// ValidateURL validates that a string is a valid URL
func (v *Validator) ValidateURL(field, value string) bool {
	// Allow empty URLs
	if value == "" {
		return true
	}

	// Parse the URL
	u, err := url.Parse(value)
	if err != nil {
		v.AddError(field, "must be a valid URL")
		return false
	}

	// Check for required components
	if u.Scheme == "" || u.Host == "" {
		v.AddError(field, "must be a complete URL with scheme and host")
		return false
	}

	// Only allow http and https
	if u.Scheme != "http" && u.Scheme != "https" {
		v.AddError(field, "must use http or https protocol")
		return false
	}

	return true
}

// ValidateNoHTML validates that a string contains no HTML tags
func (v *Validator) ValidateNoHTML(field, value string) bool {
	// Check for HTML-like tags with tag names
	// This matches <tagname> or </tagname> or <tagname attr="value">
	htmlRegex := regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	if htmlRegex.MatchString(value) {
		v.AddError(field, "must not contain HTML tags")
		return false
	}
	return true
}

// ValidateInteger validates that a string can be parsed as an integer within bounds
func (v *Validator) ValidateInteger(field, value string, min, max int64) (int64, bool) {
	var num int64
	_, err := fmt.Sscanf(value, "%d", &num)
	if err != nil {
		v.AddError(field, "must be a valid number")
		return 0, false
	}

	if num < min {
		v.AddError(field, fmt.Sprintf("must be at least %d", min))
		return 0, false
	}

	if num > max {
		v.AddError(field, fmt.Sprintf("must not exceed %d", max))
		return 0, false
	}

	return num, true
}

// ValidateAlphanumeric validates that a string contains only alphanumeric characters and allowed extras
func (v *Validator) ValidateAlphanumeric(field, value, allowedExtras string) bool {
	allowed := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" + allowedExtras
	for _, r := range value {
		if !strings.ContainsRune(allowed, r) {
			v.AddError(field, "contains invalid characters")
			return false
		}
	}
	return true
}

// Form validation constants
const (
	MaxTitleLength    = 200
	MaxSubjectLength  = 255
	MaxBodyLength     = 10000
	MaxURLLength      = 500
	MaxLocationLength = 100
	MaxNameLength     = 100
	MaxBioLength      = 1000
	MaxPronounsLength = 50
	MinEditWindow     = 0
	MaxEditWindow     = 86400 // 24 hours in seconds
)

// ValidateThreadForm validates new thread creation form
func ValidateThreadForm(subject, body string) ValidationErrors {
	v := NewValidator()

	// Validate subject
	if v.ValidateRequired("subject", subject) {
		v.ValidateMaxLength("subject", subject, MaxSubjectLength)
		v.ValidateMinLength("subject", subject, 3)
	}

	// Validate body
	if v.ValidateRequired("body", body) {
		v.ValidateMaxLength("body", body, MaxBodyLength)
		v.ValidateMinLength("body", body, 1)
	}

	return v.Errors()
}

// ValidateThreadPostForm validates thread post/reply form
func ValidateThreadPostForm(body string) ValidationErrors {
	v := NewValidator()

	// Validate body
	if v.ValidateRequired("body", body) {
		v.ValidateMaxLength("body", body, MaxBodyLength)
		v.ValidateMinLength("body", body, 1)
	}

	return v.Errors()
}

// ValidateProfileForm validates member profile edit form
func ValidateProfileForm(photoURL, location, preferredName, bio, pronouns string) ValidationErrors {
	v := NewValidator()

	// Photo URL is optional but must be valid if provided
	if photoURL != "" {
		v.ValidateURL("photo_url", photoURL)
		v.ValidateMaxLength("photo_url", photoURL, MaxURLLength)
	}

	// All text fields are optional but have max lengths
	v.ValidateMaxLength("location", location, MaxLocationLength)
	v.ValidateMaxLength("preferred_name", preferredName, MaxNameLength)
	v.ValidateMaxLength("bio", bio, MaxBioLength)
	v.ValidateMaxLength("pronouns", pronouns, MaxPronounsLength)

	return v.Errors()
}

// ValidateAdminForm validates admin settings form
func ValidateAdminForm(boardTitle, editWindowStr string) (string, int64, ValidationErrors) {
	v := NewValidator()

	// Validate board title
	if v.ValidateRequired("board_title", boardTitle) {
		v.ValidateMaxLength("board_title", boardTitle, MaxTitleLength)
		v.ValidateMinLength("board_title", boardTitle, 1)
	}

	// Validate edit window
	editWindow := int64(0)
	if v.ValidateRequired("edit_window", editWindowStr) {
		if val, ok := v.ValidateInteger("edit_window", editWindowStr, MinEditWindow, MaxEditWindow); ok {
			editWindow = val
		}
	}

	return boardTitle, editWindow, v.Errors()
}

// SanitizeInput performs basic input sanitization
func SanitizeInput(input string) string {
	// Normalize line endings: CRLF -> LF, standalone CR -> LF
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	// Trim whitespace
	input = strings.TrimSpace(input)

	// Normalize horizontal whitespace (spaces/tabs) but preserve newlines
	hSpaceRegex := regexp.MustCompile(`[^\S\n]+`)
	input = hSpaceRegex.ReplaceAllString(input, " ")

	// Normalize multiple newlines to max of 2 (allow paragraph breaks)
	newlineRegex := regexp.MustCompile(`\n{3,}`)
	input = newlineRegex.ReplaceAllString(input, "\n\n")

	// Remove null bytes
	input = strings.ReplaceAll(input, "\x00", "")

	return input
}
