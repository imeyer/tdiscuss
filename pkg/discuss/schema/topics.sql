CREATE TABLE IF NOT EXISTS topics(
    ID integer PRIMARY KEY,
    User TEXT NOT NULL DEFAULT "",
    Topic text NOT NULL DEFAULT "",
    Body text NOT NULL DEFAULT "",
    CreatedAt integer NOT NULL DEFAULT (strftime('%s', 'now'))
);
