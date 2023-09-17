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

	"github.com/imeyer/tdiscuss/pkg/discuss"

	"tailscale.com/client/tailscale"
	"tailscale.com/hostinfo"
	"tailscale.com/tsnet"
)

//go:embed tmpl/*.html
var templateFiles embed.FS

var (
	hostname        = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss"), "Hostname to use on your tailnet")
	dataDir         = flag.String("data-location", dataLocation(), "Configuration data location.")
	debug           = flag.Bool("debug", false, "Enable debug logging")
	tsnetLogVerbose = flag.Bool("tsnet-verbose", false, "Have tsnet log verbosely to standard error")
)

func main() {
	flag.Parse()

	hostinfo.SetApp("tdiscuss")

	ctx := context.Background()

	var lvl slog.Level = slog.LevelInfo

	if *debug {
		lvl = slog.LevelDebug
	}

	logger := newLogger(&lvl)

	err := createConfigDir(*dataDir)
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

	lc, err := s.LocalClient()
	if err != nil {
		log.Fatalf("%v", err)
	}

	err = checkTailscaleReady(ctx, lc, logger)
	if err != nil {
		log.Fatal(err)
	}

	tsnetstatus, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		logger.InfoContext(ctx, fmt.Sprintf("%v", err))
		log.Fatalf("HTTPS is not enabled in the admin panel: %v", err)
	}

	httpsURL := tsnetstatus.CertDomains[0]

	if httpsURL == "" {
		log.Fatal("No HTTPS domain returned")
	}

	// Open DB connection
	db, err := discuss.NewSQLiteDB(fmt.Sprintf("%s/%s", *dataDir, "discuss.db"), logger)
	if err != nil {
		log.Fatal(err)
	}

	tmpls := template.Must(template.ParseFS(templateFiles, "tmpl/*.html"))

	dsvc := discuss.NewService(
		lc,
		logger,
		db,
		tmpls,
		httpsURL,
	)

	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("/", dsvc.DiscussionIndex)
	tailnetMux.HandleFunc("/topic/new", dsvc.DiscussionNew)
	tailnetMux.HandleFunc("/topic/save", dsvc.DiscussionSave)
	tailnetMux.HandleFunc("/topic/{id}", dsvc.DiscussionTopic)

	// Non-TLS listener
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	logger.InfoContext(ctx, fmt.Sprintf("listening on http://%s", *hostname))

	go func() { log.Fatal(http.Serve(ln, tailnetMux)) }()

	// TLS Listener
	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	defer tln.Close()

	logger.InfoContext(ctx, fmt.Sprintf("listening on https://%s", httpsURL))

	log.Fatal(http.Serve(tln, tailnetMux))
}

func createConfigDir(dir string) error {
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Join(dir, "tsnet"), 0700)
	if err != nil {
		return err
	}

	return nil
}

func checkTailscaleReady(ctx context.Context, lc *tailscale.LocalClient, logger *slog.Logger) error {
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %v", err)
		} else {
			switch st.BackendState {
			case "NoState":
				logger.InfoContext(ctx, fmt.Sprintf("%v", st), "state", st.BackendState)
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
				return fmt.Errorf("%v", err)
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
