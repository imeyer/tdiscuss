package discuss

import (
	"context"
	"database/sql"
	_ "embed"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadTopics(t *testing.T) {
	db, err := NewSQLiteDB(":memory:")
	assert.Nil(t, err)

	// stable timestamps
	ct1 := time.Now().UTC().Unix()
	time.Sleep(2 * time.Second)
	ct2 := time.Now().UTC().Unix()

	ctx := context.Background()

	topics := []*Topic{
		{
			User:      "test@example.com",
			Topic:     "topic1",
			Body:      "body1",
			CreatedAt: time.Unix(ct1, 0),
		},
		{
			User:      "test2@example.com",
			Topic:     "topic2",
			Body:      "body2",
			CreatedAt: time.Unix(ct2, 0),
		},
	}

	for _, topic := range topics {
		topicID, err := db.SaveTopic(ctx, topic)
		assert.Nil(t, err)
		assert.NotEqual(t, 0, topicID, "Last ID should not be zero")
	}

	got, err := db.LoadTopics(ctx)
	assert.Nil(t, err)

	assert.Equal(t, topics[1].Topic, got[1].Topic, "topic2: Topics do not match")
	assert.Equal(t, topics[1].CreatedAt.UTC(), got[1].CreatedAt, "topic2: Timestamps do not match")
	assert.Equal(t, topics[0].CreatedAt.UTC(), got[0].CreatedAt, "topic1: Timestamps do not match")
}

func TestSaveTopics(t *testing.T) {
	db, err := NewSQLiteDB(":memory:")
	assert.Nil(t, err)

	topic := &Topic{1, "test@test.com", "Topic1", "Body1", time.Now()}

	ctx := context.Background()

	topicID, err := db.SaveTopic(ctx, topic)
	assert.Nil(t, err)
	assert.NotEqual(t, 0, topicID, "Topic ID should not be 0")
}

func TestSQLiteDB_Ping(t *testing.T) {
	db, err := NewSQLiteDB(":memory:")
	assert.Nil(t, err)

	type fields struct {
		db *sql.DB
	}
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{"TestPing", fields{db: db.db}, args{context.Background()}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SQLiteDB{
				db: tt.fields.db,
			}
			got, err := s.Ping(tt.args.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("SQLiteDB.Ping() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("SQLiteDB.Ping() = %v, want %v", got, tt.want)
			}
		})
	}
}
