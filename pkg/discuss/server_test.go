package discuss

import (
	"embed"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

//go:embed testdata/*.html
var templateDir embed.FS

func TestDiscussService_WhoAmI(t *testing.T) {
	var err error
	db, err := NewSQLiteDB(":memory:")
	assert.Nil(t, err)

	tests := []struct {
		name        string
		path        string
		currentUser string
		wantBody    []byte
		wantStatus  int
	}{
		{

			name:        "/whoami found",
			path:        "/whoami",
			currentUser: "test2@example.com",
			wantBody:    []byte("anonymouse user\n"),
			wantStatus:  http.StatusOK,
		},
		{
			name:        "/whoamii not found",
			path:        "/whoamii",
			currentUser: "",
			wantBody:    []byte("404 page not found\n"),
			wantStatus:  http.StatusNotFound,
		},
	}

	tmpls := template.Must(template.ParseFS(templateDir, "testdata/*.html"))

	derpMap := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")

	control := &testcontrol.Server{
		DERPMap: derpMap,
	}

	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	defer control.HTTPTestServer.Close()

	s := &tsnet.Server{
		Dir:        filepath.Join(t.TempDir(), "tsnet"),
		Hostname:   "test",
		Ephemeral:  false,
		ControlURL: control.HTTPTestServer.URL,
		Logf:       func(string, ...any) {},
	}

	err = s.Start()
	defer s.Close()

	assert.Nil(t, err)

	lc, err := s.LocalClient()

	assert.Nil(t, err)

	l := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))

	srv := NewService(lc, l, db.db, tmpls, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			srv.WhoAmI(w, r)

			assert.Equal(t, tt.wantStatus, w.Code, tt.name)

			b, err := io.ReadAll(w.Body)
			assert.Nil(t, err)
			assert.Equal(t, string(tt.wantBody), string(b), tt.name)
		})
	}
}