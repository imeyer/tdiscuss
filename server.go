package main

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	tsnetlog "tailscale.com/types/logger"
)

type TailscaleClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	ExpandSNIName(ctx context.Context, name string) (fqdn string, ok bool)
	Status(ctx context.Context) (*ipnstate.Status, error)
	StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error)
}

type ExtendedQuerier interface {
	Querier
	WithTx(tx pgx.Tx) ExtendedQuerier
}

type QueriesWrapper struct {
	*Queries // embedded from pgx
}

func (qw *QueriesWrapper) WithTx(tx pgx.Tx) ExtendedQuerier {
	return &QueriesWrapper{
		Queries: qw.Queries.WithTx(tx),
	}
}

func checkTailscaleReady(ctx context.Context, lc TailscaleClient, logger *slog.Logger) error {
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return fmt.Errorf("error retrieving tailscale status; retrying: %w", err)
		} else {
			switch st.BackendState {
			case "NoState":
				logger.DebugContext(ctx, "no state")
				time.Sleep(5 * time.Second)
				continue
			case "NeedsLogin":
				logger.InfoContext(ctx, "needs login to tailscale", slog.String("auth_url", st.AuthURL))
				time.Sleep(30 * time.Second)
				continue
			case "NeedsMachineAuth":
				logger.DebugContext(ctx, fmt.Sprintf("%v", st))
				continue
			case "Stopped":
				logger.InfoContext(ctx, "tsnet stopped")
				return nil
			case "Starting":
				logger.InfoContext(ctx, "starting tsnet")
				continue
			case "Running":
				nopeers, err := lc.StatusWithoutPeers(ctx)
				if err != nil {
					logger.ErrorContext(ctx, err.Error())
				}
				logger.InfoContext(ctx, "tsnet running", "certDomains", nopeers.CertDomains)
				return nil
			}
		}
	}
}

func createHTTPServer(mux http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":80",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
}

func createHTTPSServer(mux http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":443",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
}

type DiscussService struct {
	tailClient TailscaleClient
	logger     *slog.Logger
	dbconn     *pgxpool.Pool
	queries    Querier
	tmpls      *template.Template
	httpsURL   string
	version    string
	gitSha     string
}

func NewDiscussService(tailClient TailscaleClient,
	logger *slog.Logger,
	dbconn *pgxpool.Pool,
	queries Querier,
	tmpls *template.Template,
	httpsURL string,
	version string,
	gitSha string,
) *DiscussService {
	return &DiscussService{
		tailClient: tailClient,
		dbconn:     dbconn,
		queries:    queries,
		logger:     logger,
		tmpls:      tmpls,
		httpsURL:   httpsURL,
		version:    version,
		gitSha:     gitSha,
	}
}

func NewTsNetServer(dataDir *string) *tsnet.Server {
	return &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tsnet"),
		Hostname: *hostname,
		UserLogf: tsnetlog.Discard,
		Logf:     tsnetlog.Discard,
	}
}

func setupMux(dsvc *DiscussService) http.Handler {
	fs, err := fs.Sub(cssFile, "static")
	if err != nil {
		dsvc.logger.Error("error creating fs for static assets", slog.String("error", err.Error()))
		return nil
	}

	tailnetMux := http.NewServeMux()
	tailnetMux.HandleFunc("GET /", dsvc.ListThreads)
	tailnetMux.HandleFunc("GET /member/{id}", dsvc.ListMember)
	tailnetMux.HandleFunc("GET /member/edit", dsvc.EditMemberProfile)
	tailnetMux.HandleFunc("POST /member/edit", dsvc.EditMemberProfile)
	tailnetMux.HandleFunc("GET /thread/new", dsvc.NewThread)
	tailnetMux.HandleFunc("POST /thread/new", dsvc.CreateThread)
	tailnetMux.HandleFunc("GET /thread/{id}", dsvc.ListThreadPosts)
	tailnetMux.HandleFunc("POST /thread/{id}", dsvc.CreateThreadPost)
	tailnetMux.Handle("GET /metrics", promhttp.Handler())
	tailnetMux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(fs))))

	return HistogramHttpHandler(tailnetMux)
}

