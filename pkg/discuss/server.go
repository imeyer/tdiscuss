package discuss

import (
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"

	"tailscale.com/client/tailscale"
	"tailscale.com/tailcfg"
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

	s.logger.InfoContext(r.Context(), "TEMPLATE/index.html")
	if err := s.tmpls.ExecuteTemplate(w, "index.html", map[string]any{}); err != nil {
		return
	}
}

func (s *DiscussService) WhoAmI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/whoami" {
		http.NotFound(w, r)
		return
	}

	userInfo, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
	}

	s.logger.InfoContext(r.Context(), "TEMPLATE/whoami.html")
	err = s.tmpls.ExecuteTemplate(w, "whoami.html", struct {
		UserInfo *tailcfg.UserProfile
	}{
		UserInfo: userInfo.UserProfile,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
	}
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
