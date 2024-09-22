package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MockPgxPool mocks pgxpool.Pool for testing
type MockPgxPool struct {
	PingFunc  func(ctx context.Context) error
	CloseFunc func()
}

func (m *MockPgxPool) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return nil
}

func (m *MockPgxPool) Close() {
	if m.CloseFunc != nil {
		m.CloseFunc()
	}
}

func TestSetupDatabase_NoDatabaseURL(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Unset DATABASE_URL
	os.Setenv("DATABASE_URL", "")

	dbconn, err := setupDatabase(ctx, logger)
	if err == nil {
		t.Fatal("Expected error when DATABASE_URL is not set, got nil")
	}

	expectedErr := "DATABASE_URL environment variable is not set"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error '%s', got '%v'", expectedErr, err)
	}

	if dbconn != nil {
		t.Fatal("Expected dbconn to be nil when error occurs")
	}
}

func TestSetupDatabase_PoolConfigError(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Set DATABASE_URL to some value
	os.Setenv("DATABASE_URL", "lol")

	// Override PoolConfigFunc to return an error
	originalPoolConfigFunc := PoolConfigFunc
	defer func() { PoolConfigFunc = originalPoolConfigFunc }()

	PoolConfigFunc = func(dbURL *string, logger *slog.Logger) (*pgxpool.Config, error) {
		return nil, errors.New("mock PoolConfig error")
	}

	dbconn, err := setupDatabase(ctx, logger)
	if err == nil {
		t.Fatal("Expected error when PoolConfig returns an error, got nil")
	}

	expectedErr := "failed to create pool config: mock PoolConfig error"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error '%s', got '%v'", expectedErr, err)
	}

	if dbconn != nil {
		t.Fatal("Expected dbconn to be nil when error occurs")
	}
}

func TestSetupDatabase_NewWithConfigFails(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Set DATABASE_URL to some value
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/dbname")

	// Override PoolConfigFunc to return a valid config
	originalPoolConfigFunc := PoolConfigFunc
	defer func() { PoolConfigFunc = originalPoolConfigFunc }()

	PoolConfigFunc = func(dbURL *string, logger *slog.Logger) (*pgxpool.Config, error) {
		return &pgxpool.Config{}, nil
	}

	// Override NewWithConfigFunc to return an error
	originalNewWithConfigFunc := NewWithConfigFunc
	defer func() { NewWithConfigFunc = originalNewWithConfigFunc }()

	attempts := 0
	NewWithConfigFunc = func(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
		attempts++
		return nil, errors.New("mock NewWithConfig error")
	}

	dbconn, err := setupDatabase(ctx, logger)
	if err == nil {
		t.Fatal("Expected error when NewWithConfig fails, got nil")
	}

	expectedErr := "unable to connect to database after 3 attempts: mock NewWithConfig error"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error '%s', got '%v'", expectedErr, err)
	}

	if dbconn != nil {
		t.Fatal("Expected dbconn to be nil when error occurs")
	}

	if attempts != 3 {
		t.Fatalf("Expected 3 attempts, got %d", attempts)
	}
}

// Note: Testing Ping failure and successful connections would require a more complex setup or
// integration tests involving a real database or advanced mocking techniques.
