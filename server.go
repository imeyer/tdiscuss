package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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

type User struct {
	ID      int64
	Email   string
	IsAdmin bool
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
	telemetry  *TelemetryConfig
}

func NewDiscussService(tailClient TailscaleClient,
	logger *slog.Logger,
	dbconn *pgxpool.Pool,
	queries Querier,
	tmpls *template.Template,
	httpsURL string,
	version string,
	gitSha string,
	telemetry *TelemetryConfig,
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
		telemetry:  telemetry,
	}
}

func NewTsNetServer(dataDir *string, hostname string) *tsnet.Server {
	return &tsnet.Server{
		Dir:      filepath.Join(*dataDir, "tsnet"),
		Hostname: hostname,
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

	tailnetMux.HandleFunc("POST /admin", CSRFMiddleware(dsvc.Admin))
	tailnetMux.HandleFunc("POST /member/edit", CSRFMiddleware(dsvc.EditMemberProfile))
	tailnetMux.HandleFunc("POST /thread/new", CSRFMiddleware(dsvc.CreateThread))
	tailnetMux.HandleFunc("POST /thread/{tid}/edit", CSRFMiddleware(dsvc.EditThread))
	tailnetMux.HandleFunc("POST /thread/{tid}/{pid}/edit", CSRFMiddleware(dsvc.EditThreadPost))
	tailnetMux.HandleFunc("POST /thread/{tid}", CSRFMiddleware(dsvc.CreateThreadPost))

	tailnetMux.HandleFunc("GET /{$}", CSRFMiddleware(dsvc.ListThreads))
	tailnetMux.HandleFunc("GET /admin", CSRFMiddleware(dsvc.Admin))
	tailnetMux.HandleFunc("GET /member/{mid}", CSRFMiddleware(dsvc.ListMember))
	tailnetMux.HandleFunc("GET /member/edit", CSRFMiddleware(dsvc.EditMemberProfile))
	tailnetMux.HandleFunc("GET /thread/new", CSRFMiddleware(dsvc.NewThread))
	tailnetMux.HandleFunc("GET /thread/{tid}", CSRFMiddleware(dsvc.ListThreadPosts))
	tailnetMux.HandleFunc("GET /thread/{tid}/edit", CSRFMiddleware(dsvc.EditThread))
	tailnetMux.HandleFunc("GET /thread/{tid}/{pid}/edit", CSRFMiddleware(dsvc.EditThreadPost))
	tailnetMux.Handle("GET /_/metrics", promhttp.Handler())
	tailnetMux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(fs))))

	userMiddleware := UserMiddleware(dsvc, tailnetMux)
	securityMiddleware := SecurityHeadersMiddleware(userMiddleware)

	return OTELMiddleware(securityMiddleware, dsvc.telemetry)
}

func setupTsNetServer(logger *slog.Logger) *tsnet.Server {
	err := createConfigDir(*dataDir)
	if err != nil {
		logger.Info(fmt.Sprintf("creating configuration directory (%s) failed: %v", *dataDir, err), "data-dir", *dataDir)
	}

	s := NewTsNetServer(dataDir, *hostname)

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
	LastViewPosts  any
	Dot            pgtype.Bool
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	CanEdit        pgtype.Bool
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
	CanEdit    pgtype.Bool
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
	LastViewPosts  any
	Dot            string
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	CanEdit        pgtype.Bool
}

func (s *DiscussService) Admin(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering Admin()")
	defer s.logger.DebugContext(r.Context(), "exiting Admin()")

	switch r.Method {
	case http.MethodGet:
		s.AdminGET(w, r)
	case http.MethodPost:
		s.AdminPOST(w, r)
	default:
		s.renderError(w, http.StatusMethodNotAllowed)
	}
}

func (s *DiscussService) AdminGET(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering AdminGET()")
	defer s.logger.DebugContext(r.Context(), "exiting AdminGET()")

	csrfToken := GetCSRFToken(r)

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if !user.IsAdmin {
		s.logger.ErrorContext(
			r.Context(),
			"user is not admin",
			slog.String("email", user.Email),
			slog.Int64("user_id", user.ID),
			slog.Bool("is_admin", user.IsAdmin),
		)
		s.renderError(w, http.StatusForbidden)
		return
	}

	boardData, err := s.queries.GetBoardData(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, r, "admin.html", map[string]interface{}{
		"Title":     GetBoardTitle(r),
		"BoardData": boardData,
		"Version":   s.version,
		"GitSha":    s.gitSha,
		"CSRFToken": csrfToken,
		"User":      user,
	})
}

