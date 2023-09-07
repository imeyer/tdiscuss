package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/exp/slog"
	"tailscale.com/client/tailscale"
	"tailscale.com/hostinfo"
	"tailscale.com/tsnet"
)

//go:embed tmpl/*.html
var templateFiles embed.FS

var (
	hostname        = flag.String("hostname", envOr("TSNET_HOSTNAME", "discuss"), "hostname to use on your tailnet, TSNET_HOSTNAME in the environment")
	dataDir         = flag.String("data-location", dataLocation(), "where data is stored, defaults to DATA_DIR or ~/.config/tailscale/discuss")
	slogLevel       = flag.String("slog-level", envOr("SLOG_LEVEL", "INFO"), "log level")
	tsnetLogVerbose = flag.Bool("tsnet-verbose", false, "if set, have tsnet log verbosely to standard error")
)

const formDataLimit = 64 * 1024 // 64 kilobytes (approx. 32 printed pages of text)

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

type Server struct {
	lc *tailscale.LocalClient
	// db       *sql.DB
	tmpls    *template.Template
	httpsURL string
}

func (s *Server) DiscussionIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.NotFound(w, r)
		return
	}

	if err := s.tmpls.ExecuteTemplate(w, "index.html", map[string]any{}); err != nil {
		return
	}
}

func (s *Server) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "%v not found", r.URL.Path)
}

func main() {
	flag.Parse()

	hostinfo.SetApp("tdiscuss")

	// Set log level, configure logger
	var programLevel slog.Level
	if err := (&programLevel).UnmarshalText([]byte(*slogLevel)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid log level %s: %v, using INFO\n", *slogLevel, err)
		programLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     programLevel,
	})))

	slog.Info("starting tdiscuss")

	os.MkdirAll(*dataDir, 0700)
	os.MkdirAll(filepath.Join(*dataDir, "tsnet"), 0700)

	s := &tsnet.Server{
		Dir:        filepath.Join(*dataDir, "tsnet"),
		Store:      nil,
		Hostname:   *hostname,
		Ephemeral:  false,
		AuthKey:    "",
		ControlURL: "",
		Port:       0,
	}

	if err := s.Start(); err != nil {
		slog.Error("%v", err)
	}
	defer s.Close()

	lc, err := s.LocalClient()
	if err != nil {
		slog.Error("%v", err)
	}

	if *tsnetLogVerbose {
		s.Logf = log.Printf
	}

	for i := 0; i < 12; i++ {
		st, err := lc.Status(context.Background())
		if err != nil {
			slog.Error("error retrieving tailscale status; retrying: %v", err)
		} else {
			if st.BackendState == "Running" {
				break
			}
		}
		slog.Info("%v", st)
		time.Sleep(5 * time.Second)
	}

	ctx := context.Background()
	httpsURL, ok := lc.ExpandSNIName(ctx, *hostname)
	if !ok {
		slog.Info(httpsURL)
		log.Fatal("HTTPS is not enabled in the admin panel")
	}

	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}

	tmpls := template.Must(template.ParseFS(templateFiles, "tmpl/*.html"))

	srv := &Server{lc, tmpls, httpsURL}

	tailnetMux := http.NewServeMux()

	tailnetMux.HandleFunc("/", srv.DiscussionIndex)
	log.Fatal(http.Serve(ln, tailnetMux))
	// log.Printf("listening on http://%s", *hostname)

	// ln, err := s.ListenTLS("tcp", ":443")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer ln.Close()
	// slog.Info("listening on https://%s", httpsURL)
	// log.Fatal(http.Serve(ln, tailnetMux))
}
