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
-- To grant admin without a capability grant:
--
--   UPDATE member SET is_admin = true WHERE email = 'you@example.com';

CREATE OR REPLACE FUNCTION createOrReturnID(p_email VARCHAR(255))
RETURNS TABLE (id BIGINT, is_admin BOOLEAN, is_blocked BOOLEAN) AS $$
DECLARE
    v_id BIGINT;
    v_is_admin BOOLEAN;
    v_is_blocked BOOLEAN;
BEGIN
    -- New members get no privileges. Admin is granted either by a capability
    -- grant in the tailnet policy file (see the README) or by setting
    -- member.is_admin directly; it is never inferred from signup order.
    v_is_admin := false;
    v_is_blocked := false;

    -- Try to find the existing email
    SELECT member.id, COALESCE(member.is_admin, false), COALESCE(member.is_blocked, false) INTO v_id, v_is_admin, v_is_blocked
    FROM member
    WHERE member.email = p_email;

    -- If the email doesn't exist, create a new record
    IF v_id IS NULL THEN
        INSERT INTO member (email, is_admin, is_blocked)
        VALUES (p_email, false, false)
        RETURNING member.id, COALESCE(member.is_admin, false), COALESCE(member.is_blocked, false) INTO v_id, v_is_admin, v_is_blocked;

        INSERT INTO member_profile (member_id)
        VALUES (v_id);
    END IF;

    -- Return the ID (either existing or newly created)
    RETURN QUERY SELECT v_id, v_is_admin, v_is_blocked;
END;
$$ LANGUAGE plpgsql;
