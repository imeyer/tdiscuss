package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Metric labels must come from the route a request matched, never the raw
// path or method, so made-up URLs and methods cannot create new metric series.
func TestRouteAndMethodLabels(t *testing.T) {
	var gotRoute, gotMethod string
	record := func(w http.ResponseWriter, r *http.Request) {
		gotRoute, gotMethod = routeLabel(r), methodLabel(r.Method)
		w.WriteHeader(http.StatusOK)
	}
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /{$}", "GET /thread/{tid}", "POST /thread/{tid}/{pid}/edit", "/"} {
		mux.HandleFunc(pattern, record)
	}

	tests := []struct {
		method, path          string
		wantRoute, wantMethod string
	}{
		{http.MethodGet, "/", "/", http.MethodGet},
		{http.MethodGet, "/thread/42", "/thread/{tid}", http.MethodGet},
		{http.MethodHead, "/thread/42", "/thread/{tid}", http.MethodHead},
		{http.MethodPost, "/thread/42/7/edit", "/thread/{tid}/{pid}/edit", http.MethodPost},
		{http.MethodGet, "/wp-login.php", "unmatched", http.MethodGet},
		{http.MethodGet, "/thread/42/random/junk", "unmatched", http.MethodGet},
		{"FOO", "/thread/42", "unmatched", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			gotRoute, gotMethod = "", ""
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))
			assert.Equal(t, tt.wantRoute, gotRoute)
			assert.Equal(t, tt.wantMethod, gotMethod)
		})
	}

	// A request no ServeMux routed has no pattern.
	assert.Equal(t, "unmatched", routeLabel(httptest.NewRequest(http.MethodGet, "/thread/42", nil)))
}
