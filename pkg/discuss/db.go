package discuss

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteDB struct {
	db *sql.DB
	mu sync.RWMutex
}

//go:embed schema.sql
var sqlSchema string

func NewSQLiteDB(f string) (*SQLiteDB, error) {
	db, err := sql.Open("sqlite", f)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	if _, err = db.Exec(sqlSchema); err != nil {
		return nil, err
	}

	return &SQLiteDB{db: db}, nil
}

func (s *SQLiteDB) LoadTopics(ctx context.Context) ([]*Topic, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var topics []*Topic
	rows, err := s.db.Query("SELECT ID, User, Topic, Body, CreatedAt FROM Topics")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		topic := new(Topic)
		var createdAt int64
		err := rows.Scan(&topic.ID, &topic.User, &topic.Topic, &topic.Body, &createdAt)
		if err != nil {
			return nil, err
		}
		topic.CreatedAt = time.Unix(createdAt, 0).UTC()
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

func (s *SQLiteDB) SaveTopic(ctx context.Context, topic *Topic) (tid int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec("INSERT OR REPLACE INTO Topics (Topic, Body, User, CreatedAt) VALUES (?, ?, ?, ?)", topic.Topic, topic.Body, topic.User, topic.CreatedAt.Unix())
	if err != nil {
		return 0, err
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
	return lid, nil
}

func (s *SQLiteDB) Ping(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.db.PingContext(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}
