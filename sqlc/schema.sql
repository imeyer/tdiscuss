CREATE TABLE board_data
(
  id      serial PRIMARY KEY,                 -- id
  title  varchar NOT NULL CHECK(title <> ''), -- title of board
  allow_editing boolean DEFAULT false,        -- allow editing of posts
  allow_deleting boolean DEFAULT false,       -- allow deleting of posts
  edit_window int DEFAULT 0,                  -- time in seconds to allow editing of posts
  total_members int DEFAULT 0,                -- total members
  total_threads int DEFAULT 0,                -- total threads
  total_thread_posts int DEFAULT 0            -- total posts in threads
);

INSERT INTO board_data (title, edit_window) VALUES ('My Board', 900);

CREATE TABLE member
(
  cookie               char(32),
  date_joined          timestamptz DEFAULT now(),           -- date of signup
  email                varchar NOT NULL CHECK(email <> ''), -- email used to sign up
  id                   bigserial UNIQUE PRIMARY KEY,        -- id
  is_admin             boolean DEFAULT false,               -- is admin?
  last_post            timestamp,                           -- last post to board
  last_view            timestamp,                           -- last view of board
  total_thread_posts   int DEFAULT 0,                       -- member's total posts
  total_threads        int DEFAULT 0                        -- member's total threads created
);

CREATE TABLE member_profile
(
  id                   bigserial UNIQUE PRIMARY KEY, -- id
  member_id            bigint NOT NULL,              -- id of member
  location             varchar,                      -- location of member
  pronouns             varchar,                      -- pronouns of member
  preferred_name       varchar,                      -- preferred name of member
  proper_name          varchar,                      -- proper name of member
  photo_url            varchar,                      -- url to the users photo
  timezone             varchar,                      -- timezone of member
  bio                  text                          -- bio of member

);

CREATE TABLE thread
(
  id                 bigserial UNIQUE PRIMARY KEY,
  member_id          bigint NOT NULL,                     -- id of member who created thread
  subject            text NOT NULL CHECK(subject <> ''),  -- subject of thread
  date_posted        timestamptz not NULL DEFAULT now(),  -- date thread was created
  first_post_id       int,                                -- first post id
  posts              int DEFAULT 0,                       -- total posts in a thread
  views              int DEFAULT 0,                       -- total views to thread
  sticky             boolean DEFAULT false,               -- thread sticky flag
  locked             boolean DEFAULT false,               -- thread locked flag
  last_member_id     bigint NOT NULL,                     -- last member who posted to thread
  date_last_posted   timestamptz NOT NULL DEFAULT now(),  -- time last post was entered
  indexed            bool NOT NULL DEFAULT false,         -- has been indexed: for search indexer
  edited             bool NOT NULL DEFAULT false,         -- has been edited: for search indexer
  deleted            bool NOT NULL DEFAULT false          -- flagged for deletion: for search indexer
);

CREATE TABLE thread_post
(
  id            bigserial UNIQUE PRIMARY KEY, -- id of post
  thread_id     bigint NOT NULL,              -- thread post belongs to
  date_posted   timestamptz DEFAULT now(),    -- time this post was created
  member_id     bigint NOT NULL,              -- id of member who created post
  indexed       bool NOT NULL DEFAULT false,  -- has been indexed by search indexer
  edited        bool NOT NULL DEFAULT false,  -- has been edited: for search indexer
  deleted       bool NOT NULL DEFAULT false,  -- flagged for deletion: for search indexer
  body          text                          -- body text of post
);

CREATE TABLE thread_member
(
  member_id	            bigint NOT NULL,
  thread_id	            bigint NOT NULL,
  -- dot is a visual cue for "I have participated in this thread", undot removes the visual participation cue
  undot                 bool NOT NULL DEFAULT false,
  date_posted           timestamp,
  last_view_posts       int NOT NULL DEFAULT 0
);