func (s *DiscussService) AdminPOST(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering AdminPOST()")
	defer s.logger.DebugContext(r.Context(), "exiting AdminPOST()")

	if err := validateCSRFToken(r); err != nil {
		s.logger.ErrorContext(r.Context(), "CSRF validation failed", slog.String("error", err.Error()))
		http.Error(w, "CSRF validation failed", http.StatusForbidden)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if !user.IsAdmin {
		s.logger.ErrorContext(
			r.Context(),
			"user is not admin",
			slog.String("email", user.Email),
			slog.Int64("user_id", user.ID),
			slog.Bool("is_admin", user.IsAdmin),
		)
		s.renderError(w, http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusBadRequest)
		return
	}

	if !r.Form.Has("board_title") {
		s.logger.ErrorContext(r.Context(), "missing board_title")
		s.renderError(w, http.StatusBadRequest)
		return
	}

	boardTitle := r.Form.Get("board_title")

	if err := s.queries.UpdateBoardTitle(r.Context(), boardTitle); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if !r.Form.Has("edit_window") {
		s.logger.ErrorContext(r.Context(), "missing edit_window")
		s.renderError(w, http.StatusBadRequest)
		return
	}

	editWindow, err := strconv.ParseInt(r.Form.Get("edit_window"), 10, 32)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusBadRequest)
		return
	}

	if err := s.queries.UpdateBoardEditWindow(r.Context(), pgtype.Int4{Int32: int32(editWindow), Valid: true}); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	s.logger.Info("success")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// CreateThread handles the creation of a new thread.
