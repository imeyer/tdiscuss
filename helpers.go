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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"tailscale.com/client/tailscale"
	"tailscale.com/tsnet"
)

func createConfigDir(dir string) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Join(dir, "tsnet"), 0o700)
	if err != nil {
		return err
	}

	return nil
}

func newLogger(logLevel *slog.Level) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))
	slog.SetDefault(logger)

	return logger
}

func dataLocation() string {
	if dir, ok := os.LookupEnv("DATA_DIR"); ok {
		return dir
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return os.Getenv("DATA_DIR")
	}
	return filepath.Join(dir, "tailscale", "discuss")
}

func envOr(key, defaultVal string) string {
	if result, ok := os.LookupEnv(key); ok {
		return result
	}
	return defaultVal
}

func formatTimestamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

func setupLogger() *slog.Logger {
	if *debug {
		logLevel = slog.LevelDebug
	}
	return newLogger(&logLevel)
}

func setupDatabase(ctx context.Context, logger *slog.Logger) *pgxpool.Pool {
	dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dbCancel()

	dbconn, err := pgxpool.NewWithConfig(dbCtx, PoolConfig(os.Getenv("DATABASE_URL"), logger))
	if err != nil {
		logger.Error("unable to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	return dbconn
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

func setupTemplates() *template.Template {
	return template.Must(template.New("any").Funcs(template.FuncMap{
		"formatTimestamp": formatTimestamp,
	}).ParseFS(templateFiles, "tmpl/*html"))
}

func setupMux(dsvc *DiscussService) http.Handler {
	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("GET /", dsvc.ListThreads)
	tailnetMux.HandleFunc("GET /thread/new", dsvc.ThreadNew)
	tailnetMux.HandleFunc("POST /thread/new", dsvc.CreateThread)
	tailnetMux.HandleFunc("GET /thread/{id}", dsvc.ListThreadPosts)
	tailnetMux.HandleFunc("POST /thread/{id}", dsvc.CreateThreadPost)
	tailnetMux.Handle("GET /metrics", promhttp.Handler())
	tailnetMux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	return HistogramHttpHandler(tailnetMux)
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

func getTailscaleLocalClient(s *tsnet.Server, logger *slog.Logger) *tailscale.LocalClient {
	lc, err := s.LocalClient()
	if err != nil {
		logger.Error("error creating s.LocalClient()")
		return nil
	}

	return lc
}

func expandSNIName(ctx context.Context, lc *tailscale.LocalClient, logger *slog.Logger) string {
	sni, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		logger.Error("error expanding SNI name")
		return ""
	}
	return sni
}
