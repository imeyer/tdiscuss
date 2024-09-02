package main

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type ThreadData struct{}

type ThreadPostData struct {
	ID          int64
	ThreadPosts []ListThreadPostsRow
	Subject     string
}

func (s *DiscussService) ListThreads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusNotFound)), http.StatusMethodNotAllowed)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		return
	}

	memberId, err := s.queries.GetMemberId(r.Context(), email)
	if err != nil && memberId == 0 {
		if err := s.queries.CreateMember(r.Context(), email); err != nil {
			s.logger.Debug(err.Error())
			s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
			return
		}
	}

	start := time.Now()
	threads, err := s.queries.ListThreads(r.Context(), memberId)
	duration := time.Since(start).Seconds()
	listThreadsQueryDuration.WithLabelValues("ListThreads").Observe(duration)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	s.logger.DebugContext(r.Context(), "index fetch", "route", r.URL.Path, "rows", len(threads))
	if err := s.tmpls.ExecuteTemplate(w, "index.html", struct {
		Title   string
		Threads []ListThreadsRow
		Version string
		GitSha  string
	}{
		Version: s.version,
		GitSha:  s.gitSha,
		Title:   "tdiscuss - A Discussion Board for your Tailnet",
		Threads: threads,
	}); err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		return
	}
}

func (s *DiscussService) ThreadNew(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	whois, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
	}

	if err := s.tmpls.ExecuteTemplate(w, "newthread.html", struct {
		User    string
		Title   string
		Version string
		GitSha  string
	}{
		User:    whois.UserProfile.LoginName,
		Title:   "New thread!",
		Version: s.version,
		GitSha:  s.gitSha,
	}); err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}
}

