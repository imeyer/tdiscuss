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
	ID    int64
	Email string

	// IsAdmin is the effective admin status: the member's is_admin column
	// unioned with any admin role granted by the tailnet policy file.
	IsAdmin bool

	// IsAdminByGrant reports that the tailnet policy file granted admin, so
	// the UI can distinguish it from the database column.
	IsAdminByGrant bool

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

// createDebugServer builds the server for the debug listener, which carries
// the metrics endpoint. It is deliberately a separate listener on a separate
// port (see startListeners) so that access is enforced by tailnet ACLs rather
// than by anything this process decides about the peer.
func createDebugServer(mux http.Handler) *http.Server {
	return &http.Server{
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
		hostname:   hostname,
		version:    version,
		gitSha:     gitSha,
		telemetry:  telemetry,
	}
}

func NewTsNetServer(logger *slog.Logger) *tsnet.Server {
	logf := tsnetlog.Discard
	if *tsnetLog {
		logf = func(format string, args ...any) {
			logger.Info(fmt.Sprintf(format, args...), slog.String("source", "tsnet"))
		}
	}
	return &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tailscale"),
		Hostname: *hostname,
		Logf:     logf,
	}
}

// setupMux creates the HTTP handler using the new middleware system
func setupMux(dsvc *DiscussService) http.Handler {
	return SetupRoutes(dsvc, staticFiles)
}

func setupTsNetServer(logger *slog.Logger) (*tsnet.Server, error) {
	if err := createConfigDir(*dataDir); err != nil {
		return nil, fmt.Errorf("creating config directory: %w", err)
	}

	s := NewTsNetServer(logger)

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

	return s, nil
}

// debugPort carries the metrics endpoint. It is separate from the application
// ports so that tailnet ACLs decide who may scrape it, e.g.:
//
//	{"action": "accept", "src": ["tag:prom"], "dst": ["discuss:9090"]}
const debugPort = ":9090"

// startListeners opens the tailnet listeners. Every listener is a tsnet
// listener, so a connection's RemoteAddr is always a WireGuard-authenticated
// tailnet address.
func startListeners(s *tsnet.Server) (ln, tln, dln net.Listener, err error) {
	defer func() {
		if err == nil {
			return
		}
		// Don't leak the listeners we did manage to open.
		for _, l := range []net.Listener{ln, tln, dln} {
			if l != nil {
				l.Close()
			}
		}
		ln, tln, dln = nil, nil, nil
	}()

	if ln, err = s.Listen("tcp", ":80"); err != nil {
		return ln, tln, dln, fmt.Errorf("creating non-TLS listener: %w", err)
	}

	if tln, err = s.ListenTLS("tcp", ":443"); err != nil {
		return ln, tln, dln, fmt.Errorf("creating TLS listener: %w", err)
	}

	if dln, err = s.Listen("tcp", debugPort); err != nil {
		return ln, tln, dln, fmt.Errorf("creating debug listener: %w", err)
	}

	return ln, tln, dln, nil
}

func startServer(server *http.Server, ln net.Listener, logger *slog.Logger, scheme, hostname string) {
	logger.Info(fmt.Sprintf("listening on %s://%s", scheme, hostname))
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error(fmt.Sprintf("%s server failed", scheme), slog.String("error", err.Error()))
	}
}

// namedServer pairs a server with the label used in shutdown logging.
type namedServer struct {
	name string
	srv  *http.Server
}

// waitForShutdown blocks until a shutdown signal arrives, drains the servers,
// and returns the exit code the process should use.
//
// It deliberately does not call os.Exit: main still has deferred cleanup to
// run - closing tsnet (which flushes logtail and the state store), the
// database pool, and the telemetry exporters - and exiting here would skip
// all of it.
func waitForShutdown(sigChan chan os.Signal, logger *slog.Logger, servers ...namedServer) int {
	sig := <-sigChan
	logger.Info("received shutdown signal, initiating graceful shutdown",
		slog.String("signal", sig.String()))

	exitCode := 0
	if sigNum, ok := sig.(syscall.Signal); ok {
		exitCode = 128 + int(sigNum)
	}

	// Set up graceful shutdown with generous timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	serversDone := make(chan struct{}, len(servers))
	for _, ns := range servers {
		go func(ns namedServer) {
			defer func() { serversDone <- struct{}{} }()
			logger.Info("shutting down server", slog.String("server", ns.name))
			if err := ns.srv.Shutdown(shutdownCtx); err != nil {
				logger.Error("failed to gracefully shutdown server",
					slog.String("server", ns.name),
					slog.String("error", err.Error()))
				return
			}
			logger.Info("server shutdown complete", slog.String("server", ns.name))
		}(ns)
	}

	for done := 0; done < len(servers); {
		select {
		case <-serversDone:
			done++
		case <-shutdownCtx.Done():
			logger.Warn("shutdown timeout reached, abandoning remaining servers")
			return exitCode
		case sig := <-sigChan:
			// Handle repeated signals
			logger.Warn("received additional signal during shutdown",
				slog.String("signal", sig.String()))
			if sig == syscall.SIGTERM || sig == syscall.SIGQUIT {
				logger.Error("abandoning graceful shutdown due to repeated signal")
				return 130 // 128 + SIGINT
			}
		}
	}

	logger.Info("all servers shutdown successfully")
	logger.Debug("exiting", slog.Int("exit_code", exitCode))

	return exitCode
}
