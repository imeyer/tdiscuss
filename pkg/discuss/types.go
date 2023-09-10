package discuss

import "time"

type Topic struct {
	ID        int
	User      string
	Topic     string
	Body      string
	CreatedAt time.Time
}

type Post struct {
	ID        int
	Body      string
	CreatedAt time.Time
	User      string
}
