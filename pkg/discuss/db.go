package discuss

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteDB struct {
	db     *sql.DB
	logger *slog.Logger
}

//go:embed schema/topics.sql
var topicsSchema string

//go:embed schema/posts.sql
var postsSchema string

func NewSQLiteDB(f string, logger *slog.Logger) (*SQLiteDB, error) {
	logger.Debug("connecting to database", "database", f)
	db, err := sql.Open("sqlite", f)
	if err != nil {
		return nil, err
	}

	topics, err := db.Exec(topicsSchema)
	if err != nil {
		logger.Debug(err.Error())
		return nil, err
	}
	logger.Debug("table created", "table", topics)

	posts, err := db.Exec(postsSchema)
	if err != nil {
		logger.Debug(err.Error())
		return nil, err
	}
	logger.Debug("table created", "table", posts)

	return &SQLiteDB{db: db, logger: logger}, nil
}

func (s *SQLiteDB) LoadTopics(ctx context.Context) ([]*Topic, error) {
	var topics []*Topic

	rows, err := s.db.QueryContext(ctx, "SELECT ID, User, Topic, Body, CreatedAt FROM Topics")
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var createdAt int64

		topic := new(Topic)

		err := rows.Scan(&topic.ID, &topic.User, &topic.Topic, &topic.Body, &createdAt)
		if err != nil {
			return nil, err
		}

		topic.CreatedAt = time.Unix(createdAt, 0).UTC()

		topics = append(topics, topic)
	}

	return topics, rows.Err()
}

func (s *SQLiteDB) LoadTopic(ctx context.Context, id int64) ([]*Post, error) {
	var posts []*Post

	s.logger.DebugContext(ctx, "LoadTopic()", "query", fmt.Sprintf("SELECT User, TopicID, Body, CreatedAt FROM Posts WHERE TopicID = %d ORDER BY CreatedAt DESC", id))

	rows, err := s.db.QueryContext(ctx, "SELECT User, TopicID, Body, CreatedAt FROM Posts WHERE TopicID = ? ORDER BY CreatedAt DESC", id)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var createdAt int64

		post := new(Post)

		err := rows.Scan(&post.User, &post.ID, &post.Body, &createdAt)
		if err != nil {
			return nil, err
		}

		post.CreatedAt = time.Unix(createdAt, 0).UTC()

		posts = append(posts, post)
	}

	return posts, rows.Err()
}

func (s *SQLiteDB) SaveTopic(ctx context.Context, topic *Topic) (tid int64, err error) {
	result, err := s.db.ExecContext(ctx, "INSERT OR REPLACE INTO topics (Topic, Body, User, CreatedAt) VALUES (?, ?, ?, ?)", topic.Topic, topic.Body, topic.User, topic.CreatedAt.Unix())
	if err != nil {
		s.logger.ErrorContext(ctx, fmt.Sprintf("commit failed: %v\n", err))
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if rows != 1 {
		return 0, fmt.Errorf("expected to add 1 row, added %d instead", rows)
	}

	lid, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	topic.ID = lid

	s.logger.DebugContext(ctx, "sending topic to SavePost()", "topic_id", topic.ID, "user", topic.User)

	_, err = s.SavePost(ctx, topic)
	if err != nil {
		return 0, fmt.Errorf("post did not save: %v", err)
	}

	return lid, nil
}

func (s *SQLiteDB) SavePost(ctx context.Context, topic *Topic) (postid int64, err error) {
	result, err := s.db.ExecContext(ctx, "INSERT INTO posts (TopicID, Body, User, CreatedAt) VALUES (?, ?, ?, ?)", topic.ID, topic.Body, topic.User, topic.CreatedAt.Unix())
	if err != nil {
		s.logger.ErrorContext(ctx, err.Error())
		return 0, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if rows != 1 {
		return 0, fmt.Errorf("expected to add 1 row, added %d instead", rows)
	}

	postid, err = result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return postid, nil
}
