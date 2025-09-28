package main

import (
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/imeyer/tdiscuss/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// Template data structures
type MemberThreadPostTemplateData struct {
	MemberID    int64
	MemberEmail string
	PostCount   int64
}

type ThreadPostTemplateData struct {
	ID       int64
	Body     template.HTML
	ThreadID pgtype.Int8
	MemberID pgtype.Int8
	Email    pgtype.Text
	// nosemgrep
	DatePosted pgtype.Timestamptz
	CanEdit    pgtype.Bool
}

type ThreadTemplateData struct {
	ThreadID       int64
	Subject        template.HTML
	Email          pgtype.Text
	Lastid         pgtype.Int8
	Lastname       pgtype.Text
	Posts          pgtype.Int4
	Views          pgtype.Int4
	DateLastPosted pgtype.Timestamptz
	CanEdit        pgtype.Bool
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
}

// Helper methods
func (s *DiscussService) renderTemplate(w http.ResponseWriter, r *http.Request, tmpl string, data map[string]interface{}) {
	if err := s.tmpls.ExecuteTemplate(w, tmpl, data); err != nil {
		s.logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func (s *DiscussService) renderError(w http.ResponseWriter, statusCode int) {
	http.Error(w, http.StatusText(statusCode), statusCode)
}

// Admin handlers
func (s *DiscussService) Admin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.AdminGET(w, r)
	} else if r.Method == http.MethodPost {
		s.AdminPOST(w, r)
	} else {
		s.renderError(w, http.StatusBadRequest)
	}
}

func (s *DiscussService) AdminGET(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "entering AdminGET")
	defer s.logger.DebugContext(r.Context(), "exiting AdminGET")

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting user", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if !user.IsAdmin {
		s.renderError(w, http.StatusForbidden)
		return
	}

	// Get board data with statistics
	boardData, err := s.queries.GetBoardData(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting board data", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	var memberThreadPosts []MemberThreadPostTemplateData

	// TODO: Implement ListAllThreadPostsGroupedByMember query
	// For now, return empty list
	memberThreadPosts = []MemberThreadPostTemplateData{
		// Stub data for testing
		{MemberID: 1, MemberEmail: "admin@example.com", PostCount: 10},
		{MemberID: 2, MemberEmail: "user@example.com", PostCount: 5},
	}

	s.logger.DebugContext(r.Context(), "rendering admin template")

	s.renderTemplate(w, r, "admin.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"BoardData":        boardData,
		"Posts":            memberThreadPosts,
		"Version":          s.version,
		"GitSha":           s.gitSha,
		"CurrentUserEmail": user.Email,
		"User":             user,
	})
}

