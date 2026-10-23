-- Remove the auto-admin bootstrap from createOrReturnID.
--
-- The old function made the first member to ever sign in an admin, because at
-- the time that was the only supported way to get one. Admin is now granted by
-- a capability grant in the tailnet policy file (see the README), so privilege
-- no longer depends on who happens to arrive first - which in practice was as
-- likely to be a health check or a link preview fetcher as a person.
--
-- This also drops the RAISE NOTICE debug statements, which fired on every
-- authenticated request and wrote member identifiers to the PostgreSQL log.
--
-- Safe to apply to a running database, and safe to apply more than once:
-- CREATE OR REPLACE FUNCTION replaces the body in place. Existing admins are
-- unaffected - this only changes what happens to *new* members, and the old
-- auto-admin branch could never fire again once any member existed.
--
--   psql -U tdiscuss -d tdiscuss -f sqlc/remove_auto_admin_bootstrap.sql
--
-- The body below is the same one schema.sql carries, including the concurrent
-- first-login fix from fix_create_or_return_id.sql. It has to be: this file
-- replaces the whole function, so leaving the old body here would undo that
-- fix for anyone who applies this file afterwards. The one thing it does not
-- do is create the unique index on member_profile.member_id; run
-- fix_create_or_return_id.sql as well (in either order) to get that.
--
-- To grant admin without a capability grant:
--
--   UPDATE member SET is_admin = true WHERE email = 'you@example.com';

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
