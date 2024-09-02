package main

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"tailscale.com/client/tailscale"
	"tailscale.com/tsnet"
)

type DiscussService struct {
	tailClient *tailscale.LocalClient
	logger     *slog.Logger
	dbconn     *pgxpool.Pool
	queries    *Queries
	tmpls      *template.Template
	httpsURL   string
	version    string
	gitSha     string
}

func NewDiscussService(tailClient *tailscale.LocalClient,
	logger *slog.Logger,
	dbconn *pgxpool.Pool,
	queries *Queries,
	tmpls *template.Template,
	httpsURL string,
	version string,
	gitSha string,
) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		dbconn:     dbconn,
		queries:    queries,
		logger:     logger,
		tmpls:      tmpls,
		httpsURL:   httpsURL,
		version:    version,
		gitSha:     gitSha,
	}
}

func NewTsNetServer(dataDir *string) *tsnet.Server {
	return &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tsnet"),
		Hostname: *hostname,
		Logf:     func(string, ...any) {},
	}
}

func checkTailscaleReady(ctx context.Context, lc *tailscale.LocalClient, logger *slog.Logger) error {
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %w", err)
		} else {
			switch st.BackendState {
			case "NoState":
				logger.DebugContext(ctx, fmt.Sprintf("%v", st), "state", st.BackendState)
				time.Sleep(2 * time.Second)
				continue
			case "NeedsLogin":
				logger.InfoContext(ctx, fmt.Sprintf("login to tailscale at %s", st.AuthURL), "state", st.BackendState)
				time.Sleep(15 * time.Second)
				continue
			case "NeedsMachineAuth":
				logger.InfoContext(ctx, fmt.Sprintf("%v", st), "state", st.BackendState)
				continue
			case "Stopped":
				logger.InfoContext(ctx, "tsnet stopped", "state", st.BackendState)
				return fmt.Errorf("%w", err)
			case "Starting":
				logger.InfoContext(ctx, "starting tsnet", "state", st.BackendState)
				continue
			case "Running":
				nopeers, err := lc.StatusWithoutPeers(ctx)
				if err != nil {
					logger.ErrorContext(ctx, err.Error())
				}
				logger.InfoContext(ctx, "tsnet running", "state", st.BackendState, "certDomains", nopeers.CertDomains)
				return nil
			}
		}
	}
}
