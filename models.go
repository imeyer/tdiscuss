// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.27.0

package main

import (
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"
)

type BoardDatum struct {
	ID    int32
	Name  string
	Value string
}

type Favorite struct {
	ID       int32
	MemberID int64
	ThreadID int64
}

type Member struct {
	Cookie           pgtype.Text
	DateFirstPost    pgtype.Date
	DateJoined       pgtype.Timestamptz
	Email            string
	ID               int64
	IsAdmin          pgtype.Bool
	LastChat         pgtype.Timestamp
	LastPost         pgtype.Timestamp
	LastSearch       pgtype.Timestamp
	LastView         pgtype.Timestamp
	PhotoUrl         pgtype.Text
	Session          pgtype.Text
	TotalThreadPosts pgtype.Int4
	TotalThreads     pgtype.Int4
}

type MemberPref struct {
	ID       int32
	PrefID   int32
	MemberID int64
	Value    string
}

type Message struct {
	ID             int64
	MemberID       int64
	Subject        string
	FirstPostID    pgtype.Int4
	DatePosted     pgtype.Timestamp
	Posts          pgtype.Int4
	Views          pgtype.Int4
	LastMemberID   int64
	DateLastPosted pgtype.Timestamp
}

type MessageMember struct {
	MemberID      int64
	MessageID     int64
	DatePosted    pgtype.Timestamp
	LastViewPosts int32
	Deleted       bool
}

type MessagePost struct {
	ID         int64
	MessageID  int32
	DatePosted pgtype.Timestamp
	MemberID   int64
	MemberIp   netip.Prefix
	Body       pgtype.Text
}

type Pref struct {
	ID         int32
	PrefTypeID int32
	Name       string
	Display    string
	Profile    bool
	Session    bool
	Editable   bool
	Width      pgtype.Int4
	Ordering   pgtype.Int4
}

type PrefType struct {
	ID   int32
	Name string
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
