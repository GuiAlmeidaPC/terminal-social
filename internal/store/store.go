package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tsocial/tsocial/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(string(b)); err != nil {
			return fmt.Errorf("migration %s: %w", n, err)
		}
	}
	return nil
}

// ---------- Users / Keys ----------

func (s *Store) UserByFingerprint(ctx context.Context, fp string) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.bio, u.pronouns, u.is_admin, u.is_suspended, u.created_at, u.last_seen_at
		FROM users u JOIN user_keys k ON k.user_id = u.id
		WHERE k.fingerprint = ?`, fp)
	return scanUser(row)
}

func (s *Store) UserByHandle(ctx context.Context, h string) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, handle, display_name, bio, pronouns, is_admin, is_suspended, created_at, last_seen_at
		FROM users WHERE handle = ? COLLATE NOCASE`, h)
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id int64) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, handle, display_name, bio, pronouns, is_admin, is_suspended, created_at, last_seen_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*domain.User, error) {
	u := &domain.User{}
	var lastSeen sql.NullInt64
	var createdAt int64
	var isAdmin, isSusp int
	if err := row.Scan(&u.ID, &u.Handle, &u.DisplayName, &u.Bio, &u.Pronouns, &isAdmin, &isSusp, &createdAt, &lastSeen); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsSuspended = isSusp != 0
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastSeen.Valid {
		t := time.Unix(lastSeen.Int64, 0)
		u.LastSeenAt = &t
	}
	return u, nil
}

func (s *Store) CreateUser(ctx context.Context, handle, fingerprint, pubKey string) (*domain.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO users(handle, created_at) VALUES (?, ?)`, handle, now)
	if err != nil {
		return nil, err
	}
	uid, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_keys(user_id, fingerprint, public_key, added_at) VALUES (?, ?, ?, ?)`,
		uid, fingerprint, pubKey, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, uid)
}

func (s *Store) AddKey(ctx context.Context, userID int64, fingerprint, pubKey, label string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_keys(user_id, fingerprint, public_key, label, added_at) VALUES (?, ?, ?, ?, ?)`,
		userID, fingerprint, pubKey, label, time.Now().Unix())
	return err
}

func (s *Store) UserKeys(ctx context.Context, userID int64) ([]domain.UserKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, fingerprint, public_key, label, added_at FROM user_keys WHERE user_id = ? ORDER BY added_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UserKey
	for rows.Next() {
		var k domain.UserKey
		var added int64
		if err := rows.Scan(&k.ID, &k.UserID, &k.Fingerprint, &k.PublicKey, &k.Label, &added); err != nil {
			return nil, err
		}
		k.AddedAt = time.Unix(added, 0)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProfile(ctx context.Context, userID int64, displayName, bio, pronouns string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET display_name=?, bio=?, pronouns=? WHERE id=?`,
		domain.SanitizeUserText(displayName), domain.SanitizeUserText(bio), domain.SanitizeUserText(pronouns), userID)
	return err
}

func (s *Store) TouchLastSeen(ctx context.Context, userID int64) {
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_seen_at=? WHERE id=?`, time.Now().Unix(), userID)
}

func (s *Store) SetAdmin(ctx context.Context, handle string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_admin=? WHERE handle=? COLLATE NOCASE`, v, handle)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("user not found")
	}
	return nil
}

func (s *Store) SetSuspended(ctx context.Context, handle string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_suspended=? WHERE handle=? COLLATE NOCASE`, v, handle)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("user not found")
	}
	return nil
}

// ---------- Rooms ----------

func (s *Store) CreateRoom(ctx context.Context, name string, private bool, owner int64) (*domain.Room, error) {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	priv := 0
	if private {
		priv = 1
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO rooms(name, is_private, created_by, created_at) VALUES (?, ?, ?, ?)`,
		name, priv, owner, now)
	if err != nil {
		return nil, err
	}
	rid, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_members(room_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`,
		rid, owner, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.RoomByID(ctx, rid)
}

func (s *Store) RoomByName(ctx context.Context, name string) (*domain.Room, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, topic, is_private, created_by, created_at FROM rooms WHERE name=? COLLATE NOCASE AND deleted_at IS NULL`, name)
	return scanRoom(row)
}

