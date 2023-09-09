package main

import (
	"context"
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"tdiscuss/pkg/discuss"
	"time"

	"tailscale.com/client/tailscale"
	"tailscale.com/hostinfo"
	"tailscale.com/tsnet"
)

//go:embed tmpl/*.html
var templateFiles embed.FS

var (
	hostname        = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss"), "Hostname to use on your tailnet (use TSNET_HOSTNAME in the environment)")
	dataDir         = flag.String("data-location", dataLocation(), "Configuration data location. (defaults to DATA_DIR or ~/.config/tailscale/discuss)")
	debug           = flag.Bool("debug", false, "Enable debug logging")
	tsnetLogVerbose = flag.Bool("tsnet-verbose", false, "Have tsnet log verbosely to standard error")
)

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

func main() {
	flag.Parse()

	createConfigDir(*dataDir)

	hostinfo.SetApp("tdiscuss")

	var lvl slog.Level = slog.LevelInfo

	if *debug {
		lvl = slog.LevelDebug
	}

	logger := newLogger(&lvl)

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

	checkTailscaleReady(lc)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	httpsURL, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		slog.InfoContext(ctx, fmt.Sprintf("%v", lc))
		log.Fatal("HTTPS is not enabled in the admin panel")
	}

	tmpls := template.Must(template.ParseFS(templateFiles, "tmpl/*.html"))

	dsvc := discuss.NewService(
		lc,
		logger,
		&sql.DB{},
		tmpls,
		httpsURL,
	)

	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("/", dsvc.DiscussionIndex)
	tailnetMux.HandleFunc("/whoami", dsvc.WhoAmI)

	// Non-TLS listener
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	slog.InfoContext(ctx, fmt.Sprintf("listening on http://%s", *hostname))

	go func() { log.Fatal(http.Serve(ln, tailnetMux)) }()

	// TLS Listener
	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	defer tln.Close()

	slog.InfoContext(ctx, fmt.Sprintf("listening on https://%s", httpsURL))

	log.Fatal(http.Serve(tln, tailnetMux))
}

func createConfigDir(dir string) {
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		log.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(dir, "tsnet"), 0700)
	if err != nil {
		log.Fatal(err)
	}
}

func checkTailscaleReady(lc *tailscale.LocalClient) error {
	for {
		st, err := lc.Status(context.Background())
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %v", err)
		} else {
			switch st.BackendState {
			case "NoState":
				continue
			case "NeedsLogin":
				slog.Info(fmt.Sprintf("Login to tailscale at %s", st.AuthURL))
				continue
			case "NeedsMachineAuth":
				slog.Info(fmt.Sprintf("%v", st))
				continue
			case "Stopped":
				return fmt.Errorf("%v", err)
			case "Starting":
				continue
			case "Running":
				return nil
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func newLogger(logLevel *slog.Level) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))
	slog.SetDefault(logger)

	return logger
}
