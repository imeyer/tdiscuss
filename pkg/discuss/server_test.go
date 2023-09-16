package discuss

import (
	"context"
	"embed"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

//go:embed testdata/*.html
var templateDir embed.FS

func TestDiscussService_DiscussionIndex(t *testing.T) {
	var err error
	db, err := NewSQLiteDB(":memory:")
	assert.Nil(t, err)

	topics := []struct {
		topic *Topic
	}{
		{
			topic: &Topic{
				User:      "test@example.com",
				Topic:     "first topic",
				Body:      "wow",
				CreatedAt: time.Now(),
			},
		},
		{
			topic: &Topic{
				User:      "test2@example.com",
				Topic:     "second topic",
				Body:      "wow wow",
				CreatedAt: time.Now(),
			},
		},
		{
			topic: &Topic{
				User:      "test@example.com",
				Topic:     "third topic",
				Body:      "wowww",
				CreatedAt: time.Now(),
			},
		},
	}

	// seed some topics
	for _, topic := range topics {
		topicID, err := db.SaveTopic(context.Background(), topic.topic)
		if err != nil {
			t.Fatalf("Can't save topics: %v", err)
		}
		t.Logf("topic created id:%v", topicID)
	}

	tests := []struct {
		name        string
		path        string
		method      string
		currentUser string
		wantBody    []byte
		wantStatus  int
	}{
		{
			name:        "DiscussionIndex renders",
			path:        "/",
			method:      "GET",
			currentUser: "test2@example.com",
			wantBody:    []byte("<p>first topic</p><p>second topic</p><p>third topic</p>\n"),
			wantStatus:  http.StatusOK,
		},
		{
			name:        "DiscussionIndex does not allow POST",
			path:        "/",
			method:      "POST",
			currentUser: "test2@example.com",
			wantBody:    []byte("HTTP method not allowed\n"),
			wantStatus:  http.StatusMethodNotAllowed,
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

	srv := NewService(lc, l, db, tmpls, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Request to %v via %v", tt.path, tt.method)
			r := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			srv.DiscussionIndex(w, r)

			assert.Equal(t, tt.wantStatus, w.Code, tt.name)

			b, err := io.ReadAll(w.Body)
			assert.Nil(t, err)
			assert.Equal(t, string(tt.wantBody), string(b), tt.name)
		})
	}
}