func setupTsNetServer(logger *slog.Logger) *tsnet.Server {
	err := createConfigDir(*dataDir)
	if err != nil {
		logger.Info(fmt.Sprintf("creating configuration directory (%s) failed: %v", *dataDir, err), "data-dir", *dataDir)
	}

	s := NewTsNetServer(dataDir)

	if *tsnetLog {
		s.UserLogf = log.Printf
		s.Logf = log.Printf
	}

	if err := s.Start(); err != nil {
		logger.Error("error starting tsnet server", slog.String("error", err.Error()))
		os.Exit(1)
	}

	lc, err := s.LocalClient()
	if err != nil {
		logger.Error("error creating s.LocalClient()", slog.String("error", err.Error()))
		os.Exit(1)
	}

	err = checkTailscaleReady(context.Background(), lc, logger)
	if err != nil {
		logger.Error("tsnet not ready", slog.String("error", err.Error()))
		os.Exit(1)
	}

	return s
}

func startListeners(s *tsnet.Server, logger *slog.Logger) (net.Listener, net.Listener) {
	ln, err := s.Listen("tcp", ":80")
	if err != nil {
		logger.Error("error creating non-TLS listener", slog.String("error", err.Error()))
		os.Exit(1)
	}

	tln, err := s.ListenTLS("tcp", ":443")
	if err != nil {
		logger.Error("error creating TLS listener", slog.String("error", err.Error()))
		os.Exit(1)
	}

	return ln, tln
}

func startServer(server *http.Server, ln net.Listener, logger *slog.Logger, scheme, hostname string) {
	logger.Info(fmt.Sprintf("listening on %s://%s", scheme, hostname))
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error(fmt.Sprintf("%s server failed", scheme), slog.String("error", err.Error()))
	}
}

func waitForShutdown(sigChan chan os.Signal, ctx context.Context, logger *slog.Logger, serverPlain, serverTls *http.Server) {
	sig := <-sigChan
	logger.Info("shutting down gracefully", slog.String("signal", sig.String()))

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 10*time.Second)
	defer shutdownCancel()

	if err := serverPlain.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to gracefully shutdown HTTP server", slog.String("error", err.Error()))
	}

	if err := serverTls.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to gracefully shutdown HTTPS server", slog.String("error", err.Error()))
	}

	if sigNum, ok := sig.(syscall.Signal); ok {
		s := 128 + int(sigNum)
		os.Exit(s)
	}
}

// Struct for template data rendering
type MemberThreadPostTemplateData struct {
	ThreadID       int64
	DateLastPosted pgtype.Timestamptz
	ID             pgtype.Int8
	Email          pgtype.Text
	Lastid         pgtype.Int8
	Lastname       pgtype.Text
	Subject        template.HTML
	Posts          pgtype.Int4
	Views          pgtype.Int4
	LastViewPosts  interface{}
	Dot            string
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
}

type ThreadPostTemplateData struct {
	ID         int64
	DatePosted pgtype.Timestamptz
	MemberID   pgtype.Int8
	Email      pgtype.Text
	Body       template.HTML
	Subject    pgtype.Text
	ThreadID   pgtype.Int8
	IsAdmin    pgtype.Bool
}

type ThreadTemplateData struct {
	ThreadID       int64
	DateLastPosted pgtype.Timestamptz
	ID             pgtype.Int8
	Email          pgtype.Text
	Lastid         pgtype.Int8
	Lastname       pgtype.Text
	Subject        template.HTML
	Posts          pgtype.Int4
	Views          pgtype.Int4
	LastViewPosts  interface{}
	Dot            string
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
}