CREATE OR REPLACE FUNCTION member_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE board_data SET total_members=(total_members::integer)-1;
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE board_data SET total_members=(total_members::integer)+1;
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION thread_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE member SET total_threads=total_threads-1 WHERE id=OLD.member_id;
    UPDATE board_data SET total_threads=(total_threads::integer)-1;
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE member SET total_threads=total_threads+1 WHERE id=NEW.member_id;
    UPDATE board_data SET total_threads=(total_threads::integer)+1;
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION thread_post_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE member SET total_thread_posts=total_thread_posts-1, last_post=now() WHERE id=OLD.member_id;
    UPDATE board_data SET total_thread_posts=(total_thread_posts::integer)-1;
    IF (SELECT count(*) FROM thread_post WHERE thread_id=OLD.thread_id) > 1 THEN
      UPDATE
        thread
      SET
        posts=posts-1,
        first_post_id=(SELECT id FROM thread_post WHERE thread_id=OLD.thread_id ORDER BY date_posted ASC LIMIT 1),
        last_member_id=(SELECT member_id FROM thread_post WHERE thread_id=OLD.thread_id ORDER BY date_posted DESC LIMIT 1),
        date_last_posted=(SELECT date_posted FROM thread_post WHERE thread_id=OLD.thread_id ORDER BY date_posted DESC LIMIT 1)
      WHERE
        id=OLD.thread_id;
    ELSEIF (SELECT posts FROM thread WHERE id=OLD.thread_id) = 1 THEN
      DELETE FROM thread_member WHERE thread_id=OLD.thread_id;
      DELETE FROM favorite WHERE thread_id=OLD.thread_id;
      DELETE FROM thread WHERE id=OLD.thread_id;
    END IF;
    IF (SELECT count(*) FROM thread_post WHERE member_id=OLD.member_id AND thread_id=OLD.thread_id) = 0 THEN
      DELETE FROM thread_member WHERE member_id=OLD.member_id AND thread_id=OLD.thread_id;
    END IF;
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE member SET last_post=now() WHERE id=NEW.member_id;
    UPDATE member SET total_thread_posts=total_thread_posts+1 WHERE id=NEW.member_id;
    UPDATE board_data SET total_thread_posts=(total_thread_posts::integer)+1;
    UPDATE
      thread
    SET
      posts=posts+1,
      first_post_id=(SELECT id FROM thread_post WHERE thread_id=NEW.thread_id ORDER BY date_posted ASC LIMIT 1),
      last_member_id=(SELECT member_id FROM thread_post WHERE thread_id=NEW.thread_id ORDER BY date_posted DESC LIMIT 1),
      date_last_posted=now()
    WHERE
      id=NEW.thread_id;
    IF NOT EXISTS (SELECT 1 FROM thread_member WHERE member_id=NEW.member_id AND thread_id=NEW.thread_id) THEN
      INSERT INTO
        thread_member (member_id,thread_id,date_posted,last_view_posts)
      VALUES
        (NEW.member_id,NEW.thread_id,now(),(SELECT posts FROM thread WHERE id=NEW.thread_id));
    ELSE
      UPDATE
        thread_member
      SET
        date_posted=now(),
        last_view_posts=(SELECT posts FROM thread WHERE id=NEW.thread_id)
      WHERE
        member_id=NEW.member_id
      AND
        thread_id=NEW.thread_id;
    END IF;
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION join(varchar,anyarray) RETURNS varchar AS $$
DECLARE
  sep ALIAS FOR $1;
  arr ALIAS FOR $2;
  buf varchar;
BEGIN
  buf := '';

  FOR i IN COALESCE(array_lower(arr,1),0)..COALESCE(array_upper(arr,1),-1) LOOP
    buf := buf || arr[i];
    IF i < array_upper(arr, 1) THEN
      buf := buf || sep;
    END IF;
  END LOOP;

  RETURN buf;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION indexOf(anyelement,anyarray) RETURNS integer AS $$
DECLARE
  search ALIAS FOR $1;
  arr ALIAS FOR $2;
BEGIN
  FOR i IN COALESCE(array_lower(arr,1),0)..COALESCE(array_upper(arr,1),-1) LOOP
    IF arr[i] = search THEN
      RETURN i;
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION createOrReturnID(p_email VARCHAR(255))
RETURNS TABLE (id BIGINT, is_admin BOOLEAN) AS $$
DECLARE
    v_id BIGINT;
    v_is_admin BOOLEAN;
    v_member_count INTEGER;
