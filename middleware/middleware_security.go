package middleware

import (
	"context"
	"crypto/rand"
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

	HSTSMaxAge            int
	HSTSIncludeSubDomains bool
	HSTSPreload           bool

	// CSRF protection (using Go 1.25 built-in)
	CSRFProtection     *http.CrossOriginProtection
	CSRFTrustedOrigins []string
	CSRFBypassPatterns []string

	PermissionsPolicy map[string][]string

	FrameOptions       string
	ContentTypeOptions string
	ReferrerPolicy     string

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
		HSTSPreload:        true,
		CSRFProtection:     http.NewCrossOriginProtection(),
		CSRFTrustedOrigins: []string{},
		CSRFBypassPatterns: []string{},
		FrameOptions:       "DENY",
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
			w.Header().Set("X-Content-Type-Options", config.ContentTypeOptions)
			w.Header().Set("X-Frame-Options", config.FrameOptions)
			w.Header().Set("Referrer-Policy", config.ReferrerPolicy)

			// Modern alternative to X-XSS-Protection
			// We disable the XSS auditor as it can introduce vulnerabilities
			w.Header().Set("X-XSS-Protection", "0")

			if permissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", permissionsPolicy)
			}

			if isHTTPS(r) {
				hsts := buildHSTS(config)
				w.Header().Set("Strict-Transport-Security", hsts)
			}

			if config.GenerateNonce {
				nonce := generateNonce()
				rc := getOrCreateRequestContext(r.Context())
				rc.CSPNonce = nonce

				csp := buildCSPWithNonce(config.CSPDirectives, nonce)
				w.Header().Set("Content-Security-Policy", csp)
			} else {
				w.Header().Set("Content-Security-Policy", staticCSP)
			}

			for k, v := range config.CustomHeaders {
				w.Header().Set(k, v)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// csrfProtectionMiddleware provides CSRF protection using Go 1.25's built-in CrossOriginProtection
func csrfProtectionMiddleware(config *SecurityConfig) Middleware {
	if config == nil {
		config = defaultSecurityConfig()
	}

	protection := config.CSRFProtection
	if protection == nil {
		protection = http.NewCrossOriginProtection()
	}

	for _, origin := range config.CSRFTrustedOrigins {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			// Invalid origins are ignored.
			continue
		}
	}

	for _, pattern := range config.CSRFBypassPatterns {
		protection.AddInsecureBypassPattern(pattern)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := protection.Check(r); err != nil {
				http.Error(w, "Cross-origin request rejected", http.StatusForbidden)
				return
			}

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
				if r.ContentLength > maxSize {
					http.Error(w, fmt.Sprintf("Request body too large. Maximum size: %d bytes", maxSize),
						http.StatusRequestEntityTooLarge)
					return
				}

				r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			}

			next.ServeHTTP(w, r)
		})
	}
}

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
	newDirectives := make(map[string]string)
	for k, v := range directives {
		newDirectives[k] = v
	}

	if scriptSrc, ok := newDirectives["script-src"]; ok {
		newDirectives["script-src"] = fmt.Sprintf("%s 'nonce-%s'", scriptSrc, nonce)
	}

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


// getCSPNonce retrieves the CSP nonce from request context
func getCSPNonce(ctx context.Context) string {
	rc, ok := getRequestContext(ctx)
	if !ok {
		return ""
	}
	return rc.CSPNonce
}

