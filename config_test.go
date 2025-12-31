package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolConfig(t *testing.T) {
	dsn := "postgres://user:password@localhost:5432/testdb"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config, err := PoolConfig(&dsn, logger)

	t.Run("Basic Configuration", func(t *testing.T) {
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, int32(4), config.MaxConns)
		assert.Equal(t, int32(0), config.MinConns)
		assert.Equal(t, time.Hour, config.MaxConnLifetime)
		assert.Equal(t, 15*time.Minute, config.MaxConnIdleTime)
		assert.Equal(t, time.Minute, config.HealthCheckPeriod)
		assert.Equal(t, 5*time.Second, config.ConnConfig.ConnectTimeout)
	})

	t.Run("BeforeConnect Callback", func(t *testing.T) {
		require.NotNil(t, config.BeforeConnect)
		err := config.BeforeConnect(context.Background(), &pgx.ConnConfig{})
		assert.NoError(t, err)
	})

	t.Run("AfterConnect Callback", func(t *testing.T) {
		require.NotNil(t, config.AfterConnect)
		err := config.AfterConnect(context.Background(), &pgx.Conn{})
		assert.NoError(t, err)
	})

	t.Run("BeforeClose Callback", func(t *testing.T) {
		require.NotNil(t, config.BeforeClose)
		config.BeforeClose(&pgx.Conn{})
	})
}

func TestPoolConfigWithInvalidDSN(t *testing.T) {
	invalidDSN := "invalid-dsn"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config, err := PoolConfig(&invalidDSN, logger)

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to parse database configuration")
}

func TestPoolConfigWithNilLogger(t *testing.T) {
	dsn := "postgres://user:password@localhost:5432/testdb"

	config, err := PoolConfig(&dsn, nil)

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, int32(4), config.MaxConns)
	assert.Equal(t, int32(0), config.MinConns)
	assert.Equal(t, time.Hour, config.MaxConnLifetime)
	assert.Equal(t, 15*time.Minute, config.MaxConnIdleTime)
	assert.Equal(t, time.Minute, config.HealthCheckPeriod)
	assert.Equal(t, 5*time.Second, config.ConnConfig.ConnectTimeout)
}
