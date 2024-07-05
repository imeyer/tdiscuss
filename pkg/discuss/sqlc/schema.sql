CREATE TABLE board_data
(
  id      serial PRIMARY KEY,
  name    text NOT NULL CHECK(name <> ''),  -- name of variable
  value   text NOT NULL CHECK(value <> ''), -- preference value
  UNIQUE(name)
);
-- INSERT INTO board_data (name,value) VALUES ('total_members',0);
-- INSERT INTO board_data (name,value) VALUES ('total_threads',0);
-- INSERT INTO board_data (name,value) VALUES ('total_thread_posts',0);x


CREATE TABLE member
(
  id                   serial PRIMARY KEY,
  -- name                 varchar(25) NOT NULL CHECK(name <> ''),     -- login name
  -- pass                 char(32) NOT NULL CHECK(pass <> ''),        -- member password md5 hashed
  -- secret               char(32) NOT NULL CHECK(secret <> ''),      -- secret word for password recovery md5 hashed
  -- ip                   cidr NOT NULL,                              -- ip of member at last login
  date_joined          timestamptz DEFAULT now(),                    -- date of signup
  date_first_post       date,                                       -- the date of the member's first post
  email                varchar NOT NULL CHECK(email <> ''),    -- email used to sign up
  -- postalcode           text NOT NULL CHECK(postalcode <> ''),      -- member's postalcode
  total_threads        int DEFAULT 0,                              -- member's total threads created
  total_thread_posts   int DEFAULT 0,                              -- member's total posts
  last_view            timestamp,                                  -- last view of board
  last_post            timestamp,                                  -- last post to board
  last_chat            timestamp,                                  -- last time user chatted
  last_search          timestamp,                                  -- last time user searched
  banned               bool DEFAULT false,                         -- banned user?
  is_admin             bool DEFAULT false,                         -- is admin?
  cookie               char(32),
  session              char(32)
);

CREATE TABLE member_ignore
(
  member_id         int,
  ignore_member_id  int
);

CREATE TABLE member_lurk_unlock
(
  id           serial PRIMARY KEY,
  member_id    int NOT NULL REFERENCES member(id),
  created_at   date NOT NULL DEFAULT now()
);

CREATE TABLE pref_type
(
  id      serial PRIMARY KEY,
  name    text NOT NULL CHECK(name <> ''),
  UNIQUE(name)
);
INSERT INTO pref_type (name) VALUES ('input');
INSERT INTO pref_type (name) VALUES ('checkbox');
INSERT INTO pref_type (name) VALUES ('textarea');

CREATE TABLE pref
(
  id            serial PRIMARY KEY,
  pref_type_id  int NOT NULL REFERENCES pref_type(id),
  name          text NOT NULL CHECK(name <> ''),
  display       text NOT NULL CHECK(display <> ''),
  profile       bool NOT NULL DEFAULT false,
  session       bool NOT NULL DEFAULT false,
  editable      bool NOT NULL DEFAULT true,
  width         int,
  ordering      int,
  UNIQUE(name)
);

