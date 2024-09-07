package main

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

// Struct for template data rendering
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
	Body           pgtype.Text
	LastViewPosts  interface{}
	Dot            string
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	Legendary      bool
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
		http.Error(w, string(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	tx, err := s.dbconn.Begin(r.Context())
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	qtx := s.queries.WithTx(tx)

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

	threadID, err := parseThreadID(r.URL.Path)
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
		http.Error(w, string(http.StatusInternalServerError), http.StatusInternalServerError)
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
		http.Error(w, string(http.StatusInternalServerError), http.StatusInternalServerError)
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
		threadsParsed[i] = ThreadTemplateData{
			ThreadID:       thread.ThreadID,
			DateLastPosted: thread.DateLastPosted,
			ID:             thread.ID,
			Email:          thread.Email,
			Lastid:         thread.Lastid,
			Lastname:       thread.Lastname,
			Subject:        template.HTML(parseHTMLStrict(parseMarkdownToHTML(thread.Subject))),
			Posts:          thread.Posts,
			Views:          thread.Views,
			Body:           pgtype.Text{},
			Sticky:         thread.Sticky,
			Locked:         thread.Locked,
		}
	}

	s.renderTemplate(w, r, "index.html", map[string]interface{}{
		"Title":   "tdiscuss - A Discussion Board for your Tailnet",
		"Threads": threadsParsed,
		"Version": s.version,
		"GitSha":  s.gitSha,
	})
}

// ListThreadPosts displays the posts in a specific thread.
func (s *DiscussService) ListThreadPosts(w http.ResponseWriter, r *http.Request) {
	threadID, err := parseThreadID(r.URL.Path)
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
		http.Error(w, string(http.StatusInternalServerError), http.StatusInternalServerError)
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

	s.logger.Info("body", slog.String("body", string(threadPosts[0].Body)))

	s.renderTemplate(w, r, "thread.html", map[string]interface{}{
		"Title":       "tdiscuss...",
		"ThreadPosts": threadPosts,
		"Subject":     template.HTML(subject),
		"ID":          threadID,
		"GitSha":      s.gitSha,
		"Version":     s.version,
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
func (s *DiscussService) ThreadNew(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, string(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, r, "newthread.html", map[string]interface{}{
		"User":    email,
		"Title":   "New thread!",
		"Version": s.version,
		"GitSha":  s.gitSha,
	})
}
