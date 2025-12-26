package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// MockAddr is a mock implementation of the net.Addr interface
type MockAddr struct{}

func (m *MockAddr) Network() string {
	return "tcp"
}

func (m *MockAddr) String() string {
	return "mock address"
}

type MockTx struct{}

func (m *MockTx) Commit(ctx context.Context) error {
	return nil
}

func (m *MockTx) Rollback(ctx context.Context) error {
	return nil
}

type MockQueries struct {
	inTransaction             bool
	CreateOrReturnIDFunc      func(ctx context.Context, email string) (CreateOrReturnIDRow, error)
	CreateThreadFunc          func(ctx context.Context, arg CreateThreadParams) error
	GetBoardDataFunc          func(ctx context.Context) (GetBoardDataRow, error)
	GetMemberFunc             func(ctx context.Context, id int64) (GetMemberRow, error)
	GetThreadForEditFunc      func(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error)
	GetThreadPostForEditFunc  func(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error)
	GetThreadSequenceIdFunc   func(ctx context.Context) (int64, error)
	GetThreadSubjectByIdFunc  func(ctx context.Context, id int64) (string, error)
	ListMemberThreadsFunc     func(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error)
	ListThreadPostsFunc       func(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error)
	UpdateBoardEditWindowFunc func(ctx context.Context, arg pgtype.Int4) error
	UpdateBoardTitleFunc      func(ctx context.Context, arg string) error
	UpdateThreadFunc          func(ctx context.Context, arg UpdateThreadParams) error
	UpdateThreadPostFunc      func(ctx context.Context, arg UpdateThreadPostParams) error
	BlockMemberFunc           func(ctx context.Context, id int64) error
}

func (m *MockQueries) CreateOrReturnID(ctx context.Context, pEmail string) (CreateOrReturnIDRow, error) {
	if m.CreateOrReturnIDFunc != nil {
		return m.CreateOrReturnIDFunc(ctx, pEmail)
	}

	return CreateOrReturnIDRow{
		ID:        1,
		IsAdmin:   true,
		IsBlocked: false,
	}, nil
}

func (m *MockQueries) CreateThread(ctx context.Context, arg CreateThreadParams) error {
	return nil
}

func (m *MockQueries) CreateThreadPost(ctx context.Context, arg CreateThreadPostParams) error {
	return nil
}

func (m *MockQueries) GetBoardData(ctx context.Context) (GetBoardDataRow, error) {
	if m.GetBoardDataFunc != nil {
		return m.GetBoardDataFunc(ctx)
	}

	return GetBoardDataRow{
		EditWindow: pgtype.Int4{Int32: 900, Valid: true},
		Title:      "Mock Board Title",
		ID:         1,
	}, nil
}

func (m *MockQueries) GetMember(ctx context.Context, id int64) (GetMemberRow, error) {
	if m.GetMemberFunc != nil {
		return m.GetMemberFunc(ctx, id)
	}

	return GetMemberRow{
		Email:      "mock@example.com",
		Location:   pgtype.Text{String: "Mock Location", Valid: true},
		ID:         id,
		DateJoined: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PhotoUrl:   pgtype.Text{String: "http://mockexample.com/photo.jpg", Valid: true},
	}, nil
}

func (m *MockQueries) GetMemberId(ctx context.Context, email string) (int64, error) {
	return 1, nil
}

func (m *MockQueries) GetThreadPostSequenceId(ctx context.Context) (int64, error) {
	return 1, nil
}

func (m *MockQueries) GetThreadSequenceId(ctx context.Context) (int64, error) {
	return 1, nil
}

func (m *MockQueries) GetThreadSubjectById(ctx context.Context, id int64) (string, error) {
	if m.GetThreadSubjectByIdFunc != nil {
		return m.GetThreadSubjectByIdFunc(ctx, id)
	}

	return "Mock Thread Subject", nil
}

func (m *MockQueries) GetThreadForEdit(ctx context.Context, arg GetThreadForEditParams) (GetThreadForEditRow, error) {
	if m.GetThreadForEditFunc != nil {
		return m.GetThreadForEditFunc(ctx, arg)
	}

	return GetThreadForEditRow{
		Email:        pgtype.Text{String: "mock@example.com", Valid: true},
		Body:         pgtype.Text{String: "Mock Body", Valid: true},
		ThreadPostID: pgtype.Int8{Valid: true, Int64: arg.ID},
		Subject:      "Mock Subject",
		ThreadID:     arg.ID,
	}, nil
}

