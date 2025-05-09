// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.28.0

package main

import (
	"github.com/jackc/pgx/v5/pgtype"
)

type BoardDatum struct {
	ID               int32
	Title            string
	AllowEditing     pgtype.Bool
	AllowDeleting    pgtype.Bool
	EditWindow       pgtype.Int4
	TotalMembers     pgtype.Int4
	TotalThreads     pgtype.Int4
	TotalThreadPosts pgtype.Int4
}

type Member struct {
	Cookie           pgtype.Text
	DateJoined       pgtype.Timestamptz
	Email            string
	ID               int64
	IsAdmin          pgtype.Bool
	LastPost         pgtype.Timestamp
	LastView         pgtype.Timestamp
	TotalThreadPosts pgtype.Int4
	TotalThreads     pgtype.Int4
}

type MemberProfile struct {
	ID            int64
	MemberID      int64
	Location      pgtype.Text
	Pronouns      pgtype.Text
	PreferredName pgtype.Text
	ProperName    pgtype.Text
	PhotoUrl      pgtype.Text
	Timezone      pgtype.Text
	Bio           pgtype.Text
}

type Thread struct {
	ID             int64
	MemberID       int64
	Subject        string
	DatePosted     pgtype.Timestamptz
	FirstPostID    pgtype.Int4
	Posts          pgtype.Int4
	Views          pgtype.Int4
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	LastMemberID   int64
	DateLastPosted pgtype.Timestamptz
	Indexed        bool
	Edited         bool
	Deleted        bool
}

type ThreadMember struct {
	MemberID      int64
	ThreadID      int64
	Undot         bool
	DatePosted    pgtype.Timestamp
	LastViewPosts int32
}

type ThreadPost struct {
	ID         int64
	ThreadID   int64
	DatePosted pgtype.Timestamptz
	MemberID   int64
	Indexed    bool
	Edited     bool
	Deleted    bool
	Body       pgtype.Text
}