-- INSERT INTO pref VALUES (1,1,'photo','photo url',false,false,true,300,1);
-- INSERT INTO pref VALUES (2,1,'location','location',true,false,true,200,2);
-- INSERT INTO pref VALUES (3,1,'email','email',true,false,true,200,3);
-- INSERT INTO pref VALUES (4,1,'website','website',true,false,true,200,4);
-- INSERT INTO pref VALUES (5,1,'aim','aim',true,false,true,NULL,5);
-- INSERT INTO pref VALUES (6,1,'msn','msn',true,false,true,NULL,6);
-- INSERT INTO pref VALUES (7,1,'yahoo','yahoo',true,false,true,NULL,7);
-- INSERT INTO pref VALUES (8,1,'gtalk','gtalk',true,false,true,NULL,8);
-- INSERT INTO pref VALUES (9,1,'jabber','jabber',true,false,true,NULL,9);
-- INSERT INTO pref VALUES (10,3,'info','info',true,false,true,NULL,10);
-- INSERT INTO pref VALUES (11,2,'show_email','show email',false,false,true,NULL,12);
-- INSERT INTO pref VALUES (12,2,'hidemedia','hide media',false,true,true, NULL,13);
-- INSERT INTO pref VALUES (13,2,'ignore','soft ignore',false,true,true,NULL,14);
-- INSERT INTO pref VALUES (14,2,'nocollapse','disable collapsing',false,true,true,NULL,19);
-- INSERT INTO pref VALUES (15,3,'theme','theme',false,true,false,NULL,23);
-- INSERT INTO pref VALUES (17,2,'nofirstpost','hide firstpost arrow',false,true,true,NULL,15);
-- INSERT INTO pref VALUES (18,2,'italicread','italicize read posts',false,true,true,NULL,20);
-- INSERT INTO pref VALUES (19,2,'nopostnumber','hide posts #',false,true,true,NULL,21);
-- INSERT INTO pref VALUES (20,2,'notabs','hide nav tabs',false,true,true,NULL,22);
-- INSERT INTO pref VALUES (21,1,'mincollapse','<span class=''small''>min post # to collapse</span>',false,true,true,50,16);
-- INSERT INTO pref VALUES (22,1,'collapseopen','<span class=''small''># open after collapse (min 1)</span>',false,true,true,50,17);
-- INSERT INTO pref VALUES (23,1,'externalcss','external css<br/><span class=''small''>(may break color schemes)</span>',false,true,true,300,11);
-- INSERT INTO pref VALUES (24,1,'uncollapsecount','<span class=''small''># posts to uncollapse</span>',false,true,true,50,18);


CREATE TABLE member_pref
(
  id             serial PRIMARY KEY,
  pref_id        int NOT NULL,
  member_id      int NOT NULL,
  value          text NOT NULL CHECK(value <> '')
);

CREATE TABLE thread
(
  id                 serial PRIMARY KEY,
  member_id          int NOT NULL,                               -- id of member who created thread
  subject            text NOT NULL CHECK(subject <> ''), -- subject of thread
  date_posted        timestamptz not NULL DEFAULT now(),           -- date thread was created
  first_post_id       int,                                        -- first post id
  posts              int DEFAULT 0,                              -- total posts in a thread
  views              int DEFAULT 0,                              -- total views to thread
  sticky             bool DEFAULT false,                         -- thread sticky flag
  locked             bool DEFAULT false,                         -- thread locked flag
  last_member_id     int NOT NULL,                               -- last member who posted to thread
  date_last_posted   timestamptz NOT NULL DEFAULT now(),           -- time last post was entered
  indexed            bool NOT NULL DEFAULT false,                -- has been indexed: for search indexer
  edited             bool NOT NULL DEFAULT false,                -- has been edited: for search indexer
  deleted            bool NOT NULL DEFAULT false,                -- flagged for deletion: for search indexer
  legendary          bool NOT NULL DEFAULT false
);

CREATE TABLE thread_post
(
  id            serial PRIMARY KEY,
  thread_id     int NOT NULL,                  -- thread post belongs to
  date_posted   timestamptz DEFAULT now(),       -- time this post was created
  member_id     int NOT NULL,                  -- id of member who created post
  indexed       bool NOT NULL DEFAULT false,   -- has been indexed by search indexer
  edited        bool NOT NULL DEFAULT false,   -- has been edited: for search indexer
  deleted       bool NOT NULL DEFAULT false,   -- flagged for deletion: for search indexer
  body          text                           -- body text of post
);

CREATE TABLE thread_member
(
  member_id	        int NOT NULL,
  thread_id	        int NOT NULL,
  undot                 bool NOT NULL DEFAULT false,
  ignore                bool NOT NULL DEFAULT false,
  date_posted           timestamp,
  last_view_posts       int NOT NULL DEFAULT 0
);


