package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"tailscale.com/tsnet"
)

// createConfigDir creates the data directory. The tsnet state directory
// underneath it (see NewTsNetServer) is created by tsnet itself, with 0700.
func createConfigDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
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

func expandSNIName(ctx context.Context, lc TailscaleClient, logger *slog.Logger) string {
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

// getTailscaleLocalClient returns the LocalAPI client for the tsnet node.
//
// It returns an error rather than a nil client: every request path depends on
// this client for WhoIs, so a nil one only defers the failure to the first
// caller that dereferences it.
func getTailscaleLocalClient(s *tsnet.Server) (TailscaleClient, error) {
	lc, err := s.LocalClient()
	if err != nil {
		return nil, fmt.Errorf("creating tsnet LocalClient: %w", err)
	}

	return lc, nil
}

func newLogger(output io.Writer, logLevel *slog.Level) *slog.Logger {
	var addSource bool = false
	if *logLevel == slog.LevelDebug {
		addSource = true
	}

	logger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		AddSource: addSource,
		Level:     logLevel,
	}))
	slog.SetDefault(logger)

	return logger
}

var (
	PoolConfigFunc    = PoolConfig
	NewWithConfigFunc = pgxpool.NewWithConfig
)

func setupDatabase(ctx context.Context, logger *slog.Logger) (*pgxpool.Pool, error) {
	dbCtx, dbCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dbCancel()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL environment variable is not set")
	}

	// Use the overrideable PoolConfigFunc
	poolConfig, err := PoolConfigFunc(&dbURL, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool config: %w", err)
	}

	// Attempt to create the database connection
	var dbconn *pgxpool.Pool
	var connectErr error
	for attempts := 1; attempts <= 3; attempts++ {
		dbconn, connectErr = NewWithConfigFunc(dbCtx, poolConfig)
		if connectErr == nil {
			break
		}
		logger.Warn("failed to connect to database",
			slog.String("error", connectErr.Error()),
			slog.Int("attempt", attempts))
		time.Sleep(time.Duration(attempts) * time.Second)
	}

	if connectErr != nil {
		return nil, fmt.Errorf("unable to connect to database after 3 attempts: %w", connectErr)
	}

	// Test the connection
	if err := dbconn.Ping(dbCtx); err != nil {
		dbconn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info("successfully connected to the database")
	return dbconn, nil
}

func setupTemplates() *template.Template {
	return template.Must(template.New("any").Funcs(template.FuncMap{
		"formatTimestamp": formatTimestamp,
	}).ParseFS(templateFiles, "tmpl/*html"))
}
