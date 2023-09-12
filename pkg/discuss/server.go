package discuss

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"

	"tailscale.com/client/tailscale"
)

type DiscussService struct {
	tailClient *tailscale.LocalClient
	logger     *slog.Logger
	db         *sql.DB
	tmpls      *template.Template
	httpsURL   string
}

func (s *DiscussService) DiscussionIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	topics := make([]Topic, 0, 10)

	q := `
SELECT * FROM Topics t
ORDER BY t.CreatedAt DESC
LIMIT 10`

	rows, err := s.db.QueryContext(r.Context(), q)
	if err != nil {
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	defer rows.Close()

	for rows.Next() {
		topic := Topic{}

		err := rows.Scan(&topic.ID, &topic.Topic, &topic.User, &topic.Body, &topic.CreatedAt)
		if err != nil {
			s.RenderError(w, r, err, http.StatusInternalServerError)
		}

		topics = append(topics, topic)
	}
	s.logger.Log(r.Context(), slog.LevelDebug, "index fetch", "route", r.URL.Path, "rows", len(topics))
	s.logger.DebugContext(r.Context(), "TEMPLATE/index.html")
	if err := s.tmpls.ExecuteTemplate(w, "index.html", map[string]any{}); err != nil {
		return
	}
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

func (s *DiscussService) WhoAmI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/whoami" {
		http.NotFound(w, r)
		return
	}

	user, err := s.getUser(s.tailClient, r)
	if err != nil {
		return
	}

	s.logger.DebugContext(r.Context(), "TEMPLATE/whoami.html")
	err = s.tmpls.ExecuteTemplate(w, "whoami.html", struct {
		User string
	}{
		User: user,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
	}
}

func (s *DiscussService) getUser(lc *tailscale.LocalClient, r *http.Request) (string, error) {
	whois, err := lc.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		return "anonymouse user", nil
	}

	return whois.UserProfile.LoginName, nil
}

func NewService(tailClient *tailscale.LocalClient, logger *slog.Logger, db *sql.DB, tmpls *template.Template, httpsURL string) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		db:         db,
		logger:     logger,
		tmpls:      tmpls,
		httpsURL:   httpsURL,
	}
}
