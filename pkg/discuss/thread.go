package discuss

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type ThreadData struct{}

type ThreadPostData struct {
	ID          int64
	ThreadPosts []ListThreadPostsRow
	Subject     string
}

func (s *DiscussService) ThreadIndex(w http.ResponseWriter, r *http.Request) {
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
	if err != nil {
		s.logger.Error(err.Error())
		return
	}

	if memberId == 0 {
		s.queries.CreateMember(context.Background(), email)
	}

	threads, err := s.queries.ListThreads(context.Background(), memberId)
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

func (s *DiscussService) DiscussionNew(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/thread/new" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusMethodNotAllowed)), http.StatusMethodNotAllowed)
		return
	}

	err := s.tmpls.ExecuteTemplate(w, "newthread.html", struct {
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

func (s *DiscussService) ListThreads(w http.ResponseWriter, r *http.Request) {
	var tpd ThreadPostData
	threadID, err := ParseThreadID(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	tpd.ID = threadID

	subject, err := s.queries.GetThreadSubjectById(context.Background(), int32(threadID))
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

	memberId, err := s.queries.GetMemberId(context.Background(), email)
	if err != nil {
		s.logger.Error(err.Error())
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusInternalServerError)), http.StatusInternalServerError)
		return
	}

	if memberId == 0 {
		s.queries.CreateMember(context.Background(), email)
	}

	threadPosts, err := s.queries.ListThreadPosts(context.Background(), int32(threadID))
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

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		err := r.ParseMultipartForm(formDataLimit)
		if err != nil {
			s.logger.DebugContext(r.Context(), "cannot parse multipart/form-data", "ip", r.RemoteAddr, "content-type", r.Header.Get("Content-Type"))
		}
	} else if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err := r.ParseForm()
		if err != nil {
			s.logger.DebugContext(r.Context(), "cannot parse multipart/form-data", "ip", r.RemoteAddr, "content-type", r.Header.Get("Content-Type"))
		}
	} else {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: unknown content type: %s", r.RemoteAddr, r.Header.Get("Content-Type")))
		s.RenderError(w, r, fmt.Errorf(http.StatusText(http.StatusBadRequest)), http.StatusBadRequest)
		return
	}

	if !r.Form.Has("thread") && !r.Form.Has("thread_body") {
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

	memberId, err := s.queries.GetMemberId(context.Background(), email)
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
		Subject:      template.HTMLEscapeString(r.Form.Get("thread")),
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
		ThreadID: int32(threadId),
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

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		err := r.ParseMultipartForm(formDataLimit)
		if err != nil {
			s.logger.DebugContext(r.Context(), "cannot parse multipart/form-data", r.RemoteAddr, r.Header.Get("Content-Type"))
		}
	} else if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err := r.ParseForm()
		if err != nil {
			s.logger.DebugContext(r.Context(), "cannot parse multipart/form-data", r.RemoteAddr, r.Header.Get("Content-Type"))
		}
	} else {
		s.logger.DebugContext(r.Context(), fmt.Sprintf("%s: unknown content type: %s", r.RemoteAddr, r.Header.Get("Content-Type")))
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
		ThreadID: int32(threadID),
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

	written, err := w.Write([]byte(http.StatusText(code)))
	if err != nil {
		s.logger.DebugContext(r.Context(), err.Error())
	}
	s.logger.DebugContext(r.Context(), "error response written", "bytes", written)
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
