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
	if err != nil {
		t.Fatal(err)
	}

	// stable timestamps
	ct1 := time.Now()
	time.Sleep(2 * time.Second)
	ct2 := time.Now()

	ctx := context.Background()

	topics := []*Topic{
		{
			User:      "test@example.com",
			Topic:     "topic1",
			Body:      "body1",
			CreatedAt: ct1,
		},
		{
			User:      "test2@example.com",
			Topic:     "topic2",
			Body:      "body2",
			CreatedAt: ct2,
		},
	}

	for _, topic := range topics {
		if err := db.SaveTopic(ctx, topic); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.LoadTopics(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, topics[1].Topic, got[1].Topic, "topic1: Topics do not match")
}

func TestSaveTopics(t *testing.T) {
	db, err := NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	topic := &Topic{1, "test@test.com", "Topic1", "Body1", time.Now()}

	ctx := context.Background()

	err = db.SaveTopic(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDB_Ping(t *testing.T) {
	db, err := NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}

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