func (s *Store) RoomByID(ctx context.Context, id int64) (*domain.Room, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, topic, is_private, created_by, created_at FROM rooms WHERE id=? AND deleted_at IS NULL`, id)
	return scanRoom(row)
}

func scanRoom(row *sql.Row) (*domain.Room, error) {
	r := &domain.Room{}
	var priv int
	var created int64
	if err := row.Scan(&r.ID, &r.Name, &r.Topic, &priv, &r.CreatedBy, &created); err != nil {
		return nil, err
	}
	r.IsPrivate = priv != 0
	r.CreatedAt = time.Unix(created, 0)
	return r, nil
}

func (s *Store) ListPublicRooms(ctx context.Context) ([]domain.Room, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, topic, is_private, created_by, created_at FROM rooms WHERE is_private=0 AND deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRooms(rows)
}

func (s *Store) ListUserRooms(ctx context.Context, userID int64) ([]domain.Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.topic, r.is_private, r.created_by, r.created_at
		FROM rooms r JOIN room_members m ON m.room_id = r.id
		WHERE m.user_id = ? AND r.deleted_at IS NULL ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRooms(rows)
}

func scanRooms(rows *sql.Rows) ([]domain.Room, error) {
	var out []domain.Room
	for rows.Next() {
		var r domain.Room
		var priv int
		var created int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Topic, &priv, &r.CreatedBy, &created); err != nil {
			return nil, err
		}
		r.IsPrivate = priv != 0
		r.CreatedAt = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) JoinRoom(ctx context.Context, roomID, userID int64) error {
	// banned?
	var banned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_bans WHERE room_id=? AND user_id=?`, roomID, userID).Scan(&banned); err == nil && banned > 0 {
		return errors.New("you are banned from this room")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO room_members(room_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)`,
		roomID, userID, time.Now().Unix())
	return err
}

func (s *Store) LeaveRoom(ctx context.Context, roomID, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND user_id=?`, roomID, userID)
	return err
}

