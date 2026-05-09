package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tsocial/tsocial/internal/auth"
	"github.com/tsocial/tsocial/internal/domain"
	"github.com/tsocial/tsocial/internal/hub"
)

func (m *AppModel) runSlash(line string) tea.Cmd {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]
	rest := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
	ctx := context.Background()

	switch cmd {
	case "/help", "/?":
		m.help = true
		return nil
	case "/quit":
		return m.quit()
	case "/join":
		if len(args) < 1 {
			return flash("usage: /join <room>", true)
		}
		name := strings.TrimPrefix(strings.ToLower(args[0]), "#")
		if !domain.ValidRoomName(name) {
			return flash("invalid room name", true)
		}
		room, err := m.deps.Store.RoomByName(ctx, name)
		if err != nil {
			return flash("no such room", true)
		}
		if room.IsPrivate {
			if ok, _, _ := m.deps.Store.IsMember(ctx, room.ID, m.user.ID); !ok {
				return flash("private room — invite required", true)
			}
		}
		if err := m.deps.Store.JoinRoom(ctx, room.ID, m.user.ID); err != nil {
			return flash(err.Error(), true)
		}
		m.refreshRooms(ctx)
		m.deps.Hub.JoinRoom(m.hubSub, room.ID)
		m.deps.Hub.PublishRoom(room.ID, hub.Event{Kind: "room_meta", RoomID: room.ID})
		m.openRoom(ctx, room.ID)
		return flash("joined #"+name, false)

	case "/leave":
		if m.current != viewRoom {
			return flash("not in a room", true)
		}
		rid := m.currentRoomID
		_ = m.deps.Store.LeaveRoom(ctx, rid, m.user.ID)
		m.deps.Hub.LeaveRoom(m.hubSub, rid)
		m.refreshRooms(ctx)
		if len(m.rooms) > 0 {
			m.openRoom(ctx, m.rooms[0].ID)
		} else {
			m.current = viewNone
		}
		m.deps.Hub.PublishRoom(rid, hub.Event{Kind: "room_meta", RoomID: rid})
		return flash("left room", false)

	case "/create":
		if len(args) < 1 {
			return flash("usage: /create <room> [public|private]", true)
		}
		name := strings.TrimPrefix(strings.ToLower(args[0]), "#")
		if !domain.ValidRoomName(name) {
			return flash("invalid room name", true)
		}
		priv := false
		if len(args) >= 2 && strings.ToLower(args[1]) == "private" {
			priv = true
		}
		// rate limit: 5/day
		if n, _ := m.deps.Store.CountRoomCreates(ctx, m.user.ID, 24*time.Hour); n >= 5 {
			return flash("room creation limit reached (5/day)", true)
		}
		if existing, _ := m.deps.Store.RoomByName(ctx, name); existing != nil {
			return flash("room exists", true)
		}
		room, err := m.deps.Store.CreateRoom(ctx, name, priv, m.user.ID)
		if err != nil {
			return flash(err.Error(), true)
		}
		m.refreshRooms(ctx)
		m.deps.Hub.JoinRoom(m.hubSub, room.ID)
		m.openRoom(ctx, room.ID)
		return flash("created #"+name, false)

	case "/topic":
		if m.current != viewRoom {
			return flash("not in a room", true)
		}
		_, role, _ := m.deps.Store.IsMember(ctx, m.currentRoomID, m.user.ID)
		if role != domain.RoleOwner && !m.user.IsAdmin {
			return flash("only owner can set topic", true)
		}
		topic := rest
		if len(topic) > domain.MaxTopicLen {
			topic = topic[:domain.MaxTopicLen]
		}
		_ = m.deps.Store.SetTopic(ctx, m.currentRoomID, topic)
		m.deps.Hub.PublishRoom(m.currentRoomID, hub.Event{Kind: "room_meta", RoomID: m.currentRoomID})
		return flash("topic updated", false)

	case "/invite":
		if m.current != viewRoom {
			return flash("not in a room", true)
		}
		if len(args) < 1 {
			return flash("usage: /invite <handle>", true)
		}
		_, role, _ := m.deps.Store.IsMember(ctx, m.currentRoomID, m.user.ID)
		if role != domain.RoleOwner && role != domain.RoleModerator && !m.user.IsAdmin {
			return flash("only owner/mods may invite", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		if err := m.deps.Store.JoinRoom(ctx, m.currentRoomID, u.ID); err != nil {
			return flash(err.Error(), true)
		}
		m.deps.Hub.PublishRoom(m.currentRoomID, hub.Event{Kind: "room_meta", RoomID: m.currentRoomID})
		return flash("invited @"+h, false)

	case "/kick", "/ban":
		if m.current != viewRoom {
			return flash("not in a room", true)
		}
		if len(args) < 1 {
			return flash("usage: "+cmd+" <handle> [reason]", true)
		}
		_, role, _ := m.deps.Store.IsMember(ctx, m.currentRoomID, m.user.ID)
		if role != domain.RoleOwner && role != domain.RoleModerator && !m.user.IsAdmin {
			return flash("mods only", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		reason := strings.Join(args[1:], " ")
		if cmd == "/ban" {
			_ = m.deps.Store.BanFromRoom(ctx, m.currentRoomID, u.ID, m.user.ID, reason)
		} else {
			_ = m.deps.Store.KickFromRoom(ctx, m.currentRoomID, u.ID)
		}
		m.deps.Hub.RemoveUserFromRoom(u.ID, m.currentRoomID)
		m.deps.Hub.PublishRoom(m.currentRoomID, hub.Event{Kind: "room_meta", RoomID: m.currentRoomID})
		return flash(cmd+" applied", false)

	case "/msg":
		if len(args) < 1 {
			return flash("usage: /msg <handle> [text]", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		if u.ID == m.user.ID {
			return flash("can't DM yourself", true)
		}
		// add to peers if not present
		known := false
		for _, p := range m.dmPeers {
			if p.ID == u.ID {
				known = true
				break
			}
		}
		if !known {
			m.dmPeers = append([]*domain.User{u}, m.dmPeers...)
			m.userCache[u.ID] = u
		}
		text := strings.TrimSpace(strings.TrimPrefix(rest, args[0]))
		m.openDM(ctx, u.ID)
		if text != "" {
			return m.sendCurrent(text)
		}
		return nil

	case "/whois":
		if len(args) < 1 {
			return flash("usage: /whois <handle>", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		online := "offline"
		if m.deps.Hub.IsOnline(u.ID) {
			online = "online"
		}
		info := fmt.Sprintf("@%s — %s — joined %s — %s", u.Handle, u.DisplayName, u.CreatedAt.Format("2006-01-02"), online)
		if u.Bio != "" {
			info += " | " + u.Bio
		}
		return flash(info, false)

	case "/block":
		if len(args) < 1 {
			return flash("usage: /block <handle>", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		_ = m.deps.Store.Block(ctx, m.user.ID, u.ID)
		m.refreshBlocks(ctx)
		m.renderHistory()
		return flash("blocked @"+h, false)

	case "/unblock":
		if len(args) < 1 {
			return flash("usage: /unblock <handle>", true)
		}
		h := strings.TrimPrefix(strings.ToLower(args[0]), "@")
		u, err := m.deps.Store.UserByHandle(ctx, h)
		if err != nil {
			return flash("no such user", true)
		}
		_ = m.deps.Store.Unblock(ctx, m.user.ID, u.ID)
		m.refreshBlocks(ctx)
		m.renderHistory()
		return flash("unblocked @"+h, false)

	case "/report":
		if len(args) < 2 {
			return flash("usage: /report <message-id> <reason>", true)
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return flash("bad message id", true)
		}
		reason := strings.Join(args[1:], " ")
		if err := m.deps.Store.ReportMessageForUser(ctx, m.user.ID, id, reason); err != nil {
			return flash(err.Error(), true)
		}
		return flash("reported. thank you.", false)

	case "/delete":
		if len(args) < 1 {
			return flash("usage: /delete <message-id>", true)
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return flash("bad id", true)
		}
		// Admins can target any message, including in rooms they aren't in.
		// Non-admins go through the visibility check.
		var msg *domain.Message
		if m.user.IsAdmin {
			msg, err = m.deps.Store.MessageByID(ctx, id)
		} else {
			msg, err = m.deps.Store.MessageByIDForUser(ctx, m.user.ID, id)
		}
		if err != nil {
			return flash("not found", true)
		}
		if err := m.deps.Store.DeleteMessageForUser(ctx, id, m.user.ID, m.user.IsAdmin); err != nil {
			return flash(err.Error(), true)
		}
		if msg.RoomID != nil {
			m.deps.Hub.PublishRoom(*msg.RoomID, hub.Event{Kind: "msg_deleted", RoomID: *msg.RoomID})
		}
		return flash("deleted", false)

	case "/me":
		if rest == "" {
			return flash("usage: /me <text>", true)
		}
		return m.sendCurrent("* " + m.user.Handle + " " + rest)

	case "/search":
		if m.current != viewRoom || rest == "" {
			return flash("usage: /search <query> in a room", true)
		}
		results, err := m.deps.Store.SearchRoomForUser(ctx, m.user.ID, m.currentRoomID, rest, 50)
		if err != nil {
			return flash("search failed: "+err.Error(), true)
		}
		if len(results) == 0 {
			return flash("no matches", false)
		}
		// Replace history view temporarily with results
		m.messages[m.curKey()] = results
		m.renderHistory()
		return flash(fmt.Sprintf("%d match(es) — switch room to reset", len(results)), false)

	case "/profile":
		if rest == "" {
			return flash("usage: /profile <name>|<bio>|<pronouns>", true)
		}
		segs := strings.SplitN(rest, "|", 3)
		name, bio, pron := m.user.DisplayName, m.user.Bio, m.user.Pronouns
		if len(segs) >= 1 {
			name = strings.TrimSpace(segs[0])
		}
		if len(segs) >= 2 {
			bio = strings.TrimSpace(segs[1])
		}
		if len(segs) >= 3 {
			pron = strings.TrimSpace(segs[2])
		}
		if len(name) > domain.MaxDisplayName || len(bio) > domain.MaxBio || len(pron) > domain.MaxPronouns {
			return flash("field too long", true)
		}
		if err := m.deps.Store.UpdateProfile(ctx, m.user.ID, name, bio, pron); err != nil {
			return flash(err.Error(), true)
		}
		m.user.DisplayName, m.user.Bio, m.user.Pronouns = name, bio, pron
		return flash("profile updated", false)

	case "/addkey":
		if rest == "" {
			return flash("usage: /addkey <ssh-ed25519|ssh-rsa> AAAA…  (paste full public-key line; optional label after a space)", true)
		}
		// Last whitespace-separated token may be a label if there are >2 tokens
		// (the standard authorized_keys "comment" field works for ssh.ParseAuthorizedKey).
		key, fp, err := auth.ParseAuthorizedKey(rest)
		if err != nil {
			return flash("invalid public key: "+err.Error(), true)
		}
		// Reject if the key is already attached to any user.
		if owner, _ := m.deps.Store.UserByFingerprint(ctx, fp); owner != nil {
			if owner.ID == m.user.ID {
				return flash("that key is already attached to your account", true)
			}
			return flash("that key is already attached to another account", true)
		}
		label := ""
		if comment := keyComment(rest); comment != "" {
			label = comment
		}
		if err := m.deps.Store.AddKey(ctx, m.user.ID, fp, auth.AuthorizedKey(key), label); err != nil {
			return flash("add key failed: "+err.Error(), true)
		}
		return flash("key added — fingerprint "+fp, false)

	case "/keys":
		keys, err := m.deps.Store.UserKeys(ctx, m.user.ID)
		if err != nil {
			return flash(err.Error(), true)
		}
		var lines []string
		for _, k := range keys {
			marker := " "
			if k.Fingerprint == m.deps.Fingerprint {
				marker = "*"
			}
			label := k.Label
			if label == "" {
				label = "(no label)"
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", marker, k.Fingerprint, label))
		}
		return flash(strings.Join(lines, " | "), false)

	case "/notifications", "/notifs":
		notifs, _ := m.deps.Store.ListNotifications(ctx, m.user.ID, 20)
		var lines []string
		for _, n := range notifs {
			marker := "•"
			if n.Read {
				marker = " "
			}
			lines = append(lines, fmt.Sprintf("%s %s — %s", marker, n.Kind, n.CreatedAt.Format("01-02 15:04")))
		}
		if len(lines) == 0 {
			return flash("no notifications", false)
		}
		return flash(strings.Join(lines, " | "), false)
	}
	return flash("unknown command: "+cmd, true)
}

// keyComment extracts the optional "comment" trailing field from an
// authorized_keys-style line (e.g. "ssh-ed25519 AAAA... my-laptop").
// Returns "" if there is no comment.
func keyComment(line string) string {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[2:], " ")
}
