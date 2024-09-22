package main

import (
	"context"
	"embed"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"

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
	version  string     = "dev"
	gitSha   string     = "no-commit"
	logLevel slog.Level = slog.LevelInfo
)

func main() {
	flag.Parse()

	hostinfo.SetApp("tdiscuss")

	logger := setupLogger()
	logger.Info("starting tdiscuss", slog.String("version", version), slog.String("git_sha", gitSha))

	versionGauge.With(prometheus.Labels{"version": version, "git_commit": gitSha, "hostname": *hostname}).Set(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	dbconn, err := setupDatabase(ctx, logger)
	if err != nil {
		logger.Error("failed to connect to	 database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer dbconn.Close()

	s := setupTsNetServer(logger)
	defer s.Close()

	tmpls := setupTemplates()

	lc := getTailscaleLocalClient(s, logger)

	dsvc := NewDiscussService(
		lc,
		logger,
		dbconn,
		New(dbconn),
		tmpls,
		*hostname,
		version,
		gitSha,
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
