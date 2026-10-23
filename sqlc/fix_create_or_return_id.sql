-- Make createOrReturnID safe to call concurrently.
--
-- The old body did SELECT then INSERT with no conflict handling. Two requests
-- from the same login arriving at once - which is what a first page load does,
-- since the page and its assets each authenticate - both found no member and
-- both inserted, so the loser raised 23505 and got a 500.
--
-- The new body inserts with ON CONFLICT DO NOTHING and re-reads on a miss, and
-- creates the profile idempotently. It needs member_profile_member_id_index to
-- be UNIQUE for that last part to mean anything: an untargeted
-- ON CONFLICT DO NOTHING only skips on a real violation, so without the
-- constraint two concurrent callers would each add a profile row.
--
-- Also matches the member on LOWER(email), the way member_email_lower_index
-- enforces uniqueness. Matching the raw column let a login whose case differed
-- from the stored row miss it and then fail the insert.
--
-- Safe to apply to a running database, and safe to apply more than once.
--
--   psql -U tdiscuss -d tdiscuss -f sqlc/fix_create_or_return_id.sql

BEGIN;

-- A profile is one-per-member. If duplicates somehow exist they have to go
-- before the unique index can be created; the oldest row wins, since that is
-- the one the member has been editing.
DELETE FROM member_profile mp
USING member_profile older
WHERE mp.member_id = older.member_id
  AND mp.id > older.id;

DROP INDEX IF EXISTS member_profile_member_id_index;
CREATE UNIQUE INDEX member_profile_member_id_index ON member_profile(member_id);

CREATE OR REPLACE FUNCTION createOrReturnID(p_email VARCHAR(255))
RETURNS TABLE (id BIGINT, is_admin BOOLEAN, is_blocked BOOLEAN) AS $$
DECLARE
    v_id BIGINT;
BEGIN
    -- Match on LOWER(email), the same thing member_email_lower_index enforces
    -- unique. Comparing the raw column here would let "Someone@Example.com"
    -- slip past an existing "someone@example.com" and fail on the insert.
    SELECT member.id INTO v_id
    FROM member
    WHERE LOWER(member.email) = LOWER(p_email)
    ORDER BY member.id
    LIMIT 1;

    -- New members get no privileges. Admin is granted either by a capability
    -- grant in the tailnet policy file (see the README) or by setting
    -- member.is_admin directly; it is never inferred from signup order.
    --
    -- Concurrent first requests from one login are ordinary: a first page
    -- load fires several requests at once. ON CONFLICT makes the loser of
    -- that race skip the insert instead of raising 23505, and RETURNING then
    -- yields no row, which is what the second lookup is for. PostgreSQL
    -- waits for the winning transaction before deciding, so the row it
    -- reads is committed.
    IF v_id IS NULL THEN
        INSERT INTO member (email, is_admin, is_blocked)
        VALUES (p_email, false, false)
        ON CONFLICT DO NOTHING
        RETURNING member.id INTO v_id;

        IF v_id IS NULL THEN
            SELECT member.id INTO v_id
            FROM member
            WHERE LOWER(member.email) = LOWER(p_email)
            ORDER BY member.id
            LIMIT 1;
        END IF;
    END IF;

    IF v_id IS NULL THEN
        RAISE EXCEPTION 'createOrReturnID: no member row for % after insert', p_email;
    END IF;

    -- The profile is created once; member_profile_member_id_index is unique
    -- so a concurrent caller cannot add a second one.
    INSERT INTO member_profile (member_id)
    VALUES (v_id)
    ON CONFLICT DO NOTHING;

    RETURN QUERY
    SELECT member.id, COALESCE(member.is_admin, false), COALESCE(member.is_blocked, false)
    FROM member
    WHERE member.id = v_id;
END;
$$ LANGUAGE plpgsql;

COMMIT;