func (m *MockQueries) GetThreadPostForEdit(ctx context.Context, arg GetThreadPostForEditParams) (GetThreadPostForEditRow, error) {
	if m.GetThreadPostForEditFunc != nil {
		return m.GetThreadPostForEditFunc(ctx, arg)
	}

	return GetThreadPostForEditRow{
		ID:   arg.ID,
		Body: pgtype.Text{String: "Mock Body", Valid: true},
	}, nil
}

func (m *MockQueries) ListMemberThreads(ctx context.Context, memberID int64) ([]ListMemberThreadsRow, error) {
	if m.ListMemberThreadsFunc != nil {
		return m.ListMemberThreadsFunc(ctx, memberID)
	}

	return []ListMemberThreadsRow{}, nil
}

func (m *MockQueries) ListThreadPosts(ctx context.Context, arg ListThreadPostsParams) ([]ListThreadPostsRow, error) {
	if m.ListThreadPostsFunc != nil {
		return m.ListThreadPostsFunc(ctx, arg)
	}

	return []ListThreadPostsRow{}, nil
}

func (m *MockQueries) ListThreads(ctx context.Context, arg ListThreadsParams) ([]ListThreadsRow, error) {
	return []ListThreadsRow{
		{
			ThreadID:       1,
			DateLastPosted: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ID:             pgtype.Int8{Int64: 1, Valid: true},
			Email:          pgtype.Text{String: "mock@example.com", Valid: true},
			Lastid:         pgtype.Int8{Int64: 2, Valid: true},
			Lastname:       pgtype.Text{String: "mocklast@example.com", Valid: true},
			Subject:        "Mock Subject",
			Posts:          pgtype.Int4{Int32: 5, Valid: true},
			Views:          pgtype.Int4{Int32: 100, Valid: true},
			Body:           pgtype.Text{String: "Mock Body", Valid: true},
			LastViewPosts:  0,
			Dot:            false,
			Sticky:         pgtype.Bool{Bool: false, Valid: true},
			Locked:         pgtype.Bool{Bool: false, Valid: true},
		},
	}, nil
}

func (m *MockQueries) UpdateBoardEditWindow(ctx context.Context, arg pgtype.Int4) error {
	if m.UpdateBoardEditWindowFunc != nil {
		return m.UpdateBoardEditWindowFunc(ctx, arg)
	}

	return nil
}

func (m *MockQueries) UpdateBoardTitle(ctx context.Context, arg string) error {
	if m.UpdateBoardTitleFunc != nil {
		return m.UpdateBoardTitleFunc(ctx, arg)
	}

	return nil
}

func (m *MockQueries) UpdateMemberProfileByID(ctx context.Context, arg UpdateMemberProfileByIDParams) error {
	return nil
}

func (m *MockQueries) UpdateThread(ctx context.Context, arg UpdateThreadParams) error {
	if m.UpdateThreadFunc != nil {
		return m.UpdateThreadFunc(ctx, arg)
	}

	return nil
}

func (m *MockQueries) UpdateThreadPost(ctx context.Context, arg UpdateThreadPostParams) error {
	if m.UpdateThreadPostFunc != nil {
		return m.UpdateThreadPostFunc(ctx, arg)
	}

	return nil
}

func (m *MockQueries) BlockMember(ctx context.Context, id int64) error {
	if m.BlockMemberFunc != nil {
		return m.BlockMemberFunc(ctx, id)
	}

	return nil
}

func (m *MockQueries) WithTx(pgx.Tx) ExtendedQuerier {
	return &MockQueries{
		inTransaction: true,
	}
}

type MockTailscaleClient struct {
	WhoIsFunc         func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	ExpandSNINameFunc func(ctx context.Context, hostname string) (string, bool)
}

func (m *MockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.WhoIsFunc != nil {
		return m.WhoIsFunc(ctx, remoteAddr)
	}

	return &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "mock@example.com",
		},
	}, nil
}

func (m *MockTailscaleClient) ExpandSNIName(ctx context.Context, hostname string) (string, bool) {
	if m.ExpandSNINameFunc != nil {
		return m.ExpandSNINameFunc(ctx, hostname)
	}
	return "", false
}

func (m *MockTailscaleClient) Status(ctx context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{
		CertDomains: []string{"tsnet.example.com"},
	}, nil
}

func (m *MockTailscaleClient) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{
		CertDomains: []string{"tsnet.example.com"},
	}, nil
}
