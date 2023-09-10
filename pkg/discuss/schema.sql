CREATE TABLE
    IF NOT EXISTS Topics (
        ID INTEGER PRIMARY KEY,
        User TEXT NOT NULL DEFAULT "",
        Topic TEXT NOT NULL DEFAULT "",
        Body TEXT NOT NULL DEFAULT "",
        CreatedAt INTEGER NOT NULL DEFAULT (strftime ('%s', 'now'))
    );

CREATE TABLE
    IF NOT EXISTS Posts (
        ID INTEGER PRIMARY KEY,
        TopicID INTEGER NOT NULL,
        CreatedAt INTEGER NOT NULL DEFAULT (strftime ('%s', 'now')),
        Body TEXT NOT NULL DEFAULT "",
        User TEXT NOT NULL DEFAULT ""
    );
