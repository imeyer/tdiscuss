// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.26.0

package discuss

import (
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"
)

type BoardDatum struct {
	ID    int32
	Name  string
	Value string
}

type Chat struct {
	ID       int32
	MemberID int32
	Stamp    pgtype.Timestamp
	Chat     pgtype.Text
}

type Donation struct {
	ID            int32
	FundraiserID  int32
	PaymentDate   pgtype.Date
	PaymentStatus pgtype.Text
	PayerEmail    pgtype.Text
	TxnID         pgtype.Text
	PaymentFee    pgtype.Numeric
	PaymentGross  pgtype.Numeric
}

type Favorite struct {
	ID       int32
	MemberID int32
	ThreadID int32
}

type Fundraiser struct {
	ID   int32
	Name pgtype.Text
	Goal pgtype.Numeric
}

type Member struct {
	ID               int32
	DateJoined       pgtype.Timestamptz
	DateFirstPost    pgtype.Date
	Email            string
	TotalThreads     pgtype.Int4
	TotalThreadPosts pgtype.Int4
	LastView         pgtype.Timestamp
	LastPost         pgtype.Timestamp
	LastChat         pgtype.Timestamp
	LastSearch       pgtype.Timestamp
	Banned           pgtype.Bool
	IsAdmin          pgtype.Bool
	Cookie           pgtype.Text
	Session          pgtype.Text
}

type MemberIgnore struct {
	MemberID       pgtype.Int4
	IgnoreMemberID pgtype.Int4
}

type MemberLurkUnlock struct {
	ID        int32
	MemberID  int32
	CreatedAt pgtype.Date
}

type MemberPref struct {
	ID       int32
	PrefID   int32
	MemberID int32
	Value    string
}

type Message struct {
	ID             int32
	MemberID       int32
	Subject        string
	FirstPostID    pgtype.Int4
	DatePosted     pgtype.Timestamp
	Posts          pgtype.Int4
	Views          pgtype.Int4
	LastMemberID   int32
	DateLastPosted pgtype.Timestamp
}

type MessageMember struct {
	MemberID      int32
	MessageID     int32
	DatePosted    pgtype.Timestamp
	LastViewPosts int32
	Deleted       bool
}

type MessagePost struct {
	ID         int32
	MessageID  int32
	DatePosted pgtype.Timestamp
	MemberID   int32
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

type Theme struct {
	ID    int32
	Name  string
	Value pgtype.Text
	Main  bool
}

type Thread struct {
	ID             int32
	MemberID       int32
	Subject        string
	DatePosted     pgtype.Timestamptz
	FirstPostID    pgtype.Int4
	Posts          pgtype.Int4
	Views          pgtype.Int4
	Sticky         pgtype.Bool
	Locked         pgtype.Bool
	LastMemberID   int32
	DateLastPosted pgtype.Timestamptz
	Indexed        bool
	Edited         bool
	Deleted        bool
	Legendary      bool
}

type ThreadMember struct {
	MemberID      int32
	ThreadID      int32
	Undot         bool
	Ignore        bool
	DatePosted    pgtype.Timestamp
	LastViewPosts int32
}

type ThreadPost struct {
	ID         int32
	ThreadID   int32
	DatePosted pgtype.Timestamptz
	MemberID   int32
	Indexed    bool
	Edited     bool
	Deleted    bool
	Body       pgtype.Text
}
