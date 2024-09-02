package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jackc/pgx/v5/pgxpool"

	"tailscale.com/hostinfo"
)

//go:embed tmpl/*.html
var templateFiles embed.FS

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

	versionGauge.With(prometheus.Labels{"version": version, "git_commit": gitSha}).Set(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if *debug {
		logLevel = slog.LevelDebug
	}

	logger := newLogger(&logLevel)

	dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dbCancel()

	// Open DB connection
	dbconn, err := pgxpool.NewWithConfig(dbCtx, PoolConfig(os.Getenv("DATABASE_URL"), logger))
	if err != nil {
		logger.Error("unable to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer dbconn.Close()

	err = createConfigDir(*dataDir)
	if err != nil {
		logger.Info(fmt.Sprintf("creating configuration directory (%s) failed: %v", *dataDir, err), "data-dir", *dataDir)
	}

	s := NewTsNetServer(dataDir)

	if *tsnetLogVerbose {
		s.Logf = log.Printf
	}

	if err := s.Start(); err != nil {
		logger.Error("error starting tsnet server", slog.String("error", err.Error()))
		return
	}
	defer s.Close()

	lc, err := s.LocalClient()
	if err != nil {
		logger.Error("error getting LocalClient()", slog.String("error", err.Error()))
		return
	}

	err = checkTailscaleReady(ctx, lc, logger)
	if err != nil {
		log.Fatal(err)
		return
	}

	n, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		logger.Error("no hostname for https")
		return
	}

	tmpls := template.Must(template.New("any").Funcs(template.FuncMap{
		"formatTimestamp": formatTimestamp,
	}).ParseFS(templateFiles, "tmpl/*html"))

	dsvc := NewDiscussService(
		lc,
		logger,
		dbconn,
		New(dbconn),
		tmpls,
		n,
		version,
		gitSha,
	)

	// For static assets like css, js etc
	fs := http.FileServer(http.Dir("./static"))

	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("GET /", dsvc.ListThreads)
	tailnetMux.HandleFunc("GET /thread/new", dsvc.ThreadNew)
	tailnetMux.HandleFunc("POST /thread/new", dsvc.CreateThread)
	tailnetMux.HandleFunc("GET /thread/{id}", dsvc.ListThreadPosts)
	tailnetMux.HandleFunc("POST /thread/{id}", dsvc.CreateThreadPost)
	tailnetMux.Handle("GET /metrics", promhttp.Handler())
	tailnetMux.Handle("GET /static/", http.StripPrefix("/static/", fs))

	// Instrument all the routes!
	mux := HistogramHttpHandler(tailnetMux)

	serverPlain := &http.Server{
		Addr:         ":80",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	serverTls := &http.Server{
		Addr:         ":443",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Non-TLS listener
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		logger.Error("error creating non-TLS listener", slog.String("error", err.Error()))
		return
	}
	defer ln.Close()

	// TLS Listener
	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		logger.Error("error creating TLS listener", slog.String("error", err.Error()))
		return
	}
	defer tln.Close()

	go func() {
		logger.Info(fmt.Sprintf("Listening on http://%s", *hostname))
		if err := serverPlain.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "error", err)
		}
	}()

	go func() {
		logger.Info(fmt.Sprintf("Listening on https://%s", n))
		if err := serverTls.Serve(tln); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTPS server failed", "error", err)
		}
	}()

	sig := <-sigChan
	logger.Info("Shutting down gracefully", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 10*time.Second)
	defer shutdownCancel()

	if err := serverPlain.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to gracefully shutdown HTTP server", "error", err)
	}

	if err := serverTls.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to gracefully shutdown HTTPS server", "error", err)
	}

	logger.Info("Servers stopped")

	if sigNum, ok := sig.(syscall.Signal); ok {
		s := 128 + int(sigNum)
		os.Exit(s)
	}
}
