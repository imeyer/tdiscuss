package main

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	tsnetlog "tailscale.com/types/logger"
)

// Interfaces
type TailscaleClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	ExpandSNIName(ctx context.Context, name string) (fqdn string, ok bool)
	Status(ctx context.Context) (*ipnstate.Status, error)
	StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error)
}

type ExtendedQuerier interface {
	Querier
	WithTx(tx pgx.Tx) ExtendedQuerier
}

// Types
type User struct {
	ID        int64
	Email     string
	IsAdmin   bool
	IsBlocked bool
}

type QueriesWrapper struct {
	*Queries // embedded from pgx
}

func (qw *QueriesWrapper) WithTx(tx pgx.Tx) ExtendedQuerier {
	return &QueriesWrapper{
		Queries: qw.Queries.WithTx(tx),
	}
}

// Server functions
func checkTailscaleReady(ctx context.Context, lc TailscaleClient, logger *slog.Logger) error {
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %w", err)
		} else {
			switch st.BackendState {
			case "NoState":
				logger.DebugContext(ctx, "no state")
				time.Sleep(5 * time.Second)
				continue
			case "NeedsLogin":
				logger.InfoContext(ctx, "needs login to tailscale", slog.String("auth_url", st.AuthURL))
				time.Sleep(30 * time.Second)
				continue
			case "NeedsMachineAuth":
				logger.DebugContext(ctx, fmt.Sprintf("%v", st))
				continue
			case "Stopped":
				logger.InfoContext(ctx, "tsnet stopped")
				return nil
			case "Starting":
				logger.InfoContext(ctx, "starting tsnet")
				continue
			case "Running":
				nopeers, err := lc.StatusWithoutPeers(ctx)
				if err != nil {
					logger.ErrorContext(ctx, err.Error())
				}
				logger.InfoContext(ctx, "tsnet running", "certDomains", nopeers.CertDomains)
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
		IdleTimeout:  120 * time.Second,
	}
}

func createHTTPSServer(mux http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":443",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// DiscussService holds all the dependencies for the service
type DiscussService struct {
	tailClient TailscaleClient
	logger     *slog.Logger
	dbconn     *pgxpool.Pool
	queries    Querier
	tmpls      *template.Template
	devMode    bool
	hostname   string
	version    string
	gitSha     string
	telemetry  *TelemetryConfig
}

// NewDiscussService creates a new DiscussService instance
func NewDiscussService(
	tailClient TailscaleClient,
	logger *slog.Logger,
	dbconn *pgxpool.Pool,
	queries Querier,
	tmpls *template.Template,
	hostname string,
	version string,
	gitSha string,
	telemetry *TelemetryConfig,
) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		logger:     logger,
		dbconn:     dbconn,
		queries:    queries,
		tmpls:      tmpls,
		devMode:    false,
		hostname:   hostname,
		version:    version,
		gitSha:     gitSha,
		telemetry:  telemetry,
	}
}

func NewTsNetServer() *tsnet.Server {
	return &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tailscale"),
		Hostname: *hostname,
		Logf:     tsnetlog.Discard,
	}
}

// setupMux creates the HTTP handler using the new middleware system
func setupMux(dsvc *DiscussService) http.Handler {
	return SetupRoutes(dsvc, staticFiles)
}

func setupTsNetServer(logger *slog.Logger) *tsnet.Server {
	err := createConfigDir(*dataDir)
	if err != nil {
		logger.Error("error creating config directory", slog.String("error", err.Error()))
		os.Exit(1)
	}

	s := NewTsNetServer()

	// TODO: enable once we move to tsnet
	// // Set up HTTP health check
	// http.HandleFunc("/health", healthCheck)
	// s.ServeHTTP(":80", nil) // health check only

	// TODO: enable once we get https
	// ln443, err := s.Listen("tcp", ":443")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer ln443.Close()

	// tls_config := &tls.Config{
	// 	GetCertificate: lc.GetCertificate,
	// }

	// ln443 = tls.NewListener(ln443, tls_config)
	// go func() {
	// 	log.Fatal(http.Serve(ln443, mux))
	// }()

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
	logger.Info(fmt.Sprintf("listening on %s://%s", scheme, hostname))
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error(fmt.Sprintf("%s server failed", scheme), slog.String("error", err.Error()))
	}
}

func waitForShutdown(sigChan chan os.Signal, ctx context.Context, logger *slog.Logger, serverPlain, serverTls *http.Server) {
	sig := <-sigChan
	sigName := sig.String()
	logger.Info("received shutdown signal, initiating graceful shutdown",
		slog.String("signal", sigName))

	// Set up graceful shutdown with generous timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Track shutdown completion
	serversDone := make(chan struct{}, 2)

	// Shutdown HTTP server
	go func() {
		defer func() { serversDone <- struct{}{} }()
		logger.Info("shutting down HTTP server")
		if err := serverPlain.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to gracefully shutdown HTTP server",
				slog.String("error", err.Error()))
		} else {
			logger.Info("HTTP server shutdown complete")
		}
	}()

	// Shutdown HTTPS server
	go func() {
		defer func() { serversDone <- struct{}{} }()
		logger.Info("shutting down HTTPS server")
		if err := serverTls.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to gracefully shutdown HTTPS server",
				slog.String("error", err.Error()))
		} else {
			logger.Info("HTTPS server shutdown complete")
		}
	}()

	// Wait for both servers to shutdown or timeout
	serversShutdown := 0
	shutdownComplete := false

	for !shutdownComplete {
		select {
		case <-serversDone:
			serversShutdown++
			if serversShutdown >= 2 {
				shutdownComplete = true
				logger.Info("all servers shutdown successfully")
			}
		case <-shutdownCtx.Done():
			shutdownComplete = true
			logger.Warn("shutdown timeout reached, forcing exit")
		case sig := <-sigChan:
			// Handle repeated signals
			logger.Warn("received additional signal during shutdown",
				slog.String("signal", sig.String()))
			if sig == syscall.SIGTERM || sig == syscall.SIGQUIT {
				logger.Error("forcing immediate shutdown due to repeated signal")
				os.Exit(130) // 128 + SIGINT
			}
		}
	}

	logger.Info("graceful shutdown complete")

	// Exit with appropriate code
	if sigNum, ok := sig.(syscall.Signal); ok {
		exitCode := 128 + int(sigNum)
		logger.Debug("exiting with signal-based exit code", slog.Int("exit_code", exitCode))
		os.Exit(exitCode)
	}

	os.Exit(0)
}
