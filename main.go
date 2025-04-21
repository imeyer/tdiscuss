package main

import (
	"context"
	"embed"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tailscale.com/hostinfo"
)

const BOARD_TITLE string = "tdiscuss - A Discussion Board for your Tailnet"

//go:embed tmpl/*.html
var templateFiles embed.FS

//go:embed static/style.css
var cssFile embed.FS

var (
	hostname            = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss"), "Hostname to use on your tailnet")
	dataDir             = flag.String("data-location", dataLocation(), "Configuration data location.")
	debug               = flag.Bool("debug", false, "Enable debug logging")
	tsnetLog            = flag.Bool("tsnet-log", false, "Enable tsnet logging")
	otlpMode            = flag.Bool("otlp", false, "Enable OTLP metrics output, IYKYK")
	version  string     = "dev"
	gitSha   string     = "no-commit"
	logLevel slog.Level = slog.LevelInfo
)

func main() {
	flag.Parse()

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

	config.ServiceVersion = version

	telemetry.Metrics.VersionGauge.Record(ctx, 1)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Enabling logging within csrf.go
	if *debug {
		initCSRFLogger(logger.With("component", "csrf"))
	}

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