// CreateThread handles the creation of a new thread.
func (s *DiscussService) CreateThread(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" || r.Method != http.MethodPost {
		s.renderError(w, r, fmt.Errorf("invalid path or method"), http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil || !r.Form.Has("subject") || !r.Form.Has("thread_body") {
		s.renderError(w, r, fmt.Errorf("bad request"), http.StatusBadRequest)
		return
	}

	subject := parseHTMLStrict(parseMarkdownToHTML(r.Form.Get("subject")))
	body := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	memberID, err := s.queries.CreateOrReturnID(r.Context(), email)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	tx, err := s.dbconn.Begin(r.Context())
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	qtx := s.queries.(ExtendedQuerier).WithTx(tx)

	if err := qtx.CreateThread(r.Context(), CreateThreadParams{
		Subject:      subject,
		MemberID:     memberID,
		LastMemberID: memberID,
	}); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	threadID, err := qtx.GetThreadSequenceId(r.Context())
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	if err := qtx.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadID,
		Body:     pgtype.Text{Valid: true, String: body},
		MemberID: memberID,
	}); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

// CreateThreadPost handles adding a new post to an existing thread.
func (s *DiscussService) CreateThreadPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.renderError(w, r, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	threadID, err := parseID(r.URL.Path)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil || r.Form.Get("thread_body") == "" {
		s.renderError(w, r, fmt.Errorf("bad request"), http.StatusBadRequest)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	memberID, err := s.queries.CreateOrReturnID(r.Context(), email)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := s.queries.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadID,
		Body: pgtype.Text{
			Valid:  true,
			String: body,
		},
		MemberID: memberID,
	}); err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

func (s *DiscussService) EditMemberProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		s.renderError(w, r, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	// Get the current user's email
	currentUserEmail, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	// Get the member ID from the URL
	memberID, err := s.queries.GetMemberId(r.Context(), currentUserEmail)
	if err != nil {
		s.renderTemplate(w, r, "edit-profile.html", map[string]interface{}{
			"Title":            BOARD_TITLE + " - Edit Profile",
			"Member":           GetMemberRow{},
			"CurrentUserEmail": currentUserEmail,
			"Version":          s.version,
			"GitSha":           s.gitSha,
		})
		return
	}

	// Get the member details
	member, err := s.queries.GetMember(r.Context(), memberID)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	// Check if the current user is the owner of the profile
	if member.Email != currentUserEmail {
		s.renderError(w, r, fmt.Errorf("unauthorized"), http.StatusForbidden)
		return
	}

	if r.Method == http.MethodGet {
		// Render the edit profile form
		s.renderTemplate(w, r, "edit-profile.html", map[string]interface{}{
			"Title":            BOARD_TITLE + " - Edit Profile",
			"Member":           member,
			"CurrentUserEmail": currentUserEmail,
			"Version":          s.version,
			"GitSha":           s.gitSha,
		})
		return
	}

	// Handle POST request
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, fmt.Errorf("bad request"), http.StatusBadRequest)
		return
	}

	// Get the new photo URL from the form
	newPhotoURL := r.Form.Get("photo_url")
	newLocation := r.Form.Get("location")

	// Update the member's profile
	err = s.queries.UpdateMemberByEmail(r.Context(), UpdateMemberByEmailParams{
		Email: currentUserEmail,
		PhotoUrl: pgtype.Text{
			String: parseHTMLStrict(newPhotoURL),
			Valid:  true,
		},
		Location: pgtype.Text{
			String: parseHTMLStrict(newLocation),
			Valid:  true,
		},
	})
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	// Redirect to the member's profile page
	http.Redirect(w, r, fmt.Sprintf("/member/%d", memberID), http.StatusSeeOther)
}

func (s *DiscussService) GetTailscaleUserEmail(r *http.Request) (string, error) {
	user, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.logger.Debug("get tailscale user email", slog.String("error", err.Error()))
		return "", err
	}

	s.logger.Debug("get tailscale user email", slog.String("user", user.UserProfile.LoginName))
	return user.UserProfile.LoginName, nil
}

// ListThreads displays the list of threads on the main page.
func (s *DiscussService) ListThreads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	memberID, err := s.queries.CreateOrReturnID(r.Context(), email)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	threads, err := s.queries.ListThreads(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	threadsParsed := make([]ThreadTemplateData, len(threads))
	for i, thread := range threads {
		subject := parseHTMLStrict(parseMarkdownToHTML(thread.Subject))

		threadsParsed[i] = ThreadTemplateData{
			ThreadID:       thread.ThreadID,
			DateLastPosted: thread.DateLastPosted,
			ID:             thread.ID,
			Email:          thread.Email,
			Lastid:         thread.Lastid,
			Lastname:       thread.Lastname,
			// nosemgrep
			Subject: template.HTML(subject),
			Posts:   thread.Posts,
			Views:   thread.Views,
			Sticky:  thread.Sticky,
			Locked:  thread.Locked,
		}
	}

	s.renderTemplate(w, r, "index.html", map[string]interface{}{
		"Title":   BOARD_TITLE,
		"Threads": threadsParsed,
		"Version": s.version,
		"GitSha":  s.gitSha,
	})
}

