package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	LogDebug          bool
	Logger            *slog.Logger
	ServiceName       string
	ServiceVersion    string
	TraceMaxBatchSize int
	TraceSampleRate   float64
	OTLP              bool
}

func LoadConfig() (*Config, error) {
	config := &Config{
		LogDebug:          false,
		ServiceName:       "tdiscuss",
		TraceMaxBatchSize: 512,
		TraceSampleRate:   1.0, // Sample 25%
		OTLP:              false,
	}

	return config, nil
}

// PoolConfig function with error handling
func PoolConfig(dsn *string, logger *slog.Logger) (*pgxpool.Config, error) {
	const defaultMaxConns = int32(4)
	const defaultMinConns = int32(0)
	const defaultMaxConnLifetime = time.Hour
	const defaultMaxConnIdleTime = time.Minute * 15
	const defaultHealthCheckPeriod = time.Minute
	const defaultConnectTimeout = time.Second * 5

	dbConfig, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database configuration: %w", err)
	}

	dbConfig.MaxConns = defaultMaxConns
	dbConfig.MinConns = defaultMinConns
	dbConfig.MaxConnLifetime = defaultMaxConnLifetime
	dbConfig.MaxConnIdleTime = defaultMaxConnIdleTime
	dbConfig.HealthCheckPeriod = defaultHealthCheckPeriod
	dbConfig.ConnConfig.ConnectTimeout = defaultConnectTimeout

	dbConfig.BeforeConnect = func(ctx context.Context, c *pgx.ConnConfig) error {
		logger.Debug("creating connection")
		return nil
	}

	dbConfig.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		logger.Debug("connection created")
		return nil
	}

	dbConfig.BeforeAcquire = func(ctx context.Context, c *pgx.Conn) bool {
		logger.Debug("acquiring connection pool")
		return true
	}

	dbConfig.AfterRelease = func(c *pgx.Conn) bool {
		logger.Debug("releasing connection pool")
		return true
	}

	dbConfig.BeforeClose = func(c *pgx.Conn) {
		logger.Debug("closing connection pool")
	}

	return dbConfig, nil
}