func (s *Store) IsMember(ctx context.Context, roomID, userID int64) (bool, string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM room_members WHERE room_id=? AND user_id=?`, roomID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, role, nil
}

func (s *Store) RoomMembers(ctx context.Context, roomID int64) ([]domain.Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.user_id, u.handle, m.role, m.joined_at
		FROM room_members m JOIN users u ON u.id = m.user_id
		WHERE m.room_id=? ORDER BY u.handle COLLATE NOCASE`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Member
	for rows.Next() {
		var m domain.Member
		var joined int64
		if err := rows.Scan(&m.UserID, &m.Handle, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.JoinedAt = time.Unix(joined, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RoomMembersForUser(ctx context.Context, userID, roomID int64) ([]domain.Member, error) {
	if ok, _, err := s.IsMember(ctx, roomID, userID); err != nil {
		return nil, err
	} else if !ok {
		return nil, errors.New("not a member of this room")
	}
	return s.RoomMembers(ctx, roomID)
}

func (s *Store) SetTopic(ctx context.Context, roomID int64, topic string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE rooms SET topic=? WHERE id=?`, domain.SanitizeUserText(topic), roomID)
	return err
}

func (s *Store) DeleteRoom(ctx context.Context, roomID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE rooms SET deleted_at=? WHERE id=?`, time.Now().Unix(), roomID)
	return err
}

func (s *Store) Promote(ctx context.Context, roomID, userID int64, role string) error {
	if role != domain.RoleMember && role != domain.RoleModerator && role != domain.RoleOwner {
		return errors.New("invalid role")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE room_members SET role=? WHERE room_id=? AND user_id=?`, role, roomID, userID)
	return err
}

func (s *Store) BanFromRoom(ctx context.Context, roomID, userID, by int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO room_bans(room_id, user_id, banned_by, reason, banned_at) VALUES (?, ?, ?, ?, ?)`,
		roomID, userID, by, domain.SanitizeUserText(reason), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND user_id=?`, roomID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) KickFromRoom(ctx context.Context, roomID, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND user_id=?`, roomID, userID)
	return err
}

// ---------- Messages ----------

// InsertRoomMessage stores a room message and creates mention notifications
// only for users who are members of the room. The returned slice contains the
// user IDs that were actually notified, in input order.
func (s *Store) InsertRoomMessage(ctx context.Context, roomID, authorID int64, body string, mentions []int64) (*domain.Message, []int64, error) {
	if ok, _, err := s.IsMember(ctx, roomID, authorID); err != nil {
		return nil, nil, err
	} else if !ok {
		return nil, nil, errors.New("not a member of this room")
	}
	var banned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_bans WHERE room_id=? AND user_id=?`, roomID, authorID).Scan(&banned); err != nil {
		return nil, nil, err
	}
	if banned > 0 {
		return nil, nil, errors.New("you are banned from this room")
	}
	body = domain.SanitizeUserText(body)
	if body == "" {
		return nil, nil, errors.New("empty message")
	}
	if len(body) > domain.MaxBody {
		body = body[:domain.MaxBody]
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO messages(kind, room_id, author_id, body, created_at) VALUES ('room', ?, ?, ?, ?)`,
		roomID, authorID, body, now)
	if err != nil {
		return nil, nil, err
	}
	mid, _ := res.LastInsertId()
	notified := make([]int64, 0, len(mentions))
	for _, uid := range mentions {
		if uid == authorID {
			continue
		}
		if ok, _, err := s.IsMember(ctx, roomID, uid); err != nil || !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mentions(message_id, user_id) VALUES (?, ?)`, mid, uid); err != nil {
			return nil, nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notifications(user_id, kind, message_id, room_id, created_at) VALUES (?, 'mention', ?, ?, ?)`,
			uid, mid, roomID, now); err != nil {
			return nil, nil, err
		}
		notified = append(notified, uid)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	msg, err := s.MessageByID(ctx, mid)
	if err != nil {
		return nil, nil, err
	}
	return msg, notified, nil
}

func (s *Store) InsertDM(ctx context.Context, authorID, peerID int64, body string) (*domain.Message, error) {
	if authorID == peerID {
		return nil, errors.New("cannot DM yourself")
	}
	if _, err := s.UserByID(ctx, peerID); err != nil {
		return nil, errors.New("recipient not found")
	}
	if blocked, err := s.IsBlocked(ctx, peerID, authorID); err != nil {
		return nil, err
	} else if blocked {
		return nil, errors.New("cannot DM this user")
	}
	body = domain.SanitizeUserText(body)
	if body == "" {
		return nil, errors.New("empty message")
	}
	if len(body) > domain.MaxBody {
		body = body[:domain.MaxBody]
	}
	a, b := domain.DMKey(authorID, peerID)
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO messages(kind, dm_peer_a, dm_peer_b, author_id, body, created_at) VALUES ('dm', ?, ?, ?, ?, ?)`,
		a, b, authorID, body, now)
	if err != nil {
		return nil, err
	}
	mid, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO notifications(user_id, kind, message_id, created_at) VALUES (?, 'dm', ?, ?)`,
		peerID, mid, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.MessageByID(ctx, mid)
}

func (s *Store) MessageByID(ctx context.Context, id int64) (*domain.Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT m.id, m.kind, m.room_id, m.dm_peer_a, m.dm_peer_b, m.author_id, u.handle, m.body, m.created_at, m.deleted_at
		FROM messages m JOIN users u ON u.id = m.author_id WHERE m.id = ?`, id)
	return scanMessage(row)
}

func (s *Store) MessageByIDForUser(ctx context.Context, userID, id int64) (*domain.Message, error) {
	msg, err := s.MessageByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.canReadMessage(ctx, userID, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func scanMessage(row *sql.Row) (*domain.Message, error) {
	m := &domain.Message{}
	var rid, a, b sql.NullInt64
	var deleted sql.NullInt64
	var created int64
	if err := row.Scan(&m.ID, &m.Kind, &rid, &a, &b, &m.AuthorID, &m.Author, &m.Body, &created, &deleted); err != nil {
		return nil, err
	}
	if rid.Valid {
		v := rid.Int64
		m.RoomID = &v
	}
	if a.Valid {
		v := a.Int64
		m.DMPeerA = &v
	}
	if b.Valid {
		v := b.Int64
		m.DMPeerB = &v
	}
	m.CreatedAt = time.Unix(created, 0)
	m.Deleted = deleted.Valid
	return m, nil
}

// RoomHistory returns the most recent `limit` messages, oldest first.
func (s *Store) RoomHistory(ctx context.Context, roomID int64, limit int) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.room_id, m.dm_peer_a, m.dm_peer_b, m.author_id, u.handle, m.body, m.created_at, m.deleted_at
		FROM messages m JOIN users u ON u.id = m.author_id
		WHERE m.kind='room' AND m.room_id=?
		ORDER BY m.id DESC LIMIT ?`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) RoomHistoryForUser(ctx context.Context, userID, roomID int64, limit int) ([]domain.Message, error) {
	if ok, _, err := s.IsMember(ctx, roomID, userID); err != nil {
		return nil, err
	} else if !ok {
		return nil, errors.New("not a member of this room")
	}
	return s.RoomHistory(ctx, roomID, limit)
}

func (s *Store) DMHistory(ctx context.Context, u1, u2 int64, limit int) ([]domain.Message, error) {
	a, b := domain.DMKey(u1, u2)
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.room_id, m.dm_peer_a, m.dm_peer_b, m.author_id, u.handle, m.body, m.created_at, m.deleted_at
		FROM messages m JOIN users u ON u.id = m.author_id
		WHERE m.kind='dm' AND m.dm_peer_a=? AND m.dm_peer_b=?
		ORDER BY m.id DESC LIMIT ?`, a, b, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) DMHistoryForUser(ctx context.Context, userID, peerID int64, limit int) ([]domain.Message, error) {
	if userID == peerID {
		return nil, errors.New("cannot DM yourself")
	}
	return s.DMHistory(ctx, userID, peerID, limit)
}

func scanMessages(rows *sql.Rows) ([]domain.Message, error) {
	var out []domain.Message
	for rows.Next() {
		var m domain.Message
		var rid, a, b sql.NullInt64
		var deleted sql.NullInt64
		var created int64
		if err := rows.Scan(&m.ID, &m.Kind, &rid, &a, &b, &m.AuthorID, &m.Author, &m.Body, &created, &deleted); err != nil {
			return nil, err
		}
		if rid.Valid {
			v := rid.Int64
			m.RoomID = &v
		}
		if a.Valid {
			v := a.Int64
			m.DMPeerA = &v
		}
		if b.Valid {
			v := b.Int64
			m.DMPeerB = &v
		}
		m.CreatedAt = time.Unix(created, 0)
		m.Deleted = deleted.Valid
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMessage(ctx context.Context, msgID, byUser int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET deleted_at=?, deleted_by=? WHERE id=? AND deleted_at IS NULL`,
		time.Now().Unix(), byUser, msgID)
	return err
}

func (s *Store) DeleteMessageForUser(ctx context.Context, msgID, byUser int64, isAdmin bool) error {
	msg, err := s.MessageByID(ctx, msgID)
	if err != nil {
		return err
	}
	if msg.AuthorID != byUser && !isAdmin {
		if msg.RoomID == nil {
			return errors.New("not allowed")
		}
		_, role, err := s.IsMember(ctx, *msg.RoomID, byUser)
		if err != nil {
			return err
		}
		if role != domain.RoleOwner && role != domain.RoleModerator {
			return errors.New("not allowed")
		}
	}
	return s.DeleteMessage(ctx, msgID, byUser)
}

// SearchRoom returns matching messages; uses FTS5 if possible, falls back to LIKE.
func (s *Store) SearchRoom(ctx context.Context, roomID int64, q string, limit int) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.room_id, m.dm_peer_a, m.dm_peer_b, m.author_id, u.handle, m.body, m.created_at, m.deleted_at
		FROM messages_fts fts JOIN messages m ON m.id = fts.rowid JOIN users u ON u.id = m.author_id
		WHERE fts.body MATCH ? AND m.kind='room' AND m.room_id=?
		ORDER BY m.id DESC LIMIT ?`, q, roomID, limit)
	if err != nil {
		// fallback
		rows, err = s.db.QueryContext(ctx, `
			SELECT m.id, m.kind, m.room_id, m.dm_peer_a, m.dm_peer_b, m.author_id, u.handle, m.body, m.created_at, m.deleted_at
			FROM messages m JOIN users u ON u.id = m.author_id
			WHERE m.kind='room' AND m.room_id=? AND m.body LIKE ?
			ORDER BY m.id DESC LIMIT ?`, roomID, "%"+q+"%", limit)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) SearchRoomForUser(ctx context.Context, userID, roomID int64, q string, limit int) ([]domain.Message, error) {
	if ok, _, err := s.IsMember(ctx, roomID, userID); err != nil {
		return nil, err
	} else if !ok {
		return nil, errors.New("not a member of this room")
	}
	return s.SearchRoom(ctx, roomID, q, limit)
}

// ---------- Blocks ----------

func (s *Store) Block(ctx context.Context, by, target int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO blocks(user_id, blocked_id, created_at) VALUES (?, ?, ?)`,
		by, target, time.Now().Unix())
	return err
}