CREATE TABLE message
(
  id                 serial PRIMARY KEY,
  member_id          int NOT NULL,                               -- id of member who created message
  subject            varchar(200) NOT NULL CHECK(subject <> ''), -- subject of message
  first_post_id      int,                                        -- first post id for message
  date_posted        timestamp NOT NULL DEFAULT now(),           -- date message was created
  posts              int DEFAULT 0,                              -- total posts in a message
  views              int DEFAULT 0,                              -- total views to message
  last_member_id     int NOT NULL,                               -- last member to reply
  date_last_posted   timestamp NOT NULL DEFAULT now()
);

CREATE TABLE message_post
(
  id            serial PRIMARY KEY,
  message_id    int NOT NULL,            -- message post belongs to
  date_posted   timestamp DEFAULT now(), -- time message post was created
  member_id     int NOT NULL,            -- id of member who created message post
  member_ip     cidr NOT NULL,           -- current ip address of member who created message post
  body          text                     -- body text of message post
);

CREATE TABLE message_member
(
  member_id	        int NOT NULL,
  message_id	      int NOT NULL,
  date_posted       timestamp,
  last_view_posts   int NOT NULL DEFAULT 0,
  deleted           bool NOT NULL DEFAULT false
);

CREATE TABLE favorite
(
  id          serial PRIMARY KEY,
  member_id   int NOT NULL,       -- member who this watched thread belongs to
  thread_id   int NOT NULL        -- thread member is watching
);

CREATE TABLE chat
(
  id         serial PRIMARY KEY,
  member_id  int NOT NULL,
  stamp      timestamp DEFAULT now(),
  chat       text
);

CREATE TABLE theme
(
  id      serial PRIMARY KEY,
  name    text NOT NULL CHECK(name <> ''),
  value   text,
  main    bool NOT NULL DEFAULT false,
  UNIQUE(name)
);

