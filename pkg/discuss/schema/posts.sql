CREATE TABLE IF NOT EXISTS posts(
    ID integer PRIMARY KEY,
    TopicID integer NOT NULL,
    CreatedAt integer NOT NULL DEFAULT (strftime('%s', 'now')),
    Body text NOT NULL DEFAULT "",
    User TEXT NOT NULL DEFAULT ""
);
