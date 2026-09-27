package main

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// brokenPage writes some output and then fails, the way a template referencing
// a missing field does. Writing straight to the ResponseWriter would have sent
// a 200 and that partial output before the error surfaced.
const brokenPage = `<p>rendered before the failure</p>{{ .Row.Missing }}`

func newRenderTestService(tmpls *template.Template) *DiscussService {
	return &DiscussService{
		logger: slog.New(slog.DiscardHandler),
		tmpls:  tmpls,
	}
}

func TestRenderTemplateFailureSendsErrorPage(t *testing.T) {
	tmpls := setupTemplates()
	template.Must(tmpls.New("broken.html").Parse(brokenPage))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	newRenderTestService(tmpls).renderTemplate(rec, req, "broken.html", map[string]interface{}{
		"Row": struct{}{},
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, `<h1 class="page-title">Internal Server Error</h1>`)
	assert.Contains(t, body, errorMessage(http.StatusInternalServerError))
	assert.Contains(t, body, "Back to board")
	assert.NotContains(t, body, "rendered before the failure")
}

// If error.html itself fails, the response must still be a clean page: the
// status and message as plain text, with none of either template's output.
func TestRenderErrorFallsBackToPlainText(t *testing.T) {
	tmpls := template.Must(template.New("broken.html").Parse(brokenPage))
	// A field lookup on the string Heading fails. A missing map key would not:
	// templates render it as empty.
	template.Must(tmpls.New("error.html").Parse(`<p>error page before the failure</p>{{ .Heading.Missing }}`))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	newRenderTestService(tmpls).renderTemplate(rec, req, "broken.html", map[string]interface{}{
		"Row": struct{}{},
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t,
		"500 Internal Server Error\n\nSomething went wrong on our end. Try again in a moment.\n",
		rec.Body.String())
}

// Errors can happen before the auth and board-data middleware have run, so the
// error page must render without a user or board title in the context.
func TestRenderErrorWithoutUserStillRendersPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	newRenderTestService(setupTemplates()).renderError(rec, req, http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "<title>tdiscuss</title>")
	assert.Contains(t, body, `<h1 class="page-title">Not Found</h1>`)
	assert.Contains(t, body, template.HTMLEscapeString(errorMessage(http.StatusNotFound)))
	assert.NotContains(t, body, `href="/admin"`)
}
