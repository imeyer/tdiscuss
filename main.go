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
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jackc/pgx/v5/pgxpool"

	"tailscale.com/client/tailscale"
	"tailscale.com/hostinfo"
	"tailscale.com/tsnet"
)

//go:embed tmpl/*.html
var templateFiles embed.FS

var (
	hostname               = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss"), "Hostname to use on your tailnet")
	dataDir                = flag.String("data-location", dataLocation(), "Configuration data location.")
	debug                  = flag.Bool("debug", false, "Enable debug logging")
	tsnetLogVerbose        = flag.Bool("tsnet-verbose", false, "Have tsnet log verbosely to standard error")
	version         string = "dev"
	gitSha          string = "no-commit"

	versionGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tdiscuss_build_info",
		Help: "A gauge with version and git commit information",
	}, []string{"version", "git_commit"})
)

func main() {
	flag.Parse()

	prometheus.MustRegister(versionGauge)

	hostinfo.SetApp("tdiscuss")

	versionGauge.With(prometheus.Labels{"version": version, "git_commit": gitSha}).Set(1)

	ctx := context.Background()

	var lvl slog.Level = slog.LevelInfo

	if *debug {
		lvl = slog.LevelDebug
	}

	logger := newLogger(&lvl)

	// Open DB connection
	dbconn, err := pgxpool.NewWithConfig(context.Background(), PoolConfig(os.Getenv("DATABASE_URL"), logger))
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer dbconn.Close()

	err = createConfigDir(*dataDir)
	if err != nil {
		logger.Info(fmt.Sprintf("creating configuration directory (%s) failed: %v", *dataDir, err), "data-dir", *dataDir)
	}

	s := &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tsnet"),
		Hostname: *hostname,
		Logf:     func(string, ...any) {},
	}

	if *tsnetLogVerbose {
		s.Logf = log.Printf
	}

	if err := s.Start(); err != nil {
		log.Fatalf("%v", err)
	}
	defer s.Close()

	lc, _ := s.LocalClient()

	err = checkTailscaleReady(ctx, lc, logger)
	if err != nil {
		log.Fatal(err)
	}

	n, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		log.Fatalf("no hostname for https")
	}
	queries := New(dbconn)

	tmpls := template.Must(template.New("any").Funcs(template.FuncMap{
		"formatTimestamp": formatTimestamp,
	}).ParseFS(templateFiles, "tmpl/*html"))

	dsvc := NewService(
		lc,
		logger,
		dbconn,
		queries,
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
		log.Fatal(err)
	}
	defer ln.Close()

	logger.InfoContext(ctx, fmt.Sprintf("listening on http://%s", *hostname))

	go func() { log.Fatal(serverPlain.Serve(ln)) }()

	// TLS Listener
	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	defer tln.Close()

	logger.InfoContext(ctx, fmt.Sprintf("listening on https://%s", n))

	log.Fatal(serverTls.Serve(tln))
}

func createConfigDir(dir string) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Join(dir, "tsnet"), 0o700)
	if err != nil {
		return err
	}

	return nil
}

func checkTailscaleReady(ctx context.Context, lc *tailscale.LocalClient, logger *slog.Logger) error {
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %w", err)
		} else {
			switch st.BackendState {
			case "NoState":
				logger.DebugContext(ctx, fmt.Sprintf("%v", st), "state", st.BackendState)
				time.Sleep(2 * time.Second)
				continue
			case "NeedsLogin":
				logger.InfoContext(ctx, fmt.Sprintf("login to tailscale at %s", st.AuthURL), "state", st.BackendState)
				time.Sleep(15 * time.Second)
				continue
			case "NeedsMachineAuth":
				logger.InfoContext(ctx, fmt.Sprintf("%v", st), "state", st.BackendState)
				continue
			case "Stopped":
				logger.InfoContext(ctx, "tsnet stopped", "state", st.BackendState)
				return fmt.Errorf("%w", err)
			case "Starting":
				logger.InfoContext(ctx, "starting tsnet", "state", st.BackendState)
				continue
			case "Running":
				nopeers, err := lc.StatusWithoutPeers(ctx)
				if err != nil {
					logger.ErrorContext(ctx, err.Error())
				}
				logger.InfoContext(ctx, "tsnet running", "state", st.BackendState, "certDomains", nopeers.CertDomains)
				return nil
			}
		}
	}
}

func newLogger(logLevel *slog.Level) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))
	slog.SetDefault(logger)

	return logger
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

func formatTimestamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}