func (s *DiscussService) ListThreadPosts(w http.ResponseWriter, r *http.Request) {
	var tpd ThreadPostData
	threadID, err := ParseThreadID(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	tpd.ID = threadID

	subject, err := s.queries.GetThreadSubjectById(r.Context(), threadID)
	if err != nil {
		s.logger.Error(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	tpd.Subject = subject

	if r.Method != http.MethodGet {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		return
	}

	start := time.Now()
	memberId, err := s.queries.GetMemberId(r.Context(), email)
	duration := time.Since(start).Seconds()
	getMemberIDQueryDuration.WithLabelValues("ListThreadPosts").Observe(duration)
	if err != nil {
		s.logger.Error(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	if memberId == 0 {
		if err := s.queries.CreateMember(r.Context(), email); err != nil {
			s.logger.Error(err.Error())
			s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
			return
		}
	}

	start = time.Now()
	threadPosts, err := s.queries.ListThreadPosts(r.Context(), threadID)
	duration = time.Since(start).Seconds()
	listThreadPostsQueryDuration.WithLabelValues("ListThreadPosts").Observe(duration)
	if err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	tpd.ThreadPosts = threadPosts

	s.logger.DebugContext(r.Context(), "index fetch", "route", r.URL.Path, "rows", len(threadPosts))
	if err := s.tmpls.ExecuteTemplate(w, "thread.html", struct {
		Version string
		GitSha  string
		Title   string
		TPD     ThreadPostData
	}{
		Version: s.version,
		GitSha:  s.gitSha,
		Title:   "tdiscuss - A Discussion Board for your Tailnet",
		TPD:     tpd,
	}); err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		return
	}
}

func (s *DiscussService) CreateThread(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" {
		s.handleError(w, r, "invalid path", http.StatusNotFound)
		return
	}

	if r.Method != "POST" {
		s.handleError(w, r, "invalid method", http.StatusMethodNotAllowed, slog.String("method", r.Method))
		return
	}

	if err := r.ParseForm(); err != nil {
		s.handleError(w, r, "error parsing form", http.StatusBadRequest, slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		return
	}

	if !r.Form.Has("subject") || !r.Form.Has("thread_body") {
		s.handleError(w, r, "missing required fields", http.StatusBadRequest, slog.String("ip", r.RemoteAddr))
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.handleError(w, r, "error getting user email", http.StatusInternalServerError)
		return
	}

	start := time.Now()
	memberId, err := s.queries.GetMemberId(r.Context(), email)
	duration := time.Since(start).Seconds()
	getMemberIDQueryDuration.WithLabelValues("CreateThread").Observe(duration)
	if err != nil {
		s.handleError(w, r, "error fetching member ID", http.StatusInternalServerError)
		return
	}

	threadTx, err := s.dbconn.Begin(r.Context())
	if err != nil {
		s.handleError(w, r, "error beginning transaction", http.StatusInternalServerError)
		return
	}
	defer threadTx.Rollback(r.Context()) // Ensure rollback if anything fails

	qtx1 := s.queries.WithTx(threadTx)
	err = qtx1.CreateThread(r.Context(), CreateThreadParams{
		Subject:      template.HTMLEscapeString(r.Form.Get("subject")),
		MemberID:     memberId,
		LastMemberID: memberId,
	})
	if err != nil {
		s.handleError(w, r, "error creating thread", http.StatusInternalServerError)
		return
	}

	threadId, err := qtx1.GetThreadSequenceId(r.Context())
	if err != nil {
		s.handleError(w, r, "error fetching thread ID", http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	if err := body.Scan(r.Form.Get("thread_body")); err != nil {
		s.handleError(w, r, "error scanning thread body", http.StatusInternalServerError)
		return
	}

	err = qtx1.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadId,
		Body:     body,
		MemberID: memberId,
	})
	if err != nil {
		s.handleError(w, r, "error creating thread post", http.StatusInternalServerError)
		return
	}

	if err := threadTx.Commit(r.Context()); err != nil {
		s.handleError(w, r, "error committing transaction", http.StatusInternalServerError)
		return
	}

	s.logger.DebugContext(r.Context(), "thread created", slog.Int64("threadId", threadId))

	var scheme string = "http"
	var hostname string = *hostname

	// Check if we are serving via https
	if r.TLS != nil {
		certDomain, ok := s.tailClient.ExpandSNIName(r.Context(), hostname)
		if !ok {
			s.handleError(w, r, "can not expand SNI name", http.StatusInternalServerError)
			return
		}

		scheme = "https"
		hostname = certDomain
	}

	if r.Host != hostname {
		s.handleError(w, r, "invalid redirect hostname", http.StatusInternalServerError, slog.String("hostname", hostname), slog.String("r.Host", r.Host))
		return
	}

	// Secure redirect with a fixed scheme and host to avoid open redirect vulnerabilities
	safeRedirectURL := fmt.Sprintf("%s://%s/thread/%d", scheme, hostname, threadId)
	http.Redirect(w, r, safeRedirectURL, http.StatusSeeOther)
}

func (s *DiscussService) CreateThreadPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		s.handleError(w, r, "invalid method", http.StatusMethodNotAllowed)
		return
	}

	threadID, err := ParseThreadID(r.URL.Path)
	if err != nil {
		s.handleError(w, r, "invalid thread ID", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.logger.DebugContext(r.Context(), "error parsing form", slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	threadBody := r.Form.Get("thread_body")
	if threadBody == "" {
		s.logger.DebugContext(r.Context(), "missing thread_body", slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.handleError(w, r, "error getting user email", http.StatusInternalServerError)
		return
	}

	memberId, err := s.queries.GetMemberId(r.Context(), email)
	if err != nil {
		s.handleError(w, r, "error fetching member ID", http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	if err := body.Scan(threadBody); err != nil {
		s.handleError(w, r, "error scanning thread body", http.StatusInternalServerError)
		return
	}

	err = s.queries.CreateThreadPost(r.Context(), CreateThreadPostParams{
		Body:     body,
		MemberID: memberId,
		ThreadID: threadID,
	})
	if err != nil {
		s.handleError(w, r, "error creating thread post", http.StatusInternalServerError)
		return
	}

	// Fixed URL redirect to prevent open redirect vulnerability
	safeRedirectURL := "/"
	http.Redirect(w, r, safeRedirectURL, http.StatusSeeOther)
}

// handleError is a helper function to log and respond with an error message.
func (s *DiscussService) handleError(w http.ResponseWriter, r *http.Request, logMessage string, statusCode int, extraFields ...any) {
	s.logger.DebugContext(r.Context(), logMessage, extraFields...)
	s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(statusCode)), statusCode)
}

func (s *DiscussService) RenderError(w http.ResponseWriter, r *http.Request, err error, code int) {
	s.logger.DebugContext(r.Context(), "rendering error", "error", err.Error())
	if err := s.tmpls.ExecuteTemplate(w, "error.html", struct {
		Error string
	}{
		err.Error(),
	}); err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		return
	}
}

func ParseThreadID(path string) (int64, error) {
	re := regexp.MustCompile(`^/thread/([0-9]+)$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) < 2 {
		return 0, fmt.Errorf("URL does not match the expected pattern")
	}

	id, err := strconv.ParseInt(matches[1], 0, 64)
	if err != nil {
		return 0, fmt.Errorf("error converting ID to integer: %w", err)
	}

	return id, nil
}
