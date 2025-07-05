package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SecurityConfig holds security middleware configuration
type SecurityConfig struct {
	// CSP configuration
	CSPDirectives map[string]string
	GenerateNonce bool

	// HSTS configuration
	HSTSMaxAge            int
	HSTSIncludeSubDomains bool
	HSTSPreload           bool

	// CSRF configuration
	CSRFCookieName string
	CSRFHeaderName string
	CSRFFieldName  string
	CSRFSameSite   http.SameSite

	// Feature policy
	PermissionsPolicy map[string][]string

	// Security headers
	FrameOptions       string
	ContentTypeOptions string
	ReferrerPolicy     string

	// Custom headers
	CustomHeaders map[string]string
}

// defaultSecurityConfig returns secure defaults
func defaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		CSPDirectives: map[string]string{
			"default-src":               "'self'",
			"script-src":                "'self'",
			"style-src":                 "'self' 'unsafe-inline'",
			"img-src":                   "'self' data: https:",
			"font-src":                  "'self'",
			"connect-src":               "'self'",
			"frame-ancestors":           "'none'",
			"base-uri":                  "'self'",
			"form-action":               "'self'",
			"upgrade-insecure-requests": "",
		},
		GenerateNonce:         false,
		HSTSMaxAge:            63072000, // 2 years
		HSTSIncludeSubDomains: true,
		HSTSPreload:           true,
		CSRFCookieName:        "csrf_token",
		CSRFHeaderName:        "X-CSRF-Token",
		CSRFFieldName:         "csrf_token",
		CSRFSameSite:          http.SameSiteLaxMode,
		FrameOptions:          "DENY",
		ContentTypeOptions:    "nosniff",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		PermissionsPolicy: map[string][]string{
			"geolocation": {},
			"microphone":  {},
			"camera":      {},
			"payment":     {},
			"usb":         {},
		},
		CustomHeaders: make(map[string]string),
	}
}

