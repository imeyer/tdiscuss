package discuss

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadTopics(t *testing.T) {
	ctx := context.Background()

	l := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))

	db, err := NewSQLiteDB(":memory:", l)
	assert.Nil(t, err)

	db.logger = l

	ct1 := time.Now().UTC().Unix()
	time.Sleep(1 * time.Second)
	ct2 := time.Now().UTC().Unix()

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

	assert.Equal(t, topics[1].Topic, got[1].Topic, fmt.Sprintf("Topic[1]: have (%v), want (%v)\n", topics[1].Topic, got[1].Topic))
	assert.Equal(t, topics[1].CreatedAt.UTC(), got[1].CreatedAt, fmt.Sprintf("CreatedAt[1]: have (%v), want (%v)\n", topics[1].CreatedAt.UTC(), got[1].CreatedAt))
	assert.Equal(t, topics[0].CreatedAt.UTC(), got[0].CreatedAt, fmt.Sprintf("CreatedAt[0]: have (%v), want (%v)\n", topics[0].CreatedAt.UTC(), got[0].CreatedAt))
}

func TestLoadTopic(t *testing.T) {
	ctx := context.Background()

	l := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))

	db, err := NewSQLiteDB(":memory:", l)
	assert.Nil(t, err)

	db.logger = l

	ct1 := time.Now().UTC().Unix()

	topic := &Topic{
		User:      "test@example.com",
		Topic:     "topic1",
		Body:      "body1",
		CreatedAt: time.Unix(ct1, 0),
	}

	topicID, err := db.SaveTopic(ctx, topic)
	assert.Nil(t, err)

	row, err := db.LoadTopic(ctx, topicID)
	if err != nil {
		t.Fatal(err)
	}

	assert.NotNil(t, row, row)

	assert.Equal(t, topic.Body, row[0].Body, fmt.Sprintf("have (%v), want (%v)", row[0].Body, topic.Body))
	assert.Equal(t, topic.CreatedAt.UTC(), row[0].CreatedAt, fmt.Sprintf("have (%v), want (%v)", row[0].CreatedAt, topic.CreatedAt))
}

func TestSaveTopics(t *testing.T) {
	l := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))

	db, err := NewSQLiteDB(":memory:", l)
	assert.Nil(t, err)

	db.logger = l

	topic := &Topic{User: "test@test.com", Topic: "Topic1", Body: "Body1", CreatedAt: time.Now()}

	ctx := context.Background()

	topicID, err := db.SaveTopic(ctx, topic)
	assert.Nil(t, err)

	var wantid int64 = 1
	assert.Equal(t, wantid, topicID, fmt.Sprintf("have (%v), want (%v)", topicID, wantid))
}
