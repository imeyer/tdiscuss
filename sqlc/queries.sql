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
SELECT createOrReturnID($1);

-- name: GetMemberId :one
SELECT id FROM member WHERE email = $1;

-- name: GetMember :one
SELECT email, location, id, date_joined, photo_url FROM member WHERE id = $1;

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
  (CASE WHEN tm.last_view_posts IS null THEN 0 ELSE tm.last_view_posts END) as last_view_posts,
  (CASE WHEN tm.date_posted IS NOT null AND tm.undot IS false AND tm.member_id IS NOT null THEN 't' ELSE 'f' END) as dot,
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
  (CASE WHEN tm.date_posted IS NOT null AND tm.undot IS false AND tm.member_id IS NOT null THEN 't' ELSE 'f' END) as dot,
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
ORDER BY tp.date_posted ASC;

-- name: GetThreadSubjectById :one
SELECT subject FROM thread WHERE id=$1;

-- name: UpdateMemberByEmail :exec
UPDATE member SET
  photo_url = $2,
  location = $3
WHERE email = $1;