func (s *DiscussService) CreateThread(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "CreateThread(svc)")
	defer span.End()

	r = r.WithContext(ctx)

	if err := validateCSRFToken(r); err != nil {
		s.logger.ErrorContext(r.Context(), "CSRF validation failed", slog.String("error", err.Error()))
		http.Error(w, "CSRF validation failed", http.StatusForbidden)
		return
	}

	if r.URL.Path != "/thread/new" {
		s.renderError(w, http.StatusNotFound)
		return
	}

	if r.Method != http.MethodPost {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	span.AddEvent("GetUser(r)")
	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "CreateThread", slog.String("user", user.Email), slog.String("user_id", strconv.FormatInt(user.ID, 10)))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("r.ParseForm")
	if err := r.ParseForm(); err != nil || !r.Form.Has("subject") || !r.Form.Has("thread_body") {
		s.logger.DebugContext(r.Context(), "error parsing form", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	span.AddEvent("r.ParseSubject")
	subject := parseHTMLStrict(parseMarkdownToHTML(r.Form.Get("subject")))
	span.AddEvent("r.ParseBody")
	body := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))

	span.AddEvent("BeginTxn")
	tx, err := s.dbconn.Begin(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error starting transaction", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	qtx := s.queries.(ExtendedQuerier).WithTx(tx)

	span.AddEvent("qtx.CreateThread")
	if err := qtx.CreateThread(r.Context(), CreateThreadParams{
		Subject:      subject,
		MemberID:     user.ID,
		LastMemberID: user.ID,
	}); err != nil {
		s.logger.ErrorContext(r.Context(), "error creating thread", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("qtx.GetThreadSequenceId")
	threadID, err := qtx.GetThreadSequenceId(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting thread sequence ID", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("qtx.CreateThreadPost")
	if err := qtx.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadID,
		Body:     pgtype.Text{Valid: true, String: body},
		MemberID: user.ID,
	}); err != nil {
		s.logger.ErrorContext(r.Context(), "error creating thread post", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("tx.Commit")
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.ErrorContext(r.Context(), "error committing transaction", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// nosemgrep
	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

// CreateThreadPost handles adding a new post to an existing thread.
func (s *DiscussService) CreateThreadPost(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "CreateThreadPost")
	defer span.End()

	r = r.WithContext(ctx)

	if err := validateCSRFToken(r); err != nil {
		s.logger.ErrorContext(r.Context(), "CSRF validation failed", slog.String("error", err.Error()))
		http.Error(w, "CSRF validation failed", http.StatusForbidden)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "CreateThreadPost", slog.String("user", user.Email), slog.Int64("user_id", user.ID))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	receivedToken := r.FormValue("csrf_token")
	s.logger.DebugContext(r.Context(), "received csrf token", slog.String("token", receivedToken))

	if r.Method != http.MethodPost {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error parsing thread ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil || r.Form.Get("thread_body") == "" {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))

	if err := s.queries.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadID,
		Body: pgtype.Text{
			Valid:  true,
			String: body,
		},
		MemberID: user.ID,
	}); err != nil {
		s.logger.ErrorContext(r.Context(), "error creating thread post", slog.String("SQLError", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// nosemgrep
	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

func (s *DiscussService) EditMemberProfile(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering EditMemberProfile")
	defer s.logger.DebugContext(r.Context(), "exiting EditMemberProfile")

	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "EditMemberProfile", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Get the member details
	member, err := s.queries.GetMember(r.Context(), user.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Check if the current user is the owner of the profile
	if member.Email != user.Email {
		s.renderError(w, http.StatusForbidden)
		return
	}

	csrfToken := GetCSRFToken(r)

	if r.Method == http.MethodGet {
		// Render the edit profile form
		s.renderTemplate(w, r, "edit-profile.html", map[string]interface{}{
			"Title":            GetBoardTitle(r),
			"Member":           member,
			"CurrentUserEmail": user.Email,
			"Version":          s.version,
			"GitSha":           s.gitSha,
			"CSRFToken":        csrfToken,
			"User":             user,
		})
		return
	}

	// Handle POST request
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	// Get the new photo URL from the form
	newPhotoURL := r.Form.Get("photo_url")
	newLocation := r.Form.Get("location")
	newPreferredName := r.Form.Get("preferred_name")
	newBio := r.Form.Get("bio")
	newPronouns := r.Form.Get("pronouns")

	// Update the member's profile
	err = s.queries.UpdateMemberProfileByID(r.Context(), UpdateMemberProfileByIDParams{
		MemberID: user.ID,
		PhotoUrl: pgtype.Text{
			String: parseHTMLStrict(newPhotoURL),
			Valid:  true,
		},
		Location: pgtype.Text{
			String: parseHTMLStrict(newLocation),
			Valid:  true,
		},
		PreferredName: pgtype.Text{
			String: parseHTMLStrict(newPreferredName),
			Valid:  true,
		},
		Bio: pgtype.Text{
			String: parseHTMLStrict(newBio),
			Valid:  true,
		},
		Pronouns: pgtype.Text{
			String: parseHTMLStrict(newPronouns),
			Valid:  true,
		},
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "UpdateMemberProfileByID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Redirect to the member's profile page
	// nosemgrep
	http.Redirect(w, r, fmt.Sprintf("/member/%d", user.ID), http.StatusSeeOther)
}

func (s *DiscussService) EditThread(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "EditThreadPost", slog.String("tid", r.PathValue("tid")))

	switch r.Method {
	case http.MethodGet:
		s.editThreadGET(w, r)
	case http.MethodPost:
		s.editThreadPOST(w, r)
	default:
		s.renderError(w, http.StatusMethodNotAllowed)
	}
}

func (s *DiscussService) editThreadPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.logger.ErrorContext(r.Context(), "ParseForm", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	re := regexp.MustCompile(`^/thread/\d+/edit$`)
	matches := re.FindStringSubmatch(r.URL.Path)
	s.logger.DebugContext(r.Context(), "editThreadPOST", slog.String("path", r.URL.Path), slog.Any("matches", matches))
	if len(matches) < 1 {
		s.logger.ErrorContext(r.Context(), "Invalid path")
		s.renderError(w, http.StatusBadRequest)
		return
	}

	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusBadRequest)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))
	subject := parseHTMLStrict(parseMarkdownToHTML(r.Form.Get("subject")))

	t, err := s.queries.GetThreadForEdit(r.Context(), GetThreadForEditParams{
		ID:   threadID,
		ID_2: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if t.Body.String != body {
		err = s.queries.UpdateThreadPost(r.Context(), UpdateThreadPostParams{
			ID:       t.ThreadPostID.Int64,
			Body:     pgtype.Text{Valid: true, String: body},
			MemberID: user.ID,
		})
		if err != nil {
			s.logger.ErrorContext(r.Context(), err.Error())
			s.renderError(w, http.StatusInternalServerError)
			return
		}
	}

	if t.Subject != subject {
		err = s.queries.UpdateThread(r.Context(), UpdateThreadParams{
			Subject:  subject,
			ID:       threadID,
			MemberID: user.ID,
		})
	}

	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

func (s *DiscussService) editThreadGET(w http.ResponseWriter, r *http.Request) {
	csrfToken := GetCSRFToken(r)

	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	thread, err := s.queries.GetThreadForEdit(r.Context(), GetThreadForEditParams{
		ID:   threadID,
		ID_2: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, r, "edit-thread.html", map[string]interface{}{
		"Title":     GetBoardTitle(r),
		"Thread":    thread,
		"Version":   s.version,
		"CSRFToken": csrfToken,
		"GitSha":    s.gitSha,
		"User":      user,
	})
	return
}

func (s *DiscussService) EditThreadPost(w http.ResponseWriter, r *http.Request) {
	s.logger.ErrorContext(r.Context(), "EditThreadPost", slog.String("tid", r.PathValue("tid")), slog.String("pid", r.PathValue("pid")))

	threadIDStr := r.PathValue("tid")
	postIDStr := r.PathValue("pid")

	if threadIDStr == "" {
		s.logger.ErrorContext(r.Context(), "Missing thread ID")
		s.renderError(w, http.StatusBadRequest)
		return
	}

	if postIDStr == "" {
		s.logger.ErrorContext(r.Context(), "Missing post ID")
		s.renderError(w, http.StatusBadRequest)
		return
	}

	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "Invalid thread ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	postID, err := strconv.ParseInt(postIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "Invalid post ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.editThreadPostGET(w, r, threadID, postID)
	case http.MethodPost:
		s.editThreadPostPOST(w, r, threadID, postID)
	default:
		s.renderError(w, http.StatusMethodNotAllowed)
	}
}

func (s *DiscussService) editThreadPostPOST(w http.ResponseWriter, r *http.Request, threadID, postID int64) {
	if err := r.ParseForm(); err != nil {
		s.logger.ErrorContext(r.Context(), "ParseForm", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	tmpbody := parseHTMLLessStrict(parseMarkdownToHTML(r.Form.Get("thread_body")))
	if tmpbody == "" {
		s.logger.ErrorContext(r.Context(), "thread_body is empty", slog.String("thread_body", tmpbody))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(tmpbody))

	tp, err := s.queries.GetThreadPostForEdit(r.Context(), GetThreadPostForEditParams{
		ID:   postID,
		ID_2: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if tp.Body.String != body {
		err = s.queries.UpdateThreadPost(r.Context(), UpdateThreadPostParams{
			ID:       postID,
			Body:     pgtype.Text{Valid: true, String: body},
			MemberID: user.ID,
		})
		if err != nil {
			s.renderError(w, http.StatusInternalServerError)
			return
		}
	}

	// Redirect to the thread page
	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

func (s *DiscussService) editThreadPostGET(w http.ResponseWriter, r *http.Request, threadID, postID int64) {
	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	s.logger.DebugContext(r.Context(), "editThreadPostGET", slog.Int64("threadID", threadID), slog.Int64("postID", postID), slog.Any("user", user))

	post, err := s.queries.GetThreadPostForEdit(r.Context(), GetThreadPostForEditParams{
		ID:   postID,
		ID_2: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	csrfToken := GetCSRFToken(r)

	s.renderTemplate(w, r, "edit-thread-post.html", map[string]interface{}{
		"Title":     BOARD_TITLE,
		"Version":   s.version,
		"CSRFToken": csrfToken,
		"Post":      post,
		"ThreadID":  threadID,
		"GitSha":    s.gitSha,
		"User":      user,
	})
}

func (s *DiscussService) GetTailscaleUserEmail(r *http.Request) (string, error) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "GetTailscaleUserEmail")
	defer span.End()

	if s.tailClient == nil {
		return "", fmt.Errorf("TailscaleClient is nil")
	}

	user, err := s.tailClient.WhoIs(ctx, r.RemoteAddr)
	if err != nil {
		s.logger.Debug("get tailscale user email", slog.String("error", err.Error()))
		return "", err
	}

	ctx = context.WithValue(ctx, "email", user.UserProfile.LoginName)
	r = r.WithContext(ctx)

	span.SetStatus(codes.Ok, "")
	s.logger.Debug("get tailscale user email", slog.String("user", user.UserProfile.LoginName))
	return user.UserProfile.LoginName, nil
}

// ListThreads displays the list of threads on the main page.
func (s *DiscussService) ListThreads(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "ListThreads")
	defer span.End()

	r = r.WithContext(ctx)

	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		s.logger.ErrorContext(r.Context(), http.StatusText(http.StatusMethodNotAllowed))
		s.renderError(w, http.StatusMethodNotAllowed)
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	threads, err := s.queries.ListThreads(r.Context(), ListThreadsParams{
		Email:    user.Email,
		MemberID: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
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
			CanEdit: pgtype.Bool{
				Bool: thread.CanEdit,
			},
		}
	}

	s.renderTemplate(w, r, "index.html", map[string]any{
		"Title":   GetBoardTitle(r),
		"Threads": threadsParsed,
		"Version": s.version,
		"GitSha":  s.gitSha,
		"User":    user,
	})
}

// ListThreadPosts displays the posts in a specific thread.
func (s *DiscussService) ListThreadPosts(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	s.logger.InfoContext(r.Context(), "starting ListThreadPosts",
		slog.String("request_id", requestID),
		slog.String("path", r.URL.Path),
		slog.String("method", r.Method))

	defer s.logger.DebugContext(r.Context(), "finished ListThreadPosts", slog.String("request_id", requestID))

	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error(), slog.String("thread_id", threadIDStr))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	subject, err := s.queries.GetThreadSubjectById(r.Context(), threadID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	subject = parseMarkdownToHTML(parseHTMLStrict(subject))

	posts, err := s.queries.ListThreadPosts(r.Context(), ListThreadPostsParams{
		ThreadID: threadID,
		Email:    user.Email,
	})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError)
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
			CanEdit:    pgtype.Bool{Valid: true, Bool: post.CanEdit},
		}
	}

	csrfToken := GetCSRFToken(r)

	s.renderTemplate(w, r, "thread.html", map[string]interface{}{
		"Title":       GetBoardTitle(r),
		"ThreadPosts": threadPosts,
		// nosemgrep
		"Subject":   template.HTML(subject),
		"ID":        threadID,
		"GitSha":    s.gitSha,
		"Version":   s.version,
		"CSRFToken": template.HTML(csrfToken),
		"User":      user,
	})
}

func (s *DiscussService) ListMember(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering ListMember()")
	defer s.logger.DebugContext(r.Context(), "exiting ListMember()")

	ctx, span := s.telemetry.Tracer.Start(r.Context(), "discuss.ListMember.Entry")
	defer span.End()

	r = r.WithContext(ctx)

	if r.Method != http.MethodGet {
		s.logger.ErrorContext(r.Context(), http.StatusText(http.StatusMethodNotAllowed))
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	memberIDStr := r.PathValue("mid")
	memberID, err := strconv.ParseInt(memberIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusBadRequest)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	memberThreads, err := s.queries.ListMemberThreads(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
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
			Dot:           pgtype.Bool{Bool: mt.Dot},
			Sticky:        mt.Sticky,
			Locked:        mt.Locked,
		}
	}

	member, err := s.queries.GetMember(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.SetStatus(codes.Ok, "")

	s.renderTemplate(w, r, "member.html", map[string]interface{}{
		"Title":   GetBoardTitle(r),
		"Member":  member,
		"Threads": memberThreadsParsed,
		"GitSha":  s.gitSha,
		"Version": s.version,
		"User":    user,
	})
}

func (s *DiscussService) renderTemplate(w http.ResponseWriter, r *http.Request, tmpl string, data map[string]interface{}) {
	if err := s.tmpls.ExecuteTemplate(w, tmpl, data); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func (s *DiscussService) renderError(w http.ResponseWriter, statusCode int) {
	http.Error(w, http.StatusText(statusCode), statusCode)
}

// NewThread displays the page for creating a new thread.
func (s *DiscussService) NewThread(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	csrfToken := GetCSRFToken(r)

	s.renderTemplate(w, r, "newthread.html", map[string]interface{}{
		"User":      user,
		"Title":     GetBoardTitle(r),
		"CSRFToken": csrfToken,
		"Version":   s.version,
		"GitSha":    s.gitSha,
	})
}

func OTELMiddleware(next http.Handler, telemetry *TelemetryConfig) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a ResponseWriter that captures the status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Replace numeric IDs in paths with :id placeholder for consistent metrics
		re := regexp.MustCompile(`/(\d+)`)
		sanitizedPath := re.ReplaceAllString(r.URL.Path, "/:id")

		ctx, span := telemetry.Tracer.Start(r.Context(), sanitizedPath)
		defer span.End()

		newr := r.Clone(ctx)

		// Process the request through the next handler
		next.ServeHTTP(rw, newr)

		// Calculate duration after request completes
		duration := time.Since(start).Seconds()

		attrs := attribute.NewSet(
			attribute.String("path", sanitizedPath),
			attribute.String("method", r.Method),
			attribute.String("status_code", strconv.Itoa(rw.statusCode)),
		)

		telemetry.Metrics.RequestCounter.Add(r.Context(), 1,
			metric.WithAttributeSet(attrs))

		// Record the request duration
		if telemetry.Metrics.RequestDuration != nil {
			telemetry.Metrics.RequestDuration.Record(r.Context(), duration,
				metric.WithAttributeSet(attrs),
			)
		}
	})
}

// UserMiddleware is a middleware that adds the user's email and ID to the request context.
func UserMiddleware(s *DiscussService, next http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var span trace.Span
		ctx, span := s.telemetry.Tracer.Start(r.Context(), "UserMiddleware")
		defer span.End()

		r = r.WithContext(ctx)

		email, err := s.GetTailscaleUserEmail(r)
		if err != nil {
			s.logger.ErrorContext(r.Context(), err.Error())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			s.renderError(w, http.StatusInternalServerError)
			return
		}

		ctx = context.WithValue(r.Context(), "email", email)

		boardData, err := s.queries.GetBoardData(r.Context())
		if err != nil {
			s.logger.ErrorContext(r.Context(), err.Error())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			s.renderError(w, http.StatusInternalServerError)
			return
		}

		// Set the board title in the context
		ctx = context.WithValue(ctx, "board_title", boardData.Title)

		user, err := s.queries.CreateOrReturnID(ctx, email)
		if err != nil {
			s.logger.ErrorContext(ctx, err.Error())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			s.renderError(w, http.StatusInternalServerError)
			return
		}

		ctx = context.WithValue(ctx, "user_id", user.ID)
		ctx = context.WithValue(ctx, "is_admin", user.IsAdmin)

		s.logger.DebugContext(ctx, "UserMiddleware", slog.String("email", email), slog.Int64("user_id", user.ID), slog.Bool("is_admin", user.IsAdmin))

		span.SetStatus(codes.Ok, "")

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SecurityHeadersMiddleware adds security headers to all HTTP responses
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Basic security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		
		// Strict Transport Security (HSTS) - only on HTTPS
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		
		// Content Security Policy - restrictive by default
		// Note: We use 'unsafe-inline' for styles due to the embedded CSS
		// In production, consider moving styles to external files with hashes
		w.Header().Set("Content-Security-Policy", 
			"default-src 'self'; "+
			"script-src 'self'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: https:; "+
			"font-src 'self'; "+
			"connect-src 'self'; "+
			"frame-ancestors 'none'; "+
			"base-uri 'self'; "+
			"form-action 'self'; "+
			"upgrade-insecure-requests")
		
		next.ServeHTTP(w, r)
	})
}

// Helper function to get the user from the request context
func GetUser(r *http.Request) (User, error) {
	ctx := r.Context()
	var u User

	if uid, ok := ctx.Value("user_id").(int64); ok {
		u.ID = uid
	} else {
		return User{}, errors.New("user_id not found in context or invalid type")
	}

	if email, ok := ctx.Value("email").(string); ok {
		u.Email = email
	} else {
		return User{}, errors.New("email not found in context or invalid type")
	}

	if isAdmin, ok := ctx.Value("is_admin").(bool); ok {
		u.IsAdmin = isAdmin
	} else {
		return User{}, errors.New("is_admin not found in context or invalid type")
	}

	return u, nil
}

func GetBoardTitle(r *http.Request) string {
	ctx := r.Context()
	if title, ok := ctx.Value("board_title").(string); ok {
		return title
	}

	return BOARD_TITLE
}