// ListThreadPosts displays the posts in a specific thread.
func (s *DiscussService) ListThreadPosts(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering ListThreadPosts()")
	threadID, err := parseID(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		s.renderError(w, r, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	_, err = s.queries.CreateOrReturnID(r.Context(), email)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	subject, err := s.queries.GetThreadSubjectById(r.Context(), threadID)
	if err != nil {
		s.logger.Error(err.Error())
		s.renderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	subject = parseMarkdownToHTML(parseHTMLStrict(subject))

	posts, err := s.queries.ListThreadPosts(r.Context(), threadID)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	threadPosts := make([]ThreadPostTemplateData, len(posts))
	for i, post := range posts {
		// nosemgrep
		body := template.HTML(parseMarkdownToHTML(parseHTMLLessStrict(post.Body.String)))
		threadPosts[i] = ThreadPostTemplateData{
			ID:         post.ID,
			DatePosted: post.DatePosted,
			MemberID:   post.MemberID,
			Body:       body,
			ThreadID:   post.ThreadID,
			IsAdmin:    post.IsAdmin,
			Email:      post.Email,
			Subject:    pgtype.Text{},
		}
	}

	s.renderTemplate(w, r, "thread.html", map[string]interface{}{
		"Title":       BOARD_TITLE,
		"ThreadPosts": threadPosts,
		// nosemgrep
		"Subject": template.HTML(subject),
		"ID":      threadID,
		"GitSha":  s.gitSha,
		"Version": s.version,
	})
}

func (s *DiscussService) ListMember(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering ListMember()")
	memberID, err := parseID(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	currentUserEmail, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if r.Method != http.MethodGet {
		s.renderError(w, r, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	memberThreads, err := s.queries.ListMemberThreads(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	memberThreadsParsed := make([]MemberThreadPostTemplateData, len(memberThreads))
	for i, mt := range memberThreads {
		memberThreadsParsed[i] = MemberThreadPostTemplateData{
			ThreadID:       mt.ThreadID,
			DateLastPosted: mt.DateLastPosted,
			ID:             mt.ID,
			Email:          mt.Email,
			Lastid:         mt.Lastid,
			Lastname:       mt.Lastname,
			// nosemgrep
			Subject:       template.HTML(mt.Subject),
			Posts:         mt.Posts,
			LastViewPosts: mt.LastViewPosts,
			Dot:           mt.Dot,
			Sticky:        mt.Sticky,
			Locked:        mt.Locked,
		}
	}

	member, err := s.queries.GetMember(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, r, "member.html", map[string]interface{}{
		"Title":            BOARD_TITLE,
		"Member":           member,
		"CurrentUserEmail": currentUserEmail,
		"Threads":          memberThreadsParsed,
		"GitSha":           s.gitSha,
		"Version":          s.version,
	})
}

func (s *DiscussService) renderTemplate(w http.ResponseWriter, r *http.Request, tmpl string, data map[string]interface{}) {
	if err := s.tmpls.ExecuteTemplate(w, tmpl, data); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func (s *DiscussService) renderError(w http.ResponseWriter, r *http.Request, err error, statusCode int) {
	s.logger.ErrorContext(r.Context(), err.Error())
	http.Error(w, http.StatusText(statusCode), statusCode)
}

// ThreadNew displays the page for creating a new thread.
func (s *DiscussService) NewThread(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	_, err = s.queries.CreateOrReturnID(r.Context(), email)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, r, "newthread.html", map[string]interface{}{
		"User":    email,
		"Title":   BOARD_TITLE,
		"Version": s.version,
		"GitSha":  s.gitSha,
	})
}
