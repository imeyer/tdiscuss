package discuss

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
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	_, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
	}

	if err := s.tmpls.ExecuteTemplate(w, "newthread.html", struct {
		User  string
		Title string
	}{
		User:  "ianmmeyer@gmail.com",
		Title: "New thread!",
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
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusNotFound)), http.StatusNotFound)
		return
	}

	if r.Method != "POST" {
		s.logger.DebugContext(r.Context(), "invalid method", "method", r.Method)
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		s.logger.DebugContext(r.Context(), "unknown content type", slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("subject") && !r.Form.Has("thread_body") {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: thread, and thread_body are required", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	start := time.Now()
	memberId, err := s.queries.GetMemberId(r.Context(), email)
	duration := time.Since(start).Seconds()
	getMemberIDQueryDuration.WithLabelValues("CreateThread").Observe(duration)
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	threadTx, err := s.dbconn.Begin(r.Context())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	qtx1 := s.queries.WithTx(threadTx)
	err = qtx1.CreateThread(r.Context(), CreateThreadParams{
		Subject:      template.HTMLEscapeString(r.Form.Get("subject")),
		MemberID:     memberId,
		LastMemberID: memberId,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	threadId, err := qtx1.GetThreadSequenceId(r.Context())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	err = body.Scan(r.Form.Get("thread_body"))
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	err = qtx1.CreateThreadPost(r.Context(), CreateThreadPostParams{
		ThreadID: threadId,
		Body:     body,
		MemberID: memberId,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	err = threadTx.Commit(r.Context())
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}
	s.logger.DebugContext(r.Context(), "thread created", "threadId", threadId)

	http.Redirect(w, r, fmt.Sprintf("%v://%s/thread/%d", r.URL.Scheme, r.URL.Host, threadId), http.StatusSeeOther)
}

func (s *DiscussService) CreateThreadPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		s.logger.DebugContext(r.Context(), "invalid method", "method", r.Method)
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	threadID, err := ParseThreadID(r.URL.Path)
	if err != nil {
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	err = r.ParseForm()
	if err != nil {
		s.logger.DebugContext(r.Context(), "unknown content type", slog.String("content-type", r.Header.Get("Content-Type")), slog.String("ip", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("thread_body") {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: thread_body is required", r.RemoteAddr))
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	email, err := s.GetTailscaleUserEmail(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting tailscale user")
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	memberId, err := s.queries.GetMemberId(r.Context(), email)
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	var body pgtype.Text
	err = body.Scan(r.Form.Get("thread_body"))
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	err = s.queries.CreateThreadPost(r.Context(), CreateThreadPostParams{
		Body:     body,
		MemberID: memberId,
		ThreadID: threadID,
	})
	if err != nil {
		s.logger.Debug(err.Error())
		s.RenderError(w, r, fmt.Errorf("%v", http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	// var host string

	// switch r.URL.Host {
	// case "discuss":
	// 	host = "discuss"
	// default:
	// 	host = "discuss"
	// }

	http.Redirect(w, r, fmt.Sprintf("%v://%v/", r.URL.Scheme, r.URL.Host), http.StatusSeeOther)
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