func (s *Store) Unblock(ctx context.Context, by, target int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blocks WHERE user_id=? AND blocked_id=?`, by, target)
	return err
}

func (s *Store) BlockedIDs(ctx context.Context, userID int64) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT blocked_id FROM blocks WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) IsBlocked(ctx context.Context, by, target int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE user_id=? AND blocked_id=?`, by, target).Scan(&n)
	return n > 0, err
}

// ---------- Notifications ----------

func (s *Store) ListNotifications(ctx context.Context, userID int64, limit int) ([]domain.Notification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, kind, message_id, room_id, created_at, read_at FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		var mid, rid, readAt sql.NullInt64
		var created int64
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &mid, &rid, &created, &readAt); err != nil {
			return nil, err
		}
		if mid.Valid {
			v := mid.Int64
			n.MessageID = &v
		}
		if rid.Valid {
			v := rid.Int64
			n.RoomID = &v
		}
		n.CreatedAt = time.Unix(created, 0)
		n.Read = readAt.Valid
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UnreadCounts(ctx context.Context, userID int64) (rooms map[int64]int, dms map[int64]int, err error) {
	rooms = map[int64]int{}
	dms = map[int64]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT room_id, message_id, kind FROM notifications WHERE user_id=? AND read_at IS NULL`, userID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rid, mid sql.NullInt64
		var kind string
		if err = rows.Scan(&rid, &mid, &kind); err != nil {
			return
		}
		switch kind {
		case "mention":
			if rid.Valid {
				rooms[rid.Int64]++
			}
		case "dm":
			if mid.Valid {
				// resolve peer for the dm
				var a, b, author int64
				if err := s.db.QueryRowContext(ctx, `SELECT dm_peer_a, dm_peer_b, author_id FROM messages WHERE id=?`, mid.Int64).Scan(&a, &b, &author); err == nil {
					peer := a
					if a == userID {
						peer = b
					}
					_ = author
					dms[peer]++
				}
			}
		}
	}
	err = rows.Err()
	return
}

func (s *Store) MarkRoomRead(ctx context.Context, userID, roomID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at=? WHERE user_id=? AND room_id=? AND read_at IS NULL`,
		time.Now().Unix(), userID, roomID)
	return err
}

