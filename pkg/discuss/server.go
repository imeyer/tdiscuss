package discuss

import (
	"database/sql"
	"html/template"
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

	s.logger.DebugContext(r.Context(), "TEMPLATE/index.html")
	if err := s.tmpls.ExecuteTemplate(w, "index.html", map[string]any{}); err != nil {
		return
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