BEGIN
    v_is_admin := false;

    -- If there are no members, make this member an admin
    SELECT count(member.id) INTO v_member_count
    FROM member;

    RAISE NOTICE 'initial v_member_count: %', v_member_count;

    -- Try to find the existing email
    SELECT member.id, COALESCE(member.is_admin, false) INTO v_id, v_is_admin
    FROM member
    WHERE member.email = p_email;

    RAISE NOTICE 'After SELECT: v_id = %, v_is_admin = %', v_id, v_is_admin;

    IF v_member_count = 0 THEN
        v_is_admin = true;
    ELSE

    END IF;

    -- If the email doesn't exist, create a new record
    IF v_id IS NULL THEN
        INSERT INTO member (email, is_admin)
        VALUES (p_email, v_is_admin)
        RETURNING member.id, COALESCE(member.is_admin, false) INTO v_id, v_is_admin;
        RAISE NOTICE 'After INSERT: v_id = %, v_is_admin = %', v_id, v_is_admin;

        INSERT INTO member_profile (member_id)
        VALUES (v_id);
    END IF;

    -- Return the ID (either existing or newly created)
    RETURN QUERY SELECT v_id, v_is_admin;
END;
$$ LANGUAGE plpgsql;

-- start member
CREATE UNIQUE INDEX member_email_lower_index ON member(LOWER(email));
CREATE UNIQUE INDEX member_email_index ON member(email);
CREATE INDEX member_last_post_index ON member(last_post);
CREATE INDEX member_last_view_index ON member(last_view);

CREATE TRIGGER member_sync AFTER INSERT OR DELETE ON member
  FOR EACH ROW EXECUTE PROCEDURE member_sync();
-- end member

-- start member_profile
ALTER TABLE member_profile ADD FOREIGN KEY (member_id) REFERENCES member(id);
CREATE INDEX member_profile_member_id_index ON member_profile(member_id);
-- end member_profile

-- start thread
CREATE INDEX thread_member_id_index ON thread(member_id);
CREATE INDEX thread_sticky_index ON thread(sticky);
CREATE INDEX thread_date_last_posted_index ON thread(date_last_posted);
CREATE INDEX thread_indexed_index ON thread(indexed);
CREATE INDEX thread_edited_index ON thread(edited);
CREATE INDEX thread_deleted_index ON thread(deleted);
CLUSTER thread_date_last_posted_index ON thread;

ALTER TABLE thread ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE thread ADD FOREIGN KEY (last_member_id) REFERENCES member(id);

CREATE TRIGGER thread_sync AFTER INSERT OR DELETE ON thread
  FOR EACH ROW EXECUTE PROCEDURE thread_sync();
-- end thread

-- start thread_post
CREATE INDEX thread_post_member_id_index ON thread_post(member_id);
CREATE INDEX thread_post_thread_id_index ON thread_post(thread_id);
CREATE INDEX thread_post_date_posted_index ON thread_post(date_posted);
CREATE INDEX thread_post_indexed_index ON thread_post(indexed);
CREATE INDEX thread_post_edited_index ON thread_post(edited);
CREATE INDEX thread_post_deleted_index ON thread_post(deleted);
CREATE INDEX thread_post_thread_id_date_posted_index ON thread_post(thread_id, date_posted);
ALTER TABLE thread_post ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE thread_post ADD FOREIGN KEY (thread_id) REFERENCES thread(id);

CREATE TRIGGER thread_post_sync AFTER INSERT OR DELETE ON thread_post
  FOR EACH ROW EXECUTE PROCEDURE thread_post_sync();
-- end thread_post

-- start thread_member
CREATE UNIQUE INDEX tm_mi_mi_lvr ON thread_member(member_id,thread_id,last_view_posts);
CREATE INDEX thread_member_member_id_date_posted ON thread_member(member_id,date_posted);

ALTER TABLE thread_member ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE thread_member ADD FOREIGN KEY (thread_id) REFERENCES thread(id);
-- end thread_member