func (s *Store) MarkDMRead(ctx context.Context, userID, peerID int64) error {
	a, b := domain.DMKey(userID, peerID)
	_, err := s.db.ExecContext(ctx, `
		UPDATE notifications SET read_at=? WHERE user_id=? AND read_at IS NULL AND kind='dm'
		AND message_id IN (SELECT id FROM messages WHERE kind='dm' AND dm_peer_a=? AND dm_peer_b=?)`,
		time.Now().Unix(), userID, a, b)
	return err
}

// ---------- Reports ----------

func (s *Store) Report(ctx context.Context, reporter int64, kind string, targetID int64, reason string) error {
	if kind != "message" && kind != "user" {
		return errors.New("invalid target kind")
	}
	reason = domain.SanitizeUserText(reason)
	_, err := s.db.ExecContext(ctx, `INSERT INTO reports(reporter_id, target_kind, target_id, reason, created_at) VALUES (?, ?, ?, ?, ?)`,
		reporter, kind, targetID, reason, time.Now().Unix())
	return err
}

func (s *Store) ReportMessageForUser(ctx context.Context, reporter, messageID int64, reason string) error {
	msg, err := s.MessageByID(ctx, messageID)
	if err != nil {
		return err
	}
	if err := s.canReadMessage(ctx, reporter, msg); err != nil {
		return err
	}
	return s.Report(ctx, reporter, "message", messageID, reason)
}

