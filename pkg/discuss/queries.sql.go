// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.26.0
// source: queries.sql

package discuss

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const createMember = `-- name: CreateMember :exec
INSERT INTO member (email) values ($1)
`

func (q *Queries) CreateMember(ctx context.Context, email string) error {
	_, err := q.db.Exec(ctx, createMember, email)
	return err
}

const createThread = `-- name: CreateThread :exec
INSERT INTO thread (subject,member_id,last_member_id) VALUES ($1,$2,$3)
`

type CreateThreadParams struct {
	Subject      string
	MemberID     int64
	LastMemberID int64
}

func (q *Queries) CreateThread(ctx context.Context, arg CreateThreadParams) error {
	_, err := q.db.Exec(ctx, createThread, arg.Subject, arg.MemberID, arg.LastMemberID)
	return err
}

const createThreadPost = `-- name: CreateThreadPost :exec
INSERT INTO
  thread_post
    (thread_id,body,member_id)
  VALUES
    ($1,$2,$3)
`

type CreateThreadPostParams struct {
	ThreadID int64
	Body     pgtype.Text
	MemberID int64
}

func (q *Queries) CreateThreadPost(ctx context.Context, arg CreateThreadPostParams) error {
	_, err := q.db.Exec(ctx, createThreadPost, arg.ThreadID, arg.Body, arg.MemberID)
	return err
}

const getMemberId = `-- name: GetMemberId :one
SELECT id FROM member WHERE email = $1
`

func (q *Queries) GetMemberId(ctx context.Context, email string) (int64, error) {
	row := q.db.QueryRow(ctx, getMemberId, email)
	var id int64
	err := row.Scan(&id)
	return id, err
}

const getThreadPostSequenceId = `-- name: GetThreadPostSequenceId :one
SELECT currval('thread_id_post_seq')
`

func (q *Queries) GetThreadPostSequenceId(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, getThreadPostSequenceId)
	var currval int64
	err := row.Scan(&currval)
	return currval, err
}

const getThreadSequenceId = `-- name: GetThreadSequenceId :one
SELECT currval('thread_id_seq')
`

func (q *Queries) GetThreadSequenceId(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, getThreadSequenceId)
	var currval int64
	err := row.Scan(&currval)
	return currval, err
}

const getThreadSubjectById = `-- name: GetThreadSubjectById :one
SELECT subject FROM thread WHERE id=$1
`

func (q *Queries) GetThreadSubjectById(ctx context.Context, id int64) (string, error) {
	row := q.db.QueryRow(ctx, getThreadSubjectById, id)
	var subject string
	err := row.Scan(&subject)
	return subject, err
}

const listThreadPosts = `-- name: ListThreadPosts :many
SELECT
  tp.id,
  tp.date_posted,
  m.id as member_id,
  m.email,
  tp.body,
  t.subject,
  t.id as thread_id,
  m.is_admin
FROM
  thread_post tp
LEFT JOIN
  member m
ON
  m.id=tp.member_id
LEFT JOIN
  thread t
ON
  t.id = tp.thread_id
WHERE tp.thread_id=$1
ORDER BY tp.date_posted ASC
`

type ListThreadPostsRow struct {
	ID         int64
	DatePosted pgtype.Timestamptz
	MemberID   pgtype.Int8
	Email      pgtype.Text
	Body       pgtype.Text
	Subject    pgtype.Text
	ThreadID   pgtype.Int8
	IsAdmin    pgtype.Bool
}

func (q *Queries) ListThreadPosts(ctx context.Context, threadID int64) ([]ListThreadPostsRow, error) {
	rows, err := q.db.Query(ctx, listThreadPosts, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ListThreadPostsRow
	for rows.Next() {
		var i ListThreadPostsRow
		if err := rows.Scan(
			&i.ID,
			&i.DatePosted,
			&i.MemberID,
			&i.Email,
			&i.Body,
			&i.Subject,
			&i.ThreadID,
			&i.IsAdmin,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listThreads = `-- name: ListThreads :many
SELECT
  t.id as thread_id,
  t.date_last_posted,
  m.id,
  m.email,
  l.id as lastid,
  l.email as lastname,
  t.subject,
  t.posts,
  t.views,
  tp.body,
  (CASE WHEN tm.last_view_posts IS null THEN 0 ELSE tm.last_view_posts END) as last_view_posts,
  (CASE WHEN tm.date_posted IS NOT null AND tm.undot IS false AND tm.member_id IS NOT null THEN 't' ELSE 'f' END) as dot,
  t.sticky,
  t.locked,
  t.legendary
FROM
  thread t
LEFT JOIN
  member m
ON
  m.id=t.member_id
LEFT JOIN
  member l
ON
  l.id=t.last_member_id
LEFT JOIN
  thread_post tp
ON
  tp.id=t.first_post_id
LEFT OUTER JOIN
  thread_member tm
ON
  (tm.member_id=$1 AND tm.thread_id=t.id)
WHERE t.sticky IS false
  AND tm.ignore IS NOT TRUE
ORDER BY t.date_last_posted DESC
LIMIT 100
`

type ListThreadsRow struct {
	ThreadID       int64
	DateLastPosted pgtype.Timestamptz
	ID             pgtype.Int8
	Email          pgtype.Text
	Lastid         pgtype.Int8
	Lastname       pgtype.Text
	Subject        string
	Posts          pgtype.Int4
	Views          pgtype.Int4
	Body           pgtype.Text
	LastViewPosts  interface{}
	Dot            string
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	Legendary      bool
}

func (q *Queries) ListThreads(ctx context.Context, memberID int64) ([]ListThreadsRow, error) {
	rows, err := q.db.Query(ctx, listThreads, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ListThreadsRow
	for rows.Next() {
		var i ListThreadsRow
		if err := rows.Scan(
			&i.ThreadID,
			&i.DateLastPosted,
			&i.ID,
			&i.Email,
			&i.Lastid,
			&i.Lastname,
			&i.Subject,
			&i.Posts,
			&i.Views,
			&i.Body,
			&i.LastViewPosts,
			&i.Dot,
			&i.Sticky,
			&i.Locked,
			&i.Legendary,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
