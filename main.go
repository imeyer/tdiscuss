package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"tailscale.com/hostinfo"
)

const BOARD_TITLE string = "tdiscuss - A Discussion Board for your Tailnet"

//go:embed tmpl/*.html
var templateFiles embed.FS

//go:embed static/*
var staticFiles embed.FS

var (
	hostname            = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss-dev"), "Hostname to use on your tailnet")
	dataDir             = flag.String("data-location", dataLocation(), "Configuration data location.")
	debug               = flag.Bool("debug", false, "Enable debug logging")
	tsnetLog            = flag.Bool("tsnet-log", false, "Enable tsnet logging (warning: VERY verbose)")
	otlpMode            = flag.Bool("otlp", false, "Enable OTLP metrics output, IYKYK")
	showVersion         = flag.Bool("version", false, "Print version and exit")
	version  string     = "dev"
	gitSha   string     = "no-commit"
	logLevel slog.Level = slog.LevelInfo
)

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("tdiscuss %s (%s)\n", version, gitSha)
		os.Exit(0)
	}

	hostinfo.SetApp("tdiscuss")

	if *debug {
		logLevel = slog.LevelDebug
	}

	logger := newLogger(os.Stdout, &logLevel)
	logger.Info("starting tdiscuss", slog.String("version", version), slog.String("git_sha", gitSha))

	config, err := LoadConfig()
	if err != nil {
		logger.Error("error loading config", slog.String("error", err.Error()))
	}
	config.Logger = logger
	config.OTLP = *otlpMode
	config.ServiceVersion = version
	config.ServiceGitSha = gitSha

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	telemetry, cleanup, err := setupTelemetry(ctx, config)
	if err != nil {
		logger.ErrorContext(ctx, "failed to setup telemetry: %w", slog.String("error", err.Error()))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanup(cleanupCtx); err != nil {
			logger.ErrorContext(ctx, "failed to cleanup telemetry: %w", slog.String("error", err.Error()))
		}
	}()

	// Record version information as metric attributes
	telemetry.Metrics.VersionGauge.Record(ctx, 1,
		metric.WithAttributes(
			attribute.String("version", version),
			attribute.String("git_sha", gitSha),
		),
	)

	sigChan := make(chan os.Signal, 1)
	// Handle common shutdown signals
	signal.Notify(sigChan,
		syscall.SIGINT,  // Ctrl+C
		syscall.SIGTERM, // Termination request
		syscall.SIGQUIT, // Quit from keyboard
		syscall.SIGHUP,  // Hang up detected on controlling terminal
	)

	// CSRF is now handled by middleware, no need for separate logging

	dbconn, err := setupDatabase(ctx, logger)
	if err != nil {
		logger.Error("failed to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer dbconn.Close()

	s := setupTsNetServer(logger)
	defer s.Close()

	tmpls := setupTemplates()

	lc := getTailscaleLocalClient(s, logger)

	if err := checkTailscaleReady(ctx, lc, logger); err != nil {
		logger.Error("tailscale not ready", slog.String("error", err.Error()))
		os.Exit(1)
	}

	queries := New(dbconn)
	wrappedQueries := &QueriesWrapper{Queries: queries}

	tracedQueries := NewTracedQueriesWrapper(wrappedQueries, telemetry)

	dsvc := NewDiscussService(
		lc,
		logger,
		dbconn,
		tracedQueries,
		tmpls,
		*hostname,
		version,
		gitSha,
		telemetry,
	)

	mux := setupMux(dsvc)

	serverPlain := createHTTPServer(mux)
	serverTls := createHTTPSServer(mux)

	ln, tln := startListeners(s, logger)
	defer ln.Close()
	defer tln.Close()

	go startServer(serverPlain, ln, logger, "http", *hostname)
	go startServer(serverTls, tln, logger, "https", expandSNIName(ctx, lc, logger))

	waitForShutdown(sigChan, ctx, logger, serverPlain, serverTls)
}