func (s *Store) canReadMessage(ctx context.Context, userID int64, msg *domain.Message) error {
	switch msg.Kind {
	case "room":
		if msg.RoomID == nil {
			return errors.New("invalid room message")
		}
		if ok, _, err := s.IsMember(ctx, *msg.RoomID, userID); err != nil {
			return err
		} else if !ok {
			return errors.New("not a member of this room")
		}
	case "dm":
		if msg.DMPeerA == nil || msg.DMPeerB == nil {
			return errors.New("invalid dm")
		}
		if *msg.DMPeerA != userID && *msg.DMPeerB != userID {
			return errors.New("not allowed")
		}
	default:
		return errors.New("invalid message kind")
	}
	return nil
}

// ---------- Maintenance ----------

func (s *Store) PurgeDeletedOlderThan(ctx context.Context, d time.Duration) (int64, error) {
	cutoff := time.Now().Add(-d).Unix()
	res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) CountRoomCreates(ctx context.Context, userID int64, since time.Duration) (int, error) {
	cutoff := time.Now().Add(-since).Unix()
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE created_by=? AND created_at > ?`, userID, cutoff).Scan(&n)
	return n, err
}

// ---------- Mentions parsing ----------

var mentionRe = regexp.MustCompile(`@([a-z0-9_-]{3,20})`)

// ParseMentions extracts unique mentioned handles (lowercased) from body.
func ParseMentions(body string) []string {
	matches := mentionRe.FindAllStringSubmatch(strings.ToLower(body), -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ResolveHandles maps handles to user IDs (skipping unknown).
func (s *Store) ResolveHandles(ctx context.Context, handles []string) ([]int64, error) {
	if len(handles) == 0 {
		return nil, nil
	}
	q := strings.Repeat("?,", len(handles))
	q = q[:len(q)-1]
	args := make([]any, len(handles))
	for i, h := range handles {
		args[i] = h
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users WHERE handle IN (`+q+`) COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DMPeers lists the peer user IDs the user has had DMs with, most recent first.
func (s *Store) DMPeers(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT CASE WHEN dm_peer_a=? THEN dm_peer_b ELSE dm_peer_a END AS peer, MAX(id) AS last
		FROM messages WHERE kind='dm' AND (dm_peer_a=? OR dm_peer_b=?)
		GROUP BY peer ORDER BY last DESC`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var peer int64
		var last int64
		if err := rows.Scan(&peer, &last); err != nil {
			return nil, err
		}
		out = append(out, peer)
	}
	return out, rows.Err()
}

// Stats summary for admin CLI.
type Stats struct {
	Users    int
	Rooms    int
	Messages int
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&st.Users); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE deleted_at IS NULL`).Scan(&st.Rooms); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&st.Messages); err != nil {
		return st, err
	}
	return st, nil
}
