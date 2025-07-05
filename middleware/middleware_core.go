package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Middleware represents a standard HTTP middleware
type Middleware func(http.Handler) http.Handler

// Chain combines multiple middlewares into a single middleware
type Chain struct {
	middlewares []Middleware
}

// newChain creates a new middleware chain
func newChain(middlewares ...Middleware) *Chain {
	return &Chain{middlewares: append([]Middleware{}, middlewares...)}
}

// Then chains the middlewares and returns the final handler
func (c *Chain) Then(h http.Handler) http.Handler {
	if h == nil {
		h = http.DefaultServeMux
	}

	for i := len(c.middlewares) - 1; i >= 0; i-- {
		h = c.middlewares[i](h)
	}
	return h
}

// ThenFunc chains the middlewares and returns the final handler function
func (c *Chain) ThenFunc(fn http.HandlerFunc) http.Handler {
	if fn == nil {
		return c.Then(nil)
	}
	return c.Then(fn)
}

// Append creates a new chain with additional middlewares
func (c *Chain) Append(middlewares ...Middleware) *Chain {
	newMiddlewares := make([]Middleware, 0, len(c.middlewares)+len(middlewares))
	newMiddlewares = append(newMiddlewares, c.middlewares...)
	newMiddlewares = append(newMiddlewares, middlewares...)
	return &Chain{middlewares: newMiddlewares}
}

// Extend creates a new chain by extending with another chain
func (c *Chain) Extend(chain *Chain) *Chain {
	return c.Append(chain.middlewares...)
}

// Context key type for type safety
type contextKey string

const (
	contextKeyUser      contextKey = "tdiscuss.user"
	contextKeyRequestID contextKey = "tdiscuss.request_id"
	contextKeyTraceID   contextKey = "tdiscuss.trace_id"
	contextKeyStartTime contextKey = "tdiscuss.start_time"
	contextKeyCSPNonce  contextKey = "tdiscuss.csp_nonce"
)

// RequestContext holds all request-scoped data
type RequestContext struct {
	User      *ContextUser
	RequestID string
	TraceID   string
	StartTime time.Time
	CSPNonce  string
	mu        sync.RWMutex
	values    map[string]interface{}
}

// ContextUser holds user information in context
type ContextUser struct {
	ID        int64
	Email     string
	IsAdmin   bool
	IsBlocked bool
}

// newRequestContext creates a new request context
func newRequestContext() *RequestContext {
	return &RequestContext{
		RequestID: uuid.New().String(),
		StartTime: time.Now(),
		values:    make(map[string]interface{}),
	}
}

// Set adds a custom value to the request context
func (rc *RequestContext) Set(key string, value interface{}) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.values[key] = value
}

// Get retrieves a custom value from the request context
func (rc *RequestContext) Get(key string) (interface{}, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	val, ok := rc.values[key]
	return val, ok
}

// Context management functions
func withRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, contextKeyRequestID, rc)
}

func getRequestContext(ctx context.Context) (*RequestContext, bool) {
	rc, ok := ctx.Value(contextKeyRequestID).(*RequestContext)
	return rc, ok
}

func getOrCreateRequestContext(ctx context.Context) *RequestContext {
	if rc, ok := getRequestContext(ctx); ok {
		return rc
	}
	return newRequestContext()
}

// Helper functions for common context operations
func getUser(ctx context.Context) (*ContextUser, bool) {
	rc, ok := getRequestContext(ctx)
	if !ok || rc.User == nil {
		return nil, false
	}
	return rc.User, true
}

func getRequestID(ctx context.Context) string {
	rc, ok := getRequestContext(ctx)
	if !ok {
		return ""
	}
	return rc.RequestID
}

func getTraceID(ctx context.Context) string {
	rc, ok := getRequestContext(ctx)
	if !ok {
		return ""
	}
	return rc.TraceID
}

// Conditional middleware helpers
func when(condition func(*http.Request) bool, middleware Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if condition(r) {
				middleware(next).ServeHTTP(w, r)
			} else {
				next.ServeHTTP(w, r)
			}
		})
	}
}

func unless(condition func(*http.Request) bool, middleware Middleware) Middleware {
	return when(func(r *http.Request) bool { return !condition(r) }, middleware)
}

// Common condition functions
func isAuthenticated(r *http.Request) bool {
	user, ok := getUser(r.Context())
	return ok && user != nil
}

func isAdmin(r *http.Request) bool {
	user, ok := getUser(r.Context())
	return ok && user != nil && user.IsAdmin
}

func hasMethod(methods ...string) func(*http.Request) bool {
	methodMap := make(map[string]bool)
	for _, m := range methods {
		methodMap[m] = true
	}
	return func(r *http.Request) bool {
		return methodMap[r.Method]
	}
}

func hasPathPrefix(prefix string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		path := r.URL.Path
		return len(path) >= len(prefix) && path[:len(prefix)] == prefix
	}
}

// middlewareResponseWriter wrapper for tracking response metadata
type middlewareResponseWriter struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
	mu          sync.Mutex
}

func newResponseWriter(w http.ResponseWriter) *middlewareResponseWriter {
	return &middlewareResponseWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
}

func (rw *middlewareResponseWriter) WriteHeader(status int) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if !rw.wroteHeader {
		rw.status = status
		rw.ResponseWriter.WriteHeader(status)
		rw.wroteHeader = true
	}
}

func (rw *middlewareResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.mu.Lock()
	rw.written += int64(n)
	rw.mu.Unlock()
	return n, err
}

func (rw *middlewareResponseWriter) Status() int {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.status
}

func (rw *middlewareResponseWriter) BytesWritten() int64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.written
}

// Flush implements http.Flusher
func (rw *middlewareResponseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestContextMiddleware initializes the request context
func requestContextMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := newRequestContext()
			ctx := withRequestContext(r.Context(), rc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