// securityHeadersMiddleware adds comprehensive security headers
func securityHeadersMiddleware(config *SecurityConfig) Middleware {
	if config == nil {
		config = defaultSecurityConfig()
	}

	// Pre-compute static values for performance
	permissionsPolicy := buildPermissionsPolicy(config.PermissionsPolicy)
	staticCSP := buildCSP(config.CSPDirectives)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Basic security headers
			w.Header().Set("X-Content-Type-Options", config.ContentTypeOptions)
			w.Header().Set("X-Frame-Options", config.FrameOptions)
			w.Header().Set("Referrer-Policy", config.ReferrerPolicy)

			// Modern alternative to X-XSS-Protection
			// We disable the XSS auditor as it can introduce vulnerabilities
			w.Header().Set("X-XSS-Protection", "0")

			// Permissions Policy
			if permissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", permissionsPolicy)
			}

			// HSTS - only on HTTPS
			if isHTTPS(r) {
				hsts := buildHSTS(config)
				w.Header().Set("Strict-Transport-Security", hsts)
			}

			// CSP with optional nonce
			if config.GenerateNonce {
				nonce := generateNonce()
				rc := getOrCreateRequestContext(r.Context())
				rc.CSPNonce = nonce

				csp := buildCSPWithNonce(config.CSPDirectives, nonce)
				w.Header().Set("Content-Security-Policy", csp)
			} else {
				w.Header().Set("Content-Security-Policy", staticCSP)
			}

			// Custom headers
			for k, v := range config.CustomHeaders {
				w.Header().Set(k, v)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// csrfProtectionMiddleware provides CSRF protection
func csrfProtectionMiddleware(config *SecurityConfig) Middleware {
	if config == nil {
		config = defaultSecurityConfig()
	}

	// Methods that require CSRF protection
	protectedMethods := map[string]bool{
		"POST":   true,
		"PUT":    true,
		"PATCH":  true,
		"DELETE": true,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip CSRF check for safe methods
			if !protectedMethods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}

			// Get token from cookie
			cookie, err := r.Cookie(config.CSRFCookieName)
			if err != nil {
				http.Error(w, "CSRF token missing", http.StatusForbidden)
				return
			}

			// Get token from request (header or form)
			requestToken := r.Header.Get(config.CSRFHeaderName)
			if requestToken == "" {
				requestToken = r.FormValue(config.CSRFFieldName)
			}

			// Compare tokens
			if !validateCSRFToken(cookie.Value, requestToken) {
				http.Error(w, "CSRF token invalid", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// csrfTokenMiddleware generates and sets CSRF tokens
func csrfTokenMiddleware(config *SecurityConfig) Middleware {
	if config == nil {
		config = defaultSecurityConfig()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if token already exists
			var token string
			if cookie, err := r.Cookie(config.CSRFCookieName); err == nil {
				token = cookie.Value
			} else {
				// Generate new token
				token = generateCSRFToken()

				// Set cookie
				http.SetCookie(w, &http.Cookie{
					Name:     config.CSRFCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					Secure:   isHTTPS(r),
					SameSite: config.CSRFSameSite,
					MaxAge:   86400, // 24 hours
				})
			}

			// Make token available in context
			rc := getOrCreateRequestContext(r.Context())
			rc.Set("csrf_token", token)

			next.ServeHTTP(w, r)
		})
	}
}

// requestSizeLimitMiddleware limits request body size
func requestSizeLimitMiddleware(maxSize int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit methods that typically have bodies
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
				// Early rejection based on Content-Length
				if r.ContentLength > maxSize {
					http.Error(w, fmt.Sprintf("Request body too large. Maximum size: %d bytes", maxSize),
						http.StatusRequestEntityTooLarge)
					return
				}

				// Wrap body reader
				r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Helper functions

func buildCSP(directives map[string]string) string {
	var parts []string
	for directive, value := range directives {
		if value == "" {
			parts = append(parts, directive)
		} else {
			parts = append(parts, fmt.Sprintf("%s %s", directive, value))
		}
	}
	return strings.Join(parts, "; ")
}

func buildCSPWithNonce(directives map[string]string, nonce string) string {
	// Clone directives
	newDirectives := make(map[string]string)
	for k, v := range directives {
		newDirectives[k] = v
	}

	// Add nonce to script-src
	if scriptSrc, ok := newDirectives["script-src"]; ok {
		newDirectives["script-src"] = fmt.Sprintf("%s 'nonce-%s'", scriptSrc, nonce)
	}

	// Add nonce to style-src and remove unsafe-inline
	if styleSrc, ok := newDirectives["style-src"]; ok {
		styleSrc = strings.Replace(styleSrc, "'unsafe-inline'", "", 1)
		newDirectives["style-src"] = fmt.Sprintf("%s 'nonce-%s'", strings.TrimSpace(styleSrc), nonce)
	}

	return buildCSP(newDirectives)
}

func buildHSTS(config *SecurityConfig) string {
	parts := []string{fmt.Sprintf("max-age=%d", config.HSTSMaxAge)}

	if config.HSTSIncludeSubDomains {
		parts = append(parts, "includeSubDomains")
	}

	if config.HSTSPreload {
		parts = append(parts, "preload")
	}

	return strings.Join(parts, "; ")
}

func buildPermissionsPolicy(policies map[string][]string) string {
	var parts []string

	for feature, allowList := range policies {
		if len(allowList) == 0 {
			parts = append(parts, fmt.Sprintf("%s=()", feature))
		} else {
			parts = append(parts, fmt.Sprintf("%s=(%s)", feature, strings.Join(allowList, " ")))
		}
	}

	return strings.Join(parts, ", ")
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		strings.EqualFold(r.URL.Scheme, "https")
}

func generateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based nonce
		return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return base64.URLEncoding.EncodeToString(b)
}

func generateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate CSRF token")
	}
	return base64.URLEncoding.EncodeToString(b)
}

func validateCSRFToken(cookieToken, requestToken string) bool {
	// Both must be present
	if cookieToken == "" || requestToken == "" {
		return false
	}

	// Decode tokens
	cookieBytes, err1 := base64.URLEncoding.DecodeString(cookieToken)
	requestBytes, err2 := base64.URLEncoding.DecodeString(requestToken)

	if err1 != nil || err2 != nil {
		return false
	}

	// Use constant-time comparison
	return subtle.ConstantTimeCompare(cookieBytes, requestBytes) == 1
}

// getCSPNonce retrieves the CSP nonce from request context
func getCSPNonce(ctx context.Context) string {
	rc, ok := getRequestContext(ctx)
	if !ok {
		return ""
	}
	return rc.CSPNonce
}

// getCSRFToken retrieves the CSRF token from request context
func getCSRFToken(ctx context.Context) string {
	rc, ok := getRequestContext(ctx)
	if !ok {
		return ""
	}

	if token, ok := rc.Get("csrf_token"); ok {
		if strToken, ok := token.(string); ok {
			return strToken
		}
	}
	return ""
}
