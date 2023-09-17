package discuss

import "time"

type Topic struct {
	ID        int64     `db:"ID"`
	User      string    `db:"User"`
	Topic     string    `db:"Topic"`
	Body      string    `db:"Body"`
	CreatedAt time.Time `db:"CreatedAt"`
}

type Post struct {
	ID        int64     `db:"ID"`
	Body      string    `db:"Body"`
	CreatedAt time.Time `db:"CreatedAt"`
	User      string    `db:"User"`
}
