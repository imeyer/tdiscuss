package discuss

import (
	"context"
	"embed"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestDiscussService(t *testing.T) {
	var err error

	l := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))

	db, err := NewSQLiteDB(":memory:", l)
	db.logger = l
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
		sendBody    string
		wantBody    []byte
		wantStatus  int
	}{
		{
			name:        "DiscussionIndex via GET returns 200 with Topics",
			path:        "/",
			method:      http.MethodGet,
			currentUser: "test2@example.com",
			wantBody:    []byte("<p>first topic</p><p>second topic</p><p>third topic</p>\n"),
			wantStatus:  http.StatusOK,
		},
		{
			name:        "DiscussionIndex does not allow POST method",
			path:        "/",
			method:      http.MethodPost,
			currentUser: "test2@example.com",
			wantBody:    []byte("Method Not Allowed"),
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:        "DiscussionNew does not allow POST method",
			path:        "/topic/new",
			method:      http.MethodPost,
			currentUser: "test2@example.com",
			wantBody:    []byte("Method Not Allowed"),
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:        "DiscussionNew via GET returns 200",
			path:        "/topic/new",
			method:      http.MethodGet,
			currentUser: "test2@example.com",
			wantBody:    []byte("anonymouse@user\n"),
			wantStatus:  http.StatusOK,
		},
		{
			name:        "DiscussionSave does not allow GET method",
			path:        "/topic/save",
			method:      http.MethodGet,
			currentUser: "test2@example.com",
			wantBody:    []byte(http.StatusText(http.StatusMethodNotAllowed)),
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:        "DiscussionSave saves and redirects to the new TopicID",
			path:        "/topic/save",
			method:      http.MethodPost,
			currentUser: "test2@example.com",
			sendBody:    "topic=Test%20topic1&topic_body=wow",
			wantStatus:  http.StatusSeeOther,
		},
		{
			name:        "DiscussionSave fails with no topic",
			path:        "/topic/save",
			method:      http.MethodPost,
			currentUser: "test2@example.com",
			sendBody:    "topic_body=Test%20body1",
			wantStatus:  http.StatusPreconditionFailed,
			wantBody:    []byte("topic is required\n"),
		},
		{
			name:        "DiscussionSave fails with no topic_body",
			path:        "/topic/save",
			method:      http.MethodPost,
			currentUser: "test2@example.com",
			sendBody:    "topic=Test%20topic1",
			wantStatus:  http.StatusPreconditionFailed,
			wantBody:    []byte("topic_body is required\n"),
		},
		{
			name:        "DiscussionTopic via GET returns 200",
			path:        "/topic/1",
			method:      http.MethodGet,
			currentUser: "test2@example.com",
			sendBody:    "topic=Test%20topic1",
			wantStatus:  http.StatusOK,
			wantBody:    []byte("test@example.com<br />\n"),
		},
		{
			name:        "DiscussionTopic via HEAD returns 200 with no Body",
			path:        "/topic/1",
			method:      http.MethodHead,
			currentUser: "test2@example.com",
			wantStatus:  http.StatusOK,
			wantBody:    []byte(""),
		},
		{
			name:        "DiscussionTopic via GET returns 404 for a topic that doesn't exist",
			path:        "/topic/311",
			method:      http.MethodGet,
			currentUser: "test2@example.com",
			wantStatus:  http.StatusNotFound,
			wantBody:    []byte("404 page not found\n"),
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

	srv := NewService(lc, l, db, tmpls, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Request to %v via %v", tt.path, tt.method)

			var r *http.Request

			switch tt.method {
			case http.MethodPost:
				r = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.sendBody))
				r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
			default:
				r = httptest.NewRequest(tt.method, tt.path, nil)
			}

			w := httptest.NewRecorder()

			switch tt.path {
			case "/":
				srv.DiscussionIndex(w, r)
			case "/topic/new":
				srv.DiscussionNew(w, r)
			case "/topic/save":
				srv.DiscussionSave(w, r)
			case "/topic/1":
				srv.DiscussionTopic(w, r)
			case "/topic/311":
				srv.DiscussionTopic(w, r)
			default:
				srv.DiscussionIndex(w, r)
			}

			assert.Equal(t, tt.wantStatus, w.Code, tt.name)

			b, err := io.ReadAll(w.Body)
			assert.Nil(t, err)
			assert.Equal(t, string(tt.wantBody), string(b), tt.name)
		})
	}
}
