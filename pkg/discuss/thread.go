package discuss

import (
	"context"
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
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusNotFound)), http.StatusMethodNotAllowed)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		return
	}

	memberId, err := s.queries.GetMemberId(context.Background(), email)
	if err != nil && memberId == 0 {
		s.queries.CreateMember(context.Background(), email)
	}

	start := time.Now()
	threads, err := s.queries.ListThreads(context.Background(), memberId)
	duration := time.Since(start).Seconds()
	listThreadsQueryDuration.WithLabelValues("ListThreads").Observe(duration)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	s.logger.DebugContext(r.Context(), "index fetch", "route", r.URL.Path, "rows", len(threads))
	if err := s.tmpls.ExecuteTemplate(w, "index.html", struct {
		Title   string
		Threads []ListThreadsRow
	}{
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
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	_, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
	}

	err = s.tmpls.ExecuteTemplate(w, "newthread.html", struct {
		User  string
		Title string
	}{
		User:  "ianmmeyer@gmail.com",
		Title: "New thread!",
	})
	if err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		s.RenderError(w, r, err, http.StatusUnsupportedMediaType)
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

	subject, err := s.queries.GetThreadSubjectById(context.Background(), threadID)
	if err != nil {
		s.logger.Error(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	tpd.Subject = subject

	if r.Method != http.MethodGet {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		return
	}

	start := time.Now()
	memberId, err := s.queries.GetMemberId(context.Background(), email)
	duration := time.Since(start).Seconds()
	getMemberIDQueryDuration.WithLabelValues("ListThreadPosts").Observe(duration)
	if err != nil {
		s.logger.Error(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	if memberId == 0 {
		s.queries.CreateMember(context.Background(), email)
	}

	start = time.Now()
	threadPosts, err := s.queries.ListThreadPosts(context.Background(), threadID)
	duration = time.Since(start).Seconds()
	listThreadPostsQueryDuration.WithLabelValues("ListThreadPosts").Observe(duration)
	if err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	tpd.ThreadPosts = threadPosts

	s.logger.DebugContext(r.Context(), "index fetch", "route", r.URL.Path, "rows", len(threadPosts))
	if err := s.tmpls.ExecuteTemplate(w, "thread.html", struct {
		Title string
		TPD   ThreadPostData
	}{
		Title: "tdiscuss - A Discussion Board for your Tailnet",
		TPD:   tpd,
	}); err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
		return
	}
}

func (s *DiscussService) CreateThread(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusNotFound)), http.StatusNotFound)
		return
	}

	if r.Method != "POST" {
		s.logger.DebugContext(r.Context(), "invalid method", "method", r.Method)
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		s.logger.DebugContext(r.Context(), "unknown content type", slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("subject") && !r.Form.Has("thread_body") {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: thread, and thread_body are required", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	start := time.Now()
	memberId, err := s.queries.GetMemberId(context.Background(), email)
	duration := time.Since(start).Seconds()
	getMemberIDQueryDuration.WithLabelValues("CreateThread").Observe(duration)
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	threadTx, err := s.dbconn.Begin(context.Background())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	qtx1 := s.queries.WithTx(threadTx)
	err = qtx1.CreateThread(context.Background(), CreateThreadParams{
		Subject:      template.HTMLEscapeString(r.Form.Get("subject")),
		MemberID:     memberId,
		LastMemberID: memberId,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	threadId, err := qtx1.GetThreadSequenceId(context.Background())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	body.Scan(r.Form.Get("thread_body"))

	err = qtx1.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadId,
		Body:     body,
		MemberID: memberId,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	err = threadTx.Commit(context.Background())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}
	s.logger.DebugContext(r.Context(), "thread created", "threadId", threadId)

	http.Redirect(w, r, fmt.Sprintf("%v://%s/thread/%d", r.URL.Scheme, r.URL.Host, threadId), http.StatusSeeOther)
}

func (s *DiscussService) CreateThreadPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		s.logger.DebugContext(r.Context(), "invalid method", "method", r.Method)
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	threadID, err := ParseThreadID(r.URL.Path)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	err = r.ParseForm()
	if err != nil {
		s.logger.DebugContext(r.Context(), "unknown content type", slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("thread_body") {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: thread_body is required", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		return
	}

	memberId, err := s.queries.GetMemberId(context.Background(), email)
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	body.Scan(r.Form.Get("thread_body"))

	err = s.queries.CreateThreadPost(context.Background(), CreateThreadPostParams{
		Body:     body,
		MemberID: memberId,
		ThreadID: threadID,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, err, http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("%v://%s/", r.URL.Scheme, r.URL.Host), http.StatusSeeOther)
}

func (s *DiscussService) RenderError(w http.ResponseWriter, r *http.Request, err error, code int) {
	s.logger.DebugContext(r.Context(), "rendering error", "error", err.Error())
	responseBody := []byte(http.StatusText(code))
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(responseBody)))
	w.WriteHeader(code)

	written, writeErr := w.Write(responseBody)
	if writeErr != nil {
		s.logger.DebugContext(r.Context(), "error writing response", "error", writeErr.Error())
	}
	s.logger.DebugContext(r.Context(), "error response written", slog.String("bytes", fmt.Sprint(written)))
}

func ParseThreadID(path string) (int64, error) {
	re := regexp.MustCompile(`^/thread/([0-9]+)$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) < 2 {
		return 0, fmt.Errorf("URL does not match the expected pattern")
	}

	id, err := strconv.ParseInt(matches[1], 0, 64)
	if err != nil {
		return 0, fmt.Errorf("error converting ID to integer: %v", err)
	}

	return id, nil
}
