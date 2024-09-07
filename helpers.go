package main

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

func expandSNIName(ctx context.Context, lc *tailscale.LocalClient, logger *slog.Logger) string {
	sni, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		logger.Error("error expanding SNI name")
		return ""
	}
	return sni
}

func formatTimestamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

func getTailscaleLocalClient(s *tsnet.Server, logger *slog.Logger) *tailscale.LocalClient {
	lc, err := s.LocalClient()
	if err != nil {
		logger.Error("error creating s.LocalClient()")
		return nil
	}

	return lc
}

func newLogger(logLevel *slog.Level) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))
	slog.SetDefault(logger)

	return logger
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

func setupLogger() *slog.Logger {
	if *debug {
		logLevel = slog.LevelDebug
	}
	return newLogger(&logLevel)
}

func setupMux(dsvc *DiscussService) http.Handler {
	fs, err := fs.Sub(cssFile, "static")
	if err != nil {
		dsvc.logger.Error("error creating fs for static assets", slog.String("error", err.Error()))
		return nil
	}

	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("GET /", dsvc.ListThreads)
	tailnetMux.HandleFunc("GET /thread/new", dsvc.ThreadNew)
	tailnetMux.HandleFunc("POST /thread/new", dsvc.CreateThread)
	tailnetMux.HandleFunc("GET /thread/{id}", dsvc.ListThreadPosts)
	tailnetMux.HandleFunc("POST /thread/{id}", dsvc.CreateThreadPost)
	tailnetMux.Handle("GET /metrics", promhttp.Handler())
	tailnetMux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(fs))))

	return HistogramHttpHandler(tailnetMux)
}

func setupTemplates() *template.Template {
	return template.Must(template.New("any").Funcs(template.FuncMap{
		"formatTimestamp": formatTimestamp,
	}).ParseFS(templateFiles, "tmpl/*html"))
}