-- INSERT INTO theme (main,name,value) VALUES (true,'blue','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#c3dae4";s:3:"odd";s:7:"#acccdb";s:2:"me";s:7:"#82b3c9";s:5:"hover";s:7:"#82b3c9";s:7:"readbar";s:7:"#3488ab";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('simple','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#ffffff";s:4:"even";s:7:"#cccccc";s:3:"odd";s:7:"#eeeeee";s:2:"me";s:7:"#82b3c9";s:5:"hover";s:7:"#82b3c9";s:7:"readbar";s:7:"#82b3c9";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('gray','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#555555";s:4:"even";s:7:"#d7d7d7";s:3:"odd";s:7:"#c9c9c9";s:2:"me";s:7:"#adadad";s:5:"hover";s:7:"#adadad";s:7:"readbar";s:7:"#333333";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('white','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#ffffff";s:4:"even";s:7:"#cccccc";s:3:"odd";s:7:"#eeeeee";s:2:"me";s:7:"#999999";s:5:"hover";s:7:"#999999";s:7:"readbar";s:7:"#666666";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('black','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#000000";s:4:"even";s:7:"#bbbbbb";s:3:"odd";s:7:"#dddddd";s:2:"me";s:7:"#666666";s:5:"hover";s:7:"#666666";s:7:"readbar";s:7:"#555555";s:4:"menu";s:7:"#000000";}');
-- INSERT INTO theme (name,value) VALUES ('purple','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#bebde9";s:3:"odd";s:7:"#a6a5e1";s:2:"me";s:7:"#7978d2";s:5:"hover";s:7:"#7978d2";s:7:"readbar";s:7:"#5553ae";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('green','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#d4f0be";s:3:"odd";s:7:"#c5eba7";s:2:"me";s:7:"#a8e07a";s:5:"hover";s:7:"#a8e07a";s:7:"readbar";s:7:"#3e8c00";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('orange','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#e0c18b";s:3:"odd";s:7:"#dbb878";s:2:"me";s:7:"#d1a453";s:5:"hover";s:7:"#d1a453";s:7:"readbar";s:7:"#a36d00";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('red','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#a22626";s:3:"odd";s:7:"#ae2929";s:2:"me";s:7:"#7e0101";s:5:"hover";s:7:"#7e0101";s:7:"readbar";s:7:"#111111";s:4:"menu";s:7:"#555555";}');
-- INSERT INTO theme (name,value) VALUES ('halloween','a:9:{s:4:"font";s:37:"verdana, helvetica, arial, sans-serif";s:8:"fontsize";s:3:"1.1";s:4:"body";s:7:"#333333";s:4:"even";s:7:"#eaa61e";s:3:"odd";s:7:"#f4b028";s:2:"me";s:7:"#000000";s:5:"hover";s:7:"#000000";s:7:"readbar";s:7:"#a36d00";s:4:"menu";s:7:"#555555";}');

CREATE TABLE fundraiser
(
  id     serial PRIMARY KEY,
  name   text,
  goal   money
);

CREATE TABLE donation
(
  id              serial PRIMARY KEY,
  fundraiser_id   int NOT NULL REFERENCES fundraiser(id),
  payment_date    date NOT NULL DEFAULT now(),
  payment_status  text,
  payer_email     text,
  txn_id          text,
  payment_fee     money,
  payment_gross   money
);

CREATE OR REPLACE FUNCTION member_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE board_data SET value=(value::integer)-1 WHERE name='total_members';
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE board_data SET value=(value::integer)+1 WHERE name='total_members';
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;


CREATE OR REPLACE FUNCTION thread_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE member SET total_threads=total_threads-1 WHERE id=OLD.member_id;
    UPDATE board_data SET value=(value::integer)-1 WHERE name='total_threads';
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE member SET total_threads=total_threads+1 WHERE id=NEW.member_id;
    UPDATE board_data SET value=(value::integer)+1 WHERE name='total_threads';
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;


CREATE OR REPLACE FUNCTION thread_post_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE member SET total_thread_posts=total_thread_posts-1, last_post=now() WHERE id=OLD.member_id;
    UPDATE board_data SET value=(value::integer)-1 WHERE name='total_thread_posts';
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
    UPDATE board_data SET value=(value::integer)+1 WHERE name='total_thread_posts';
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


CREATE OR REPLACE FUNCTION message_post_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF (SELECT count(*) FROM message_post WHERE message_id=OLD.message_id) > 1 THEN
      UPDATE
        message
      SET
        posts=posts-1,
        first_post_id=(SELECT id FROM message_post WHERE message_id=OLD.message_id ORDER BY date_posted ASC LIMIT 1),
        last_member_id=(SELECT member_id FROM message_post WHERE message_id=OLD.message_id ORDER BY date_posted DESC LIMIT 1),
        date_last_posted=(SELECT date_posted FROM message_post WHERE message_id=OLD. message_id ORDER BY date_posted DESC LIMIT 1)
      WHERE
        id=OLD.message_id;
    ELSEIF (SELECT posts FROM message WHERE id=OLD.message_id) = 1 THEN
      DELETE FROM message_member WHERE message_id=OLD.message_id;
      DELETE FROM message WHERE id=OLD.message_id;
    END IF;
    IF (SELECT count(*) FROM message_post WHERE member_id=OLD.member_id AND message_id=OLD.message_id) = 0 THEN
      DELETE FROM message_member WHERE member_id=OLD.member_id AND message_id=OLD.message_id;
    END IF;
    RETURN OLD;
  ELSEIF TG_OP = 'INSERT' THEN
    UPDATE
      message
    SET
      posts=posts+1,
      first_post_id=(SELECT id FROM message_post WHERE message_id=NEW.message_id ORDER BY date_posted ASC LIMIT 1),
      last_member_id=(SELECT member_id FROM message_post WHERE message_id=NEW.message_id ORDER BY date_posted DESC LIMIT 1),
      date_last_posted=now()
    WHERE
      id=NEW.message_id;
    IF NOT EXISTS (SELECT 1 FROM message_member WHERE member_id=NEW.member_id AND message_id=NEW.message_id) THEN
      INSERT INTO
        message_member (member_id,message_id,date_posted,last_view_posts)
      VALUES
        (NEW.member_id,NEW.message_id,now(),(SELECT posts FROM message WHERE id=NEW.message_id));
    ELSE
      UPDATE
        message_member
      SET
        date_posted=now(),
        last_view_posts=(SELECT posts FROM message WHERE id=NEW.message_id)
      WHERE
        member_id=NEW.member_id
      AND
        message_id=NEW.message_id;
    END IF;
    RETURN NEW;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;


CREATE OR REPLACE FUNCTION message_member_sync() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.deleted IS TRUE THEN
      IF (SELECT count(*) FROM message_member WHERE message_id=OLD.message_id AND deleted IS false) < 1 THEN
        DELETE FROM message_member WHERE message_id=OLD.message_id;
        DELETE FROM message_post WHERE id=OLD.message_id;
      END IF;
    END IF;
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

-- start member
-- CREATE UNIQUE INDEX member_name_lower_index ON member(REPLACE(LOWER(name),' ',''));
CREATE UNIQUE INDEX member_email_lower_index ON member(LOWER(email));
CREATE INDEX member_last_post_index ON member(last_post);
CREATE INDEX member_last_view_index ON member(last_view);

CREATE TRIGGER member_sync AFTER INSERT OR DELETE ON member
  FOR EACH ROW EXECUTE PROCEDURE member_sync();
-- end member


-- start member_pref
ALTER TABLE member_pref ADD FOREIGN KEY (pref_id) REFERENCES pref(id);
ALTER TABLE member_pref ADD FOREIGN KEY (member_id) REFERENCES member(id);
CREATE UNIQUE INDEX mp_mi_pi_index ON member_pref(member_id,pref_id);
-- end member_pref


--start member_ignore
CREATE UNIQUE INDEX mi_mi_imi_index ON member_ignore(member_id,ignore_member_id);
ALTER TABLE member_ignore ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE member_ignore ADD FOREIGN KEY (ignore_member_id) REFERENCES member(id);
-- end member_ignore


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
CREATE INDEX thread_member_member_id_ignore ON thread_member(member_id,ignore);

ALTER TABLE thread_member ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE thread_member ADD FOREIGN KEY (thread_id) REFERENCES thread(id);
-- end thread_member


-- start message
CREATE INDEX message_member_id_index ON message(member_id);
CREATE INDEX message_date_last_posted_index ON message(date_last_posted);
CLUSTER message_date_last_posted_index ON message;

ALTER TABLE message ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE message ADD FOREIGN KEY (last_member_id) REFERENCES member(id);
-- end message


-- start message_member
CREATE UNIQUE INDEX mm_mi_mi_lvr ON message_member(member_id,message_id,last_view_posts);

ALTER TABLE message_member ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE message_member ADD FOREIGN KEY (message_id) REFERENCES message(id);

CREATE TRIGGER message_post_sync AFTER INSERT OR DELETE OR UPDATE ON message_member
  FOR EACH ROW EXECUTE PROCEDURE message_member_sync();
-- end message_member


-- start message_post
CREATE INDEX message_post_member_id_index ON message_post(member_id);
CREATE INDEX message_post_message_id_index ON message_post(message_id);
CREATE INDEX message_post_date_posted_index ON message_post(date_posted);
CREATE INDEX message_post_member_ip ON message_post(member_ip);

ALTER TABLE message_post ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE message_post ADD FOREIGN KEY (message_id) REFERENCES message(id);

CREATE TRIGGER message_post_sync AFTER INSERT OR DELETE ON message_post
  FOR EACH ROW EXECUTE PROCEDURE message_post_sync();
-- end message_post


-- start favorites
CREATE INDEX favorite_member_id_thread_id_index ON favorite(member_id,thread_id);

ALTER TABLE favorite ADD FOREIGN KEY (member_id) REFERENCES member(id);
ALTER TABLE favorite ADD FOREIGN KEY (thread_id) REFERENCES thread(id);
-- end favorites


-- start chat
CREATE INDEX chat_stamp_index ON CHAT(stamp);

ALTER TABLE chat ADD FOREIGN KEY (member_id) REFERENCES member(id);
-- end chat
