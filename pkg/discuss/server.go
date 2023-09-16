package discuss

import (
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tailscale.com/client/tailscale"
)

const formDataLimit = 32 * 1024

type DiscussService struct {
	tailClient *tailscale.LocalClient
	logger     *slog.Logger
	db         *SQLiteDB
	tmpls      *template.Template
	httpsURL   string
}

func (s *DiscussService) DiscussionIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != "GET" {
		s.RenderError(w, r, fmt.Errorf("HTTP method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	topics, err := s.db.LoadTopics(r.Context())
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.logger.InfoContext(r.Context(), "index fetch", "route", r.URL.Path, "rows", len(topics))
	s.logger.DebugContext(r.Context(), "TEMPLATE/index.html")
	if err := s.tmpls.ExecuteTemplate(w, "index.html", struct {
		Title  string
		Topics []*Topic
	}{
		Title:  "tdiscuss - A Discussion Board for your Tailnet",
		Topics: topics,
	}); err != nil {
		return
	}
}

func (s *DiscussService) DiscussionNew(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/topic/new" {
		s.RenderError(w, r, fmt.Errorf("404 page not found"), http.StatusNotFound)
		return
	}

	if r.Method != "GET" {
		s.RenderError(w, r, fmt.Errorf("HTTP method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	userInfo, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	err = s.tmpls.ExecuteTemplate(w, "newtopic.html", struct {
		User  string
		Title string
	}{
		User:  userInfo.UserProfile.LoginName,
		Title: "New topic!",
	})
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}
}

func (s *DiscussService) DiscussionTopic(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/topic/") {
		s.RenderError(w, r, fmt.Errorf("404 page not found"), http.StatusNotFound)
		return
	}

	if r.Method != "GET" {
		s.RenderError(w, r, fmt.Errorf("HTTP method not allowed"), http.StatusMethodNotAllowed)
		return
	}
}

func (s *DiscussService) DiscussionSave(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/topic/save" {
		s.RenderError(w, r, fmt.Errorf("404 page not found"), http.StatusNotFound)
		return
	}

	if r.Method != "POST" {
		s.RenderError(w, r, fmt.Errorf("HTTP method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	userInfo, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		err = r.ParseMultipartForm(formDataLimit)
	} else if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = r.ParseForm()
	} else {
		s.logger.DebugContext(r.Context(), "%s: unknown content type: %s", r.RemoteAddr, r.Header.Get("Content-Type"))
		http.Error(w, "bad content-type, should be a form", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.logger.DebugContext(r.Context(), "%s: bad form: %v", r.RemoteAddr, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("topic") && !r.Form.Has("topic_body") {
		s.logger.DebugContext(r.Context(), r.Form.Encode())
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: topic, and topic_body are required", r.RemoteAddr))
		http.Error(w, "include form data:values topic and topic_body", http.StatusBadRequest)
		return
	}

	topic := new(Topic)
	topic.Topic = r.Form.Get("topic")
	topic.Body = r.Form.Get("topic_body")
	topic.CreatedAt = time.Now()
	topic.User = userInfo.UserProfile.LoginName

	topicID, err := s.db.SaveTopic(r.Context(), topic)
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
	}

	s.logger.DebugContext(r.Context(), "topic created", "topic_id", topicID)
	http.Redirect(w, r, fmt.Sprintf("https://%s/topic/%d", s.httpsURL, topicID), http.StatusSeeOther)
}

func (s *DiscussService) RenderError(w http.ResponseWriter, r *http.Request, err error, code int) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)

	s.logger.InfoContext(r.Context(), fmt.Sprintf("%s: %v", r.RemoteAddr, err))

	if err := s.tmpls.ExecuteTemplate(w, "error.html", struct {
		Title, Error string
		UserInfo     any
	}{
		Title: "Oh noes!",
		Error: err.Error(),
	}); err != nil {
		log.Printf("%s: %v", r.RemoteAddr, err)
	}
}

func (s *DiscussService) getUser(lc *tailscale.LocalClient, r *http.Request) (string, error) {
	whois, err := lc.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		return "anonymouse user", nil
	}

	return whois.UserProfile.LoginName, nil
}

func NewService(tailClient *tailscale.LocalClient, logger *slog.Logger, db *SQLiteDB, tmpls *template.Template, httpsURL string) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		db:         db,
		logger:     logger,
		tmpls:      tmpls,
		httpsURL:   httpsURL,
	}
}
