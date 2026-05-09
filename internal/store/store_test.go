package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createUser(t *testing.T, st *Store, handle string) int64 {
	t.Helper()
	u, err := st.CreateUser(context.Background(), handle, "SHA256:"+handle, "ssh-ed25519 "+handle)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestRoomMessageRequiresMembership(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	outsider := createUser(t, st, "outsider")
	room, err := st.CreateRoom(ctx, "general", false, owner)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.InsertRoomMessage(ctx, room.ID, outsider, "hello", nil); err == nil {
		t.Fatal("expected non-member send to fail")
	}
	if _, err := st.RoomHistoryForUser(ctx, outsider, room.ID, 10); err == nil {
		t.Fatal("expected non-member history read to fail")
	}
	if _, err := st.SearchRoomForUser(ctx, outsider, room.ID, "hello", 10); err == nil {
		t.Fatal("expected non-member search to fail")
	}
	if err := st.JoinRoom(ctx, room.ID, outsider); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertRoomMessage(ctx, room.ID, outsider, "hello", nil); err != nil {
		t.Fatalf("expected member send to succeed: %v", err)
	}
}

func TestBanPreventsRoomMessage(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	member := createUser(t, st, "member")
	room, err := st.CreateRoom(ctx, "general", false, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JoinRoom(ctx, room.ID, member); err != nil {
		t.Fatal(err)
	}
	if err := st.BanFromRoom(ctx, room.ID, member, owner, "bad\x1b[31m"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertRoomMessage(ctx, room.ID, member, "still here", nil); err == nil {
		t.Fatal("expected banned user send to fail")
	}
}

func TestBlockedUserCannotDM(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	alice := createUser(t, st, "alice")
	bob := createUser(t, st, "bob")
	if err := st.Block(ctx, alice, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertDM(ctx, bob, alice, "hello"); err == nil {
		t.Fatal("expected DM from blocked user to fail")
	}
	if _, err := st.InsertDM(ctx, alice, bob, "hello"); err != nil {
		t.Fatalf("expected blocker to DM blocked user: %v", err)
	}
}

func TestReportRequiresMessageVisibility(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	member := createUser(t, st, "member")
	outsider := createUser(t, st, "outsider")
	room, err := st.CreateRoom(ctx, "general", false, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JoinRoom(ctx, room.ID, member); err != nil {
		t.Fatal(err)
	}
	msg, _, err := st.InsertRoomMessage(ctx, room.ID, member, "visible", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReportMessageForUser(ctx, outsider, msg.ID, "reason"); err == nil {
		t.Fatal("expected outsider report to fail")
	}
	if err := st.ReportMessageForUser(ctx, owner, msg.ID, "reason"); err != nil {
		t.Fatalf("expected visible report to succeed: %v", err)
	}
}

func TestMentionsOnlyForRoomMembers(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	member := createUser(t, st, "member")
	outsider := createUser(t, st, "outsider")
	room, err := st.CreateRoom(ctx, "general", true, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JoinRoom(ctx, room.ID, member); err != nil {
		t.Fatal(err)
	}
	_, notified, err := st.InsertRoomMessage(ctx, room.ID, owner, "@member @outsider", []int64{member, outsider})
	if err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || notified[0] != member {
		t.Fatalf("expected only member to be notified, got %v", notified)
	}
}

func TestAdminCanDeleteOutsideRoom(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	member := createUser(t, st, "member")
	if err := st.SetAdmin(ctx, "owner", true); err != nil {
		t.Fatal(err)
	}
	other := createUser(t, st, "other")
	room, err := st.CreateRoom(ctx, "secret", true, member)
	if err != nil {
		t.Fatal(err)
	}
	msg, _, err := st.InsertRoomMessage(ctx, room.ID, member, "private text", nil)
	if err != nil {
		t.Fatal(err)
	}
	// owner is admin but not a member of #secret
	if err := st.DeleteMessageForUser(ctx, msg.ID, owner, true); err != nil {
		t.Fatalf("admin delete should succeed: %v", err)
	}
	// non-admin non-member must be blocked
	if err := st.DeleteMessageForUser(ctx, msg.ID, other, false); err == nil {
		t.Fatal("non-admin non-member delete should fail")
	}
}

func TestStoreSanitizesPersistedUserText(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	owner := createUser(t, st, "owner")
	room, err := st.CreateRoom(ctx, "general", false, owner)
	if err != nil {
		t.Fatal(err)
	}
	msg, _, err := st.InsertRoomMessage(ctx, room.ID, owner, "hi \x1b[31mred\u009b0m", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "hi red" {
		t.Fatalf("unexpected sanitized body: %q", msg.Body)
	}
}
