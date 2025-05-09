-- name: CreateThread :exec
INSERT INTO thread (subject,member_id,last_member_id) VALUES ($1,$2,$3);

-- name: CreateThreadPost :exec
INSERT INTO
  thread_post
    (thread_id,body,member_id)
  VALUES
    ($1,$2,$3);

-- name: GetThreadSequenceId :one
SELECT currval('thread_id_seq');

-- name: GetThreadPostSequenceId :one
SELECT currval('thread_id_post_seq');

-- name: CreateOrReturnID :one
SELECT id::bigint, is_admin::boolean FROM createOrReturnID($1);

-- name: GetMemberId :one
SELECT id FROM member WHERE email = $1;

-- name: GetMember :one
SELECT
  m.email,
  mp.location,
  m.id,
  mp.bio,
  mp.timezone,
  mp.preferred_name,
  mp.proper_name,
  mp.pronouns,
  m.date_joined,
  mp.photo_url
FROM
  member m
LEFT JOIN
  member_profile mp
ON
  m.id = mp.member_id
WHERE
  m.id = $1;

-- name: ListThreads :many
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
  (CASE WHEN (m.email = $1 AND t.date_posted >= NOW() - INTERVAL '900 seconds') THEN 't' ELSE 'f' END)::boolean as can_edit,
  (CASE WHEN tm.last_view_posts IS null THEN 0 ELSE tm.last_view_posts END) as last_view_posts,
  (CASE WHEN tm.date_posted IS NOT null AND tm.undot IS false AND tm.member_id IS NOT null THEN 't' ELSE 'f' END)::boolean as dot,
  t.sticky,
  t.locked
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
  (tm.member_id=$2 AND tm.thread_id=t.id)
WHERE t.sticky IS false
ORDER BY t.date_last_posted DESC
LIMIT 100;

-- name: ListMemberThreads :many
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
  (CASE WHEN tm.last_view_posts IS null THEN 0 ELSE tm.last_view_posts END) as last_view_posts,
  (CASE WHEN tm.date_posted IS NOT null AND tm.undot IS false AND tm.member_id IS NOT null THEN 't' ELSE 'f' END)::boolean as dot,
  t.sticky,
  t.locked
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
AND m.id=$1
ORDER BY t.date_last_posted DESC
LIMIT 10;

-- name: ListThreadPosts :many
SELECT
  tp.id,
  tp.date_posted,
  m.id as member_id,
  m.email,
  tp.body,
  t.subject,
  t.id as thread_id,
  m.is_admin,
  (CASE WHEN (m.email = $2 AND t.date_posted >= NOW() - INTERVAL '900 seconds') THEN 't' ELSE 'f' END)::boolean as can_edit
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
ORDER BY tp.date_posted ASC;

-- name: GetBoardData :one
SELECT
  id,
  title,
  total_members,
  total_threads,
  total_thread_posts,
  edit_window
FROM board_data;

-- name: GetThreadSubjectById :one
SELECT subject FROM thread WHERE id=$1;

-- name: GetThreadForEdit :one
SELECT m.email AS email,
  t.id AS thread_id,
  t.subject AS subject,
  tp.id AS thread_post_id,
  tp.body AS body
FROM thread t
LEFT JOIN thread_post tp
  ON tp.thread_id=t.id
LEFT JOIN member m
  ON t.member_id=m.id
WHERE t.id=$1 AND m.id=$2;

-- name: GetThreadPostForEdit :one
SELECT tp.id, tp.body
FROM thread_post tp LEFT JOIN member m
  ON tp.member_id=m.id
WHERE tp.id=$1 AND m.id=$2;

-- name: UpdateBoardTitle :exec
UPDATE board_data
SET title=$1;

-- name: UpdateBoardEditWindow :exec
UPDATE board_data
SET edit_window=$1;

-- name: UpdateMemberProfileByID :exec
UPDATE member_profile SET
  photo_url = $2,
  location = $3,
  bio = $4,
  timezone = $5,
  preferred_name = $6,
  pronouns = $7
WHERE member_id = $1;

-- name: UpdateThread :exec
UPDATE thread SET
  subject = $1
WHERE id = $2
  AND member_id = $3
  AND date_posted >= NOW() - INTERVAL '900 seconds';

-- name: UpdateThreadPost :exec
UPDATE thread_post SET
  body = $1
WHERE id = $2
  AND member_id = $3
  AND date_posted >= NOW() - INTERVAL '900 seconds';
