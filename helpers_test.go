package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

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

func TestCreateConfigDirTwo(t *testing.T) {
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
				// No setup needed for this test case
			},
			wantErr: false,
		},
		{
			name: "Create existing directory",
			dir:  "testdata/existing",
			setup: func() {
				os.MkdirAll("testdata/existing", 0o700) // Create the directory beforehand
			},
			wantErr: false,
		},
		{
			name: "Error creating main directory",
			dir:  "testdata/error-main",
			setup: func() {
				// Simulate an error by creating a file with the same name
				os.WriteFile("testdata/error-main", []byte{}, 0o600)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup the test case
			tt.setup()

			defer os.RemoveAll(tt.dir)

			// Attempt to create the directory
			err := createConfigDir(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("os.MkdirAll() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Check permissions if no error is expected
			if !tt.wantErr {
				info, err := os.Stat(tt.dir)
				if err != nil {
					t.Errorf("Failed to get directory info: %v", err)
				} else if info.Mode().Perm() != 0o700 {
					t.Errorf("Incorrect permissions: got %v, want %v", info.Mode().Perm(), 0o700)
				}
			}

			// Clean up
			os.RemoveAll(tt.dir)
		})
	}
}

func TestExpandSNIName(t *testing.T) {
	tests := []struct {
		name          string
		hostname      string
		expandSNIName func(ctx context.Context, hostname string) (string, bool)
		expectedSNI   string
		expectedLog   string
	}{
		{
			name:     "Successful expansion",
			hostname: "example.com",
			expandSNIName: func(ctx context.Context, hostname string) (string, bool) {
				return "expanded.example.com", true
			},
			expectedSNI: "expanded.example.com",
			expectedLog: "",
		},
		{
			name:     "Failed expansion",
			hostname: "example.com",
			expandSNIName: func(ctx context.Context, hostname string) (string, bool) {
				return "", false
			},
			expectedSNI: "",
			expectedLog: "error expanding SNI name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockClient := &MockTailscaleClient{
				ExpandSNINameFunc: tt.expandSNIName,
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			// Temporarily set the hostname variable
			hostname = &tt.hostname

			// Capture log output
			var logOutput io.Writer
			if tt.expectedLog != "" {
				logOutput = &bytes.Buffer{}
				logger = slog.New(slog.NewTextHandler(logOutput, nil))
			}

			sni := expandSNIName(ctx, mockClient, logger)
			if sni != tt.expectedSNI {
				t.Errorf("Expected SNI '%s', got '%s'", tt.expectedSNI, sni)
			}

			if tt.expectedLog != "" {
				logStr := logOutput.(*bytes.Buffer).String()
				if !strings.Contains(logStr, tt.expectedLog) {
					t.Errorf("Expected log to contain '%s', got '%s'", tt.expectedLog, logStr)
				}
			}
		})
	}
}

func TestEnvOr(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		envValue   string
		defaultVal string
		expected   string
	}{
		{
			name:       "Environment variable set",
			envKey:     "TEST_ENV",
			envValue:   "value_from_env",
			defaultVal: "default_value",
			expected:   "value_from_env",
		},
		{
			name:       "Environment variable not set",
			envKey:     "TEST_ENV",
			envValue:   "",
			defaultVal: "default_value",
			expected:   "default_value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up the environment variable
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
			} else {
				os.Unsetenv(tt.envKey)
			}

			// Call the function
			result := envOr(tt.envKey, tt.defaultVal)

			// Check the result
			if result != tt.expected {
				t.Errorf("envOr() = %v, want %v", result, tt.expected)
			}

			// Clean up the environment variable
			os.Unsetenv(tt.envKey)
		})
	}
}

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name      string
		logLevel  slog.Level
		logFunc   func(logger *slog.Logger)
		expectMsg string
	}{
		{
			name:     "Debug level",
			logLevel: slog.LevelDebug,
			logFunc: func(logger *slog.Logger) {
				logger.Debug("debug message")
			},
			expectMsg: "debug message",
		},
		{
			name:     "Info level",
			logLevel: slog.LevelInfo,
			logFunc: func(logger *slog.Logger) {
				logger.Info("info message")
			},
			expectMsg: "info message",
		},
		{
			name:     "Warn level",
			logLevel: slog.LevelWarn,
			logFunc: func(logger *slog.Logger) {
				logger.Warn("warn message")
			},
			expectMsg: "warn message",
		},
		{
			name:     "Error level",
			logLevel: slog.LevelError,
			logFunc: func(logger *slog.Logger) {
				logger.Error("error message")
			},
			expectMsg: "error message",
		},
		{
			name:     "Debug log doesn't appear in Info level",
			logLevel: slog.LevelInfo,
			logFunc: func(logger *slog.Logger) {
				logger.Debug("debug message")
			},
			expectMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newLogger(&buf, &tt.logLevel)

			tt.logFunc(logger)

			if tt.expectMsg != "" && !strings.Contains(buf.String(), tt.expectMsg) {
				t.Errorf("Expected log output to contain '%s', got %v", tt.expectMsg, buf.String())
			} else if tt.expectMsg == "" && buf.Len() != 0 {
				t.Errorf("Expected no log output, got %v", buf.String())
			}
		})
	}
}

func TestSetupLogger(t *testing.T) {
	tests := []struct {
		name          string
		debug         bool
		expectedLevel slog.Level
		logFunc       func(logger *slog.Logger)
		expectMsg     string
	}{
		{
			name:          "Debug mode enabled",
			debug:         true,
			expectedLevel: slog.LevelDebug,
			logFunc: func(logger *slog.Logger) {
				logger.Debug("debug message")
			},
			expectMsg: "debug message",
		},
		{
			name:          "Debug mode disabled",
			debug:         false,
			expectedLevel: slog.LevelInfo, // Assuming default log level is Info
			logFunc: func(logger *slog.Logger) {
				logger.Info("info message")
			},
			expectMsg: "info message",
		},
		{
			name:          "Debug log doesn't appear in Info level",
			debug:         false,
			expectedLevel: slog.LevelInfo,
			logFunc: func(logger *slog.Logger) {
				logger.Debug("debug message")
			},
			expectMsg: "",
		},
		{
			name:          "Info log appears in Debug level",
			debug:         true,
			expectedLevel: slog.LevelDebug,
			logFunc: func(logger *slog.Logger) {
				logger.Info("info message")
			},
			expectMsg: "info message",
		},
		{
			name:          "Error log appears in Info level",
			debug:         false,
			expectedLevel: slog.LevelInfo,
			logFunc: func(logger *slog.Logger) {
				logger.Error("error message")
			},
			expectMsg: "error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Temporarily set the debug variable
			debug = &tt.debug

			// Capture log output
			var buf bytes.Buffer

			logger := newLogger(&buf, &tt.expectedLevel)

			// Perform the log function
			tt.logFunc(logger)

			// Check the log output
			if tt.expectMsg != "" && !strings.Contains(buf.String(), tt.expectMsg) {
				t.Errorf("Expected log output to contain '%s', got %v", tt.expectMsg, buf.String())
			} else if tt.expectMsg == "" && buf.Len() != 0 {
				t.Errorf("Expected no log output, got %v", buf.String())
			}
		})
	}
}
