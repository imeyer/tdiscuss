package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"tailscale.com/client/tailscale"
	"tailscale.com/tsnet"
)

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

func createHTTPServer(mux http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":80",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
}

func createHTTPSServer(mux http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":443",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
}

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

func setupTsNetServer(logger *slog.Logger) *tsnet.Server {
	err := createConfigDir(*dataDir)
	if err != nil {
		logger.Info(fmt.Sprintf("creating configuration directory (%s) failed: %v", *dataDir, err), "data-dir", *dataDir)
	}

	s := NewTsNetServer(dataDir)

	if *tsnetLog {
		s.Logf = log.Printf
	}

	if err := s.Start(); err != nil {
		logger.Error("error starting tsnet server", slog.String("error", err.Error()))
		os.Exit(1)
	}

	lc, err := s.LocalClient()
	if err != nil {
		logger.Error("error creating s.LocalClient()", slog.String("error", err.Error()))
		os.Exit(1)
	}

	err = checkTailscaleReady(context.Background(), lc, logger)
	if err != nil {
		logger.Error("Tailscale not ready", slog.String("error", err.Error()))
		os.Exit(1)
	}

	return s
}

func startListeners(s *tsnet.Server, logger *slog.Logger) (net.Listener, net.Listener) {
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		logger.Error("error creating non-TLS listener", slog.String("error", err.Error()))
		os.Exit(1)
	}

	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		logger.Error("error creating TLS listener", slog.String("error", err.Error()))
		os.Exit(1)
	}

	return ln, tln
}

func startServer(server *http.Server, ln net.Listener, logger *slog.Logger, scheme, hostname string) {
	logger.Info(fmt.Sprintf("Listening on %s://%s", scheme, hostname))
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error(fmt.Sprintf("%s server failed", scheme), slog.String("error", err.Error()))
	}
}

func waitForShutdown(sigChan chan os.Signal, ctx context.Context, logger *slog.Logger, serverPlain, serverTls *http.Server) {
	sig := <-sigChan
	logger.Info("Shutting down gracefully", slog.String("signal", sig.String()))

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 10*time.Second)
	defer shutdownCancel()

	if err := serverPlain.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to gracefully shutdown HTTP server", slog.String("error", err.Error()))
	}

	if err := serverTls.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to gracefully shutdown HTTPS server", slog.String("error", err.Error()))
	}

	logger.Info("Servers stopped")

	if sigNum, ok := sig.(syscall.Signal); ok {
		s := 128 + int(sigNum)
		os.Exit(s)
	}
}
