package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

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

// mockMkdirAll is a variable of function type that matches os.MkdirAll
var mockMkdirAll func(path string, perm os.FileMode) error

// mockableCreateConfigDir is a version of createConfigDir that uses the mock function
func mockableCreateConfigDir(dir *string) error {
	err := mockMkdirAll(*dir, 0o700)
	if err != nil {
		return err
	}
	err = mockMkdirAll(filepath.Join(*dir, "tsnet"), 0o700)
	if err != nil {
		return err
	}
	return nil
}

func TestCreateConfigDir(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		setup   func()
		wantErr bool
	}{
		{
			name: "Create new directory",
			dir:  "testdata/config",
			setup: func() {
				mockMkdirAll = os.MkdirAll
			},
			wantErr: false,
		},
		{
			name: "Create existing directory",
			dir:  "testdata/existing",
			setup: func() {
				mockMkdirAll = os.MkdirAll
				os.MkdirAll("testdata/existing", 0o700) // Create the directory beforehand
			},
			wantErr: false,
		},
		{
			name: "Error creating main directory",
			dir:  "testdata/error-main",
			setup: func() {
				mockMkdirAll = func(path string, perm os.FileMode) error {
					return errors.New("mock error")
				}
			},
			wantErr: true,
		},
		{
			name: "Error creating tsnet subdirectory",
			dir:  "testdata/error-tsnet",
			setup: func() {
				mockMkdirAll = func(path string, perm os.FileMode) error {
					if filepath.Base(path) == "tsnet" {
						return errors.New("mock error creating tsnet")
					}
					return os.MkdirAll(path, perm)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup the test case
			tt.setup()

			// Clean up after each test
			defer os.RemoveAll(tt.dir)

			err := mockableCreateConfigDir(&tt.dir)

			if (err != nil) != tt.wantErr {
				t.Errorf("createConfigDir() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Check if the main directory exists
				if _, err := os.Stat(tt.dir); os.IsNotExist(err) {
					t.Errorf("Main directory was not created")
				}

				// Check if the tsnet subdirectory exists
				tsnetDir := filepath.Join(tt.dir, "tsnet")
				if _, err := os.Stat(tsnetDir); os.IsNotExist(err) {
					t.Errorf("tsnet subdirectory was not created")
				}

				// Check permissions
				info, err := os.Stat(tt.dir)
				if err != nil {
					t.Errorf("Failed to get directory info: %v", err)
				} else if info.Mode().Perm() != 0o700 {
					t.Errorf("Incorrect permissions: got %v, want %v", info.Mode().Perm(), 0o700)
				}
			}
		})
	}
}
