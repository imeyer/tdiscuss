package discuss

import "time"

type Topic struct {
	ID        int64     `db:"ID"`
	User      string    `db:"User"`
	Topic     string    `db:"Topic"`
	Body      string    `db:"Body"`
	CreatedAt time.Time `db:"CreatedAt"`
	Posts     []Post
}

type Post struct {
	ID        int64     `db:"ID"`
	User      string    `db:"User"`
	Topic     string    `db:"Topic"`
	Body      string    `db:"Body"`
	TopicID   int64     `db:"TopicID"`
	CreatedAt time.Time `db:"CreatedAt"`
}

type TopicPost struct {
	ID        int64     `db:"ID"`
	Topic     string    `db:"Topic"`
	TopicID   int64     `db:"TopicID"`
	User      string    `db:"User"`
	Body      string    `db:"Body"`
	CreatedAt time.Time `db:"CreatedAt"`
}
