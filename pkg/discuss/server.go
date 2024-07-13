package discuss

import (
	"html/template"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"tailscale.com/client/tailscale"
)

type DiscussService struct {
	tailClient *tailscale.LocalClient
	logger     *slog.Logger
	dbconn     *pgxpool.Pool
	queries    *Queries
	tmpls      *template.Template
	httpsURL   string
}

func NewService(tailClient *tailscale.LocalClient,
	logger *slog.Logger,
	dbconn *pgxpool.Pool,
	queries *Queries,
	tmpls *template.Template,
	httpsURL string,
) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		dbconn:     dbconn,
		queries:    queries,
		logger:     logger,
		tmpls:      tmpls,
		httpsURL:   httpsURL,
	}
}