func (s *DiscussService) AdminPOST(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "AdminPOST")
	defer span.End()

	s.logger.DebugContext(r.Context(), "entering AdminPOST")
	defer s.logger.DebugContext(r.Context(), "exiting AdminPOST")

	r = r.WithContext(ctx)

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting user", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if !user.IsAdmin {
		s.renderError(w, http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	action := r.Form.Get("action")
	memberIDStr := r.Form.Get("member_id")
	threadIDStr := r.Form.Get("thread_id")

	var memberID int64
	if memberIDStr != "" {
		memberID, err = strconv.ParseInt(memberIDStr, 10, 64)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "error parsing member ID", slog.String("error", err.Error()))
			s.renderError(w, http.StatusBadRequest)
			return
		}
	}

	var threadID int64
	if threadIDStr != "" {
		threadID, err = strconv.ParseInt(threadIDStr, 10, 64)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "error parsing thread ID", slog.String("error", err.Error()))
			s.renderError(w, http.StatusBadRequest)
			return
		}
	}

	switch action {
	case "delete_member":
		if memberID <= 0 {
			s.logger.ErrorContext(r.Context(), "invalid member ID", slog.Int64("memberID", memberID))
			s.renderError(w, http.StatusBadRequest)
			return
		}
		// Block the member instead of deleting
		err := s.queries.BlockMember(r.Context(), memberID)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "failed to block member",
				slog.Int64("memberID", memberID),
				slog.String("error", err.Error()))
			s.renderError(w, http.StatusInternalServerError)
			return
		}
		s.logger.InfoContext(r.Context(), "member blocked successfully", slog.Int64("memberID", memberID))
	case "delete_thread":
		// TODO: Implement DeleteThread query
		s.logger.InfoContext(r.Context(), "DeleteThread not implemented", slog.Int64("threadID", threadID))
		// For now, just log and redirect
	case "update_config":
		boardTitle := r.Form.Get("board_title")
		editWindowStr := r.Form.Get("edit_window")

		// Update board title
		if boardTitle != "" {
			if err := s.queries.UpdateBoardTitle(r.Context(), boardTitle); err != nil {
				s.logger.ErrorContext(r.Context(), "failed to update board title", 
					slog.String("error", err.Error()))
				s.renderError(w, http.StatusInternalServerError)
				return
			}
		}

		// Update edit window
		if editWindowStr != "" {
			editWindow, err := strconv.Atoi(editWindowStr)
			if err != nil {
				s.logger.ErrorContext(r.Context(), "invalid edit window value", 
					slog.String("error", err.Error()))
				s.renderError(w, http.StatusBadRequest)
				return
			}
			
			if err := s.queries.UpdateBoardEditWindow(r.Context(), pgtype.Int4{Int32: int32(editWindow), Valid: true}); err != nil {
				s.logger.ErrorContext(r.Context(), "failed to update edit window", 
					slog.String("error", err.Error()))
				s.renderError(w, http.StatusInternalServerError)
				return
			}
		}

		s.logger.InfoContext(r.Context(), "board config updated successfully",
			slog.String("board_title", boardTitle),
			slog.String("edit_window", editWindowStr))
	default:
		s.logger.ErrorContext(r.Context(), "unknown action", slog.String("action", action))
		s.renderError(w, http.StatusBadRequest)
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

	// CSRF validation is handled by middleware

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
		s.logger.DebugContext(r.Context(), "CreateThread", slog.String("user_hash", hashEmail(user.Email)), slog.String("user_id", strconv.FormatInt(user.ID, 10)))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("r.ParseForm")
	if err := r.ParseForm(); err != nil {
		s.logger.DebugContext(r.Context(), "error parsing form", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	// Get and sanitize inputs
	subjectInput := SanitizeInput(r.Form.Get("subject"))
	bodyInput := SanitizeInput(r.Form.Get("thread_body"))

	// Validate inputs
	if errors := ValidateThreadForm(subjectInput, bodyInput); len(errors) > 0 {
		s.logger.DebugContext(r.Context(), "validation failed", slog.String("errors", errors.Error()))
		http.Error(w, errors.Error(), http.StatusBadRequest)
		return
	}

	span.AddEvent("r.ParseSubject")
	// For subjects, just sanitize HTML without markdown parsing (single-line text)
	subject := parseHTMLStrict(subjectInput)
	span.AddEvent("r.ParseBody")
	// For body content, parse markdown and allow more HTML tags
	body := parseHTMLLessStrict(parseMarkdownToHTML(bodyInput))

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

	// CSRF validation is handled by middleware

	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "CreateThreadPost", slog.String("user_hash", hashEmail(user.Email)), slog.Int64("user_id", user.ID))
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

	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	// Get and sanitize input
	bodyInput := SanitizeInput(r.Form.Get("thread_body"))

	// Validate input
	if errors := ValidateThreadPostForm(bodyInput); len(errors) > 0 {
		s.logger.DebugContext(r.Context(), "validation failed", slog.String("errors", errors.Error()))
		http.Error(w, errors.Error(), http.StatusBadRequest)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(bodyInput))

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


	if r.Method == http.MethodGet {
		// Render the edit profile form
		s.renderTemplate(w, r, "edit-profile.html", map[string]interface{}{
			"Title":            GetBoardTitle(r),
			"Member":           member,
			"CurrentUserEmail": user.Email,
			"Version":          s.version,
			"GitSha":           s.gitSha,
						"User":             user,
		})
		return
	}

	// Handle POST request
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest)
		return
	}

	// Get and sanitize inputs
	newPhotoURL := SanitizeInput(r.Form.Get("photo_url"))
	newLocation := SanitizeInput(r.Form.Get("location"))
	newPreferredName := SanitizeInput(r.Form.Get("preferred_name"))
	newBio := SanitizeInput(r.Form.Get("bio"))
	newPronouns := SanitizeInput(r.Form.Get("pronouns"))

	// Validate inputs
	if errors := ValidateProfileForm(newPhotoURL, newLocation, newPreferredName, newBio, newPronouns); len(errors) > 0 {
		s.logger.DebugContext(r.Context(), "validation failed", slog.String("errors", errors.Error()))
		http.Error(w, errors.Error(), http.StatusBadRequest)
		return
	}

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

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "GetUser", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Get and sanitize inputs
	bodyInput := SanitizeInput(r.Form.Get("thread_body"))
	subjectInput := SanitizeInput(r.Form.Get("subject"))

	// Validate inputs
	if errors := ValidateThreadForm(subjectInput, bodyInput); len(errors) > 0 {
		s.logger.DebugContext(r.Context(), "validation failed", slog.String("errors", errors.Error()))
		http.Error(w, errors.Error(), http.StatusBadRequest)
		return
	}

	// For body content, parse markdown and allow more HTML tags
	body := parseHTMLLessStrict(parseMarkdownToHTML(bodyInput))
	// For subjects, just sanitize HTML without markdown parsing (single-line text)
	subject := parseHTMLStrict(subjectInput)

	// Parse thread ID from path
	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error parsing thread ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	t, err := s.queries.GetThreadForEdit(r.Context(), GetThreadForEditParams{
		ID:   threadID,
		ID_2: user.ID,
	})

	if err != nil {
		if err == pgx.ErrNoRows {
			s.logger.ErrorContext(r.Context(), "GetThreadForEdit: no such thread or thread does not belong to user", slog.String("error", err.Error()))
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(r.Context(), "GetThreadForEdit", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	threadID = t.ThreadID
	threadPostID := t.ThreadPostID.Int64

	s.logger.DebugContext(r.Context(), "editThreadPOST", slog.Int64("threadID", threadID), slog.Int64("threadPostID", threadPostID))

	if t.Subject != subject {
		err = s.queries.UpdateThread(r.Context(), UpdateThreadParams{
			Subject:  subject,
			ID:       threadID,
			MemberID: user.ID,
		})
		if err != nil {
			s.logger.ErrorContext(r.Context(), "UpdateThread", slog.String("error", err.Error()))
			s.renderError(w, http.StatusInternalServerError)
			return
		}
	}

	if t.Body.String != body {
		err = s.queries.UpdateThreadPost(r.Context(), UpdateThreadPostParams{
			Body: pgtype.Text{
				Valid:  true,
				String: body,
			},
			ID:       threadPostID,
			MemberID: user.ID,
		})
		if err != nil {
			s.logger.ErrorContext(r.Context(), "UpdateThreadPost", slog.String("error", err.Error()))
			s.renderError(w, http.StatusInternalServerError)
			return
		}
	}

	// nosemgrep
	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

func (s *DiscussService) editThreadGET(w http.ResponseWriter, r *http.Request) {
	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "GetUser", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}


	// Parse thread ID from path
	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error parsing thread ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	t, err := s.queries.GetThreadForEdit(r.Context(), GetThreadForEditParams{
		ID:   threadID,
		ID_2: user.ID,
	})

	if err != nil {
		if err == pgx.ErrNoRows {
			s.logger.ErrorContext(r.Context(), "GetThreadForEdit: no such thread", slog.String("error", err.Error()))
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(r.Context(), "GetThreadForEdit", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Render the edit form
	s.renderTemplate(w, r, "edit-thread.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"User":             user,
		"CurrentUserEmail": user.Email,
		"Version":          s.version,
		"GitSha":           s.gitSha,
				"Thread":           t,
	})
}

func (s *DiscussService) EditThreadPost(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "EditThreadPost", slog.String("tid", r.PathValue("tid")), slog.String("pid", r.PathValue("pid")))

	switch r.Method {
	case http.MethodGet:
		s.editThreadPostGET(w, r)
	case http.MethodPost:
		s.editThreadPostPOST(w, r)
	default:
		s.renderError(w, http.StatusMethodNotAllowed)
	}
}

func (s *DiscussService) editThreadPostPOST(w http.ResponseWriter, r *http.Request) {
	s.logger.DebugContext(r.Context(), "editThreadPostPOST", slog.String("path", r.URL.Path))

	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "GetUser", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.logger.ErrorContext(r.Context(), "ParseForm", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	// Get and sanitize input
	bodyInput := SanitizeInput(r.Form.Get("thread_post_body"))

	// Validate input
	if errors := ValidateThreadPostForm(bodyInput); len(errors) > 0 {
		s.logger.DebugContext(r.Context(), "validation failed", slog.String("errors", errors.Error()))
		http.Error(w, errors.Error(), http.StatusBadRequest)
		return
	}

	body := parseHTMLLessStrict(parseMarkdownToHTML(bodyInput))

	// Parse thread post ID from path
	postIDStr := r.PathValue("pid")
	postID, err := strconv.ParseInt(postIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error parsing post ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	tp, err := s.queries.GetThreadPostForEdit(r.Context(), GetThreadPostForEditParams{
		ID:   postID,
		ID_2: user.ID,
	})

	if err != nil {
		if err == pgx.ErrNoRows {
			s.logger.ErrorContext(r.Context(), "GetThreadPostForEdit: no such thread post", slog.String("error", err.Error()))
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(r.Context(), "GetThreadPostForEdit", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	err = s.queries.UpdateThreadPost(r.Context(), UpdateThreadPostParams{
		Body: pgtype.Text{
			Valid:  true,
			String: body,
		},
		ID:       tp.ID,
		MemberID: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "UpdateThreadPost", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Get thread ID from path for redirect
	threadIDStr := r.PathValue("tid")
	// nosemgrep
	http.Redirect(w, r, fmt.Sprintf("/thread/%s", threadIDStr), http.StatusSeeOther)
}

func (s *DiscussService) editThreadPostGET(w http.ResponseWriter, r *http.Request) {
	user, err := GetUser(r)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "GetUser", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}


	// Parse thread post ID from path
	postIDStr := r.PathValue("pid")
	postID, err := strconv.ParseInt(postIDStr, 10, 64)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error parsing post ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	t, err := s.queries.GetThreadPostForEdit(r.Context(), GetThreadPostForEditParams{
		ID:   postID,
		ID_2: user.ID,
	})

	if err != nil {
		if err == pgx.ErrNoRows {
			s.logger.ErrorContext(r.Context(), "GetThreadPostForEdit: no such thread", slog.String("error", err.Error()))
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(r.Context(), "GetThreadPostForEdit", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Render the edit form
	s.renderTemplate(w, r, "edit-thread-post.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"User":             user,
		"CurrentUserEmail": user.Email,
		"Version":          s.version,
		"GitSha":           s.gitSha,
				"ThreadPost":       t,
	})
}

func (s *DiscussService) GetTailscaleUserEmail(r *http.Request) (string, error) {
	// Handle development mode
	if s.devMode {
		return "dev@example.com", nil
	}

	remoteAddr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", fmt.Errorf("failed to split remote address: %w", err)
	}

	// Check if the IP is local and handle accordingly
	if remoteAddr == "127.0.0.1" || remoteAddr == "::1" {
		// For local requests, try to get the real client IP from headers
		if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
			remoteAddr = forwardedFor
		}
	}

	whois, err := s.tailClient.WhoIs(r.Context(), remoteAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get tailscale user info: %w", err)
	}

	return whois.UserProfile.LoginName, nil
}

// ListThreads handles listing all threads.
func (s *DiscussService) ListThreads(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "ListThreads")
	defer span.End()

	r = r.WithContext(ctx)

	if r.URL.Path != "/" {
		s.renderError(w, http.StatusNotFound)
		return
	}

	if r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error getting user", "error", err)
		s.renderError(w, http.StatusInternalServerError)
		return
	}


	span.AddEvent("queries.ListThreads")
	threads, err := s.queries.ListThreads(r.Context(), ListThreadsParams{
		Email:    user.Email,
		MemberID: user.ID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error listing threads", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	span.AddEvent("map threads to template data")
	var threadData []ThreadTemplateData
	for _, thread := range threads {
		// Subject is already sanitized HTML from the database, no need to parse again
		threadData = append(threadData, ThreadTemplateData{
			ThreadID:       thread.ThreadID,
			Subject:        template.HTML(thread.Subject),
			Email:          thread.Email,
			Lastid:         thread.Lastid,
			Lastname:       thread.Lastname,
			Posts:          thread.Posts,
			Views:          thread.Views,
			DateLastPosted: thread.DateLastPosted,
			CanEdit:        pgtype.Bool{Bool: thread.CanEdit, Valid: true},
			Sticky:         thread.Sticky,
			Locked:         thread.Locked,
		})
	}

	span.AddEvent("render template")
	s.renderTemplate(w, r, "index.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"Threads":          threadData,
		"Version":          s.version,
		"GitSha":           s.gitSha,
		"CurrentUserEmail": user.Email,
				"User":             user,
	})
}

// ListThreadPosts handles displaying a specific thread with its posts.
func (s *DiscussService) ListThreadPosts(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.telemetry.Tracer.Start(r.Context(), "ListThreadPosts")
	defer span.End()

	r = r.WithContext(ctx)

	if r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	span.AddEvent("parseID")
	threadIDStr := r.PathValue("tid")
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error parsing thread ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	span.AddEvent("GetUser")
	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error getting user", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}


	span.AddEvent("queries.GetThreadSubject")
	subject, err := s.queries.GetThreadSubjectById(r.Context(), threadID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting thread subject", slog.String("error", err.Error()))
		if err == pgx.ErrNoRows {
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	subject = parseMarkdownToHTML(parseHTMLStrict(subject))

	posts, err := s.queries.ListThreadPosts(r.Context(), ListThreadPostsParams{
		Email:    user.Email,
		ThreadID: threadID,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error listing thread posts", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	var threadPosts []ThreadPostTemplateData
	for _, post := range posts {
		threadPosts = append(threadPosts, ThreadPostTemplateData{
			ID:       post.ID,
			Body:     template.HTML(post.Body.String),
			ThreadID: post.ThreadID,
			MemberID: post.MemberID,
			Email:    post.Email,
			// nosemgrep
			DatePosted: post.DatePosted,
			CanEdit:    pgtype.Bool{Bool: post.CanEdit, Valid: true},
		})
	}

	s.renderTemplate(w, r, "thread.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"CurrentUserEmail": user.Email,
		"ThreadPosts":      threadPosts,
		// nosemgrep
		"Subject":   template.HTML(subject),
		"ID":        threadID,
		"GitSha":    s.gitSha,
		"Version":   s.version,
				"User":      user,
	})
}

// ListMember displays a member's profile.
func (s *DiscussService) ListMember(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed)
		return
	}

	memberIDStr := r.PathValue("mid")
	memberID, err := strconv.ParseInt(memberIDStr, 10, 64)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error parsing member ID", slog.String("error", err.Error()))
		s.renderError(w, http.StatusBadRequest)
		return
	}

	user, err := GetUser(r)
	if err != nil {
		s.logger.DebugContext(r.Context(), "error getting user", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Get member details
	member, err := s.queries.GetMember(r.Context(), memberID)
	if err != nil {
		if err == pgx.ErrNoRows {
			s.renderError(w, http.StatusNotFound)
			return
		}
		s.logger.ErrorContext(r.Context(), "error getting member", slog.String("error", err.Error()))
		s.renderError(w, http.StatusInternalServerError)
		return
	}

	// Get member's threads
	threads, err := s.queries.ListMemberThreads(r.Context(), memberID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "error getting member threads", slog.String("error", err.Error()))
		// Don't fail the page, just show empty threads
		threads = []ListMemberThreadsRow{}
	}

	// Check if the current user can edit this profile
	canEdit := user.ID == memberID || user.IsAdmin


	s.renderTemplate(w, r, "member.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"Member":           member,
		"Threads":          threads,
		"CanEdit":          canEdit,
		"CurrentUserEmail": user.Email,
		"Version":          s.version,
		"GitSha":           s.gitSha,
				"User":             user,
		"TotalPosts":       len(threads),
	})
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


	s.renderTemplate(w, r, "newthread.html", map[string]interface{}{
		"Title":            GetBoardTitle(r),
		"CurrentUserEmail": user.Email,
		"Version":          s.version,
		"GitSha":           s.gitSha,
				"User":             user,
	})
}

// OTELMiddleware provides OpenTelemetry instrumentation for HTTP handlers
func OTELMiddleware(serviceName string, s *DiscussService) func(next http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, span := s.telemetry.Tracer.Start(r.Context(), r.URL.Path)
			defer span.End()

			r = r.WithContext(ctx)

			// Record request started
			labels := []attribute.KeyValue{
				attribute.String("method", r.Method),
				attribute.String("route", r.URL.Path),
			}

			// Capture response
			wrapped := &handlerResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Handle the request
			defer func() {
				if rec := recover(); rec != nil {
					span.RecordError(fmt.Errorf("panic: %v", rec))
					span.SetStatus(codes.Error, "panic occurred")

					// Record panic metric
					if s.telemetry.Metrics.ErrorCounter != nil {
						s.telemetry.Metrics.ErrorCounter.Add(ctx, 1, metric.WithAttributes(
							append(labels,
								attribute.Int("status", http.StatusInternalServerError),
								attribute.String("error", "panic"),
							)...,
						))
					}

					// Re-panic to let the recovery middleware handle it
					panic(rec)
				}
			}()

			// Execute the handler
			startTime := time.Now()
			next.ServeHTTP(wrapped, r)
			duration := time.Since(startTime).Seconds()

			// Update labels with actual status
			labels = append(labels, attribute.Int("status", wrapped.statusCode))

			// Record metrics
			if s.telemetry.Metrics.RequestCounter != nil {
				s.telemetry.Metrics.RequestCounter.Add(ctx, 1, metric.WithAttributes(labels...))
			}

			if s.telemetry.Metrics.RequestDuration != nil {
				s.telemetry.Metrics.RequestDuration.Record(ctx, duration, metric.WithAttributes(labels...))
			}

			// Set span attributes
			span.SetAttributes(labels...)

			// Set status based on HTTP status code
			if wrapped.statusCode >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.statusCode))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}
	}
}

// handlerResponseWriter wraps http.ResponseWriter to capture the status code for handlers
type handlerResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *handlerResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// GetBoardTitle returns the configured board title
func GetBoardTitle(r *http.Request) string {
	// Try to get from context first (set by middleware)
	if r != nil && r.Context() != nil {
		ctx := r.Context()
		if boardData, ok := middleware.GetBoardData(ctx); ok && boardData != nil {
			// The middleware returns GetBoardDataRow directly, not a pointer
			if bd, ok := boardData.(GetBoardDataRow); ok {
				return bd.Title
			}
			// Also try as a pointer (in case middleware behavior varies)
			if bd, ok := boardData.(*GetBoardDataRow); ok && bd != nil {
				return bd.Title
			}
		}
	}
	return "tdiscuss" // Default fallback
}

// HealthCheck handles health check requests
func (s *DiscussService) HealthCheck(w http.ResponseWriter, r *http.Request) {
	// Simple health check - could be expanded to check database connectivity, etc.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
