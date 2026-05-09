package tui

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tsocial/tsocial/internal/domain"
	"github.com/tsocial/tsocial/internal/hub"
	"github.com/tsocial/tsocial/internal/store"
)

// RateAllow is a per-user message rate-limit hook supplied by the SSH server.
// Returning false means the user has exceeded the configured rate.
type RateAllow func() bool

type Deps struct {
	Store        *store.Store
	Hub          *hub.Hub
	Log          *slog.Logger
	Fingerprint  string
	PublicKey    string
	ShortFP      string
	DefaultRoom  string
	RateMsg      RateAllow
	Width        int
	Height       int
	Done         <-chan struct{}
	OnDisconnect func()
}

type viewKind int

const (
	viewRoom viewKind = iota
	viewDM
	viewNone
)

type chanItem struct {
	kind   viewKind
	roomID int64
	peerID int64
	label  string
	unread int
	// for room: mention indicator (count > 0 of mentions)
	mention bool
	online  bool
}

type pane int

const (
	paneNav pane = iota
	paneChat
	paneMembers
)

type hubEventMsg hub.Event
type historyMsg struct {
	kind     viewKind
	roomID   int64
	peerID   int64
	messages []domain.Message
}
type infoMsg struct {
	text string
	err  bool
}
type tickMsg struct{}
type disconnectMsg struct{}

// AppModel — the main 3-pane app.
type AppModel struct {
	deps Deps
	user *domain.User

	width, height int

	// nav
	rooms   []domain.Room
	dmPeers []*domain.User
	// peer cache by id
	userCache map[int64]*domain.User

	current       viewKind
	currentRoomID int64
	currentPeerID int64

	pane pane

	// chat state
	vp            viewport.Model
	composer      textarea.Model
	messages      map[string][]domain.Message // key: room:<id> or dm:<peerID>
	cmdHistory    []string
	cmdHistoryIdx int

	members []domain.Member
	online  map[int64]bool
	blocked map[int64]bool

	unreadRoom  map[int64]int
	unreadDM    map[int64]int
	mentionRoom map[int64]bool

	hubSub *hub.Sub

	statusLine string
	flashLine  string
	flashUntil time.Time

	help bool
}

func NewAppModel(d Deps, u *domain.User) *AppModel {
	ta := textarea.New()
	ta.Placeholder = "type a message…  /help for commands"
	ta.Prompt = "> "
	ta.CharLimit = domain.MaxBody
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.Focus()

	vp := viewport.New(0, 0)

	return &AppModel{
		deps:        d,
		user:        u,
		width:       d.Width,
		height:      d.Height,
		messages:    map[string][]domain.Message{},
		userCache:   map[int64]*domain.User{u.ID: u},
		online:      map[int64]bool{},
		blocked:     map[int64]bool{},
		unreadRoom:  map[int64]int{},
		unreadDM:    map[int64]int{},
		mentionRoom: map[int64]bool{},
		current:     viewNone,
		pane:        paneChat,
		vp:          vp,
		composer:    ta,
	}
}

func (m *AppModel) Init() tea.Cmd {
	ctx := context.Background()
	m.refreshRooms(ctx)
	m.refreshDMPeers(ctx)
	m.refreshBlocks(ctx)
	m.refreshUnread(ctx)

	// subscribe and join all rooms
	m.hubSub = m.deps.Hub.Subscribe(m.user.ID)
	for _, r := range m.rooms {
		m.deps.Hub.JoinRoom(m.hubSub, r.ID)
	}
	for _, id := range m.deps.Hub.OnlineUserIDs() {
		m.online[id] = true
	}

	// pick initial room
	if len(m.rooms) > 0 {
		m.openRoom(ctx, m.rooms[0].ID)
	}
	m.recalcLayout()
	return tea.Batch(m.listenHub(), m.tickCmd(), m.disconnectCmd())
}

func (m *AppModel) listenHub() tea.Cmd {
	sub := m.hubSub
	return func() tea.Msg {
		ev, ok := <-sub.Ch
		if !ok {
			return nil
		}
		return hubEventMsg(ev)
	}
}

func (m *AppModel) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *AppModel) disconnectCmd() tea.Cmd {
	done := m.deps.Done
	if done == nil {
		return nil
	}
	return func() tea.Msg {
		<-done
		return disconnectMsg{}
	}
}

func (m *AppModel) refreshRooms(ctx context.Context) {
	rs, err := m.deps.Store.ListUserRooms(ctx, m.user.ID)
	if err == nil {
		m.rooms = rs
	}
}

func (m *AppModel) refreshDMPeers(ctx context.Context) {
	ids, _ := m.deps.Store.DMPeers(ctx, m.user.ID)
	m.dmPeers = nil
	for _, id := range ids {
		u, err := m.cachedUser(ctx, id)
		if err == nil && u != nil {
			m.dmPeers = append(m.dmPeers, u)
		}
	}
}

func (m *AppModel) refreshBlocks(ctx context.Context) {
	b, _ := m.deps.Store.BlockedIDs(ctx, m.user.ID)
	m.blocked = b
}

func (m *AppModel) refreshUnread(ctx context.Context) {
	rooms, dms, _ := m.deps.Store.UnreadCounts(ctx, m.user.ID)
	m.unreadRoom = rooms
	m.unreadDM = dms
	m.mentionRoom = map[int64]bool{}
	for r, n := range rooms {
		if n > 0 {
			m.mentionRoom[r] = true
		}
	}
}

func (m *AppModel) cachedUser(ctx context.Context, id int64) (*domain.User, error) {
	if u, ok := m.userCache[id]; ok {
		return u, nil
	}
	u, err := m.deps.Store.UserByID(ctx, id)
	if err == nil {
		m.userCache[id] = u
	}
	return u, err
}

func (m *AppModel) recalcLayout() {
	w := m.width
	h := m.height
	if w < 80 || h < 24 {
		return
	}
	navW := 22
	memberW := 20
	chatW := w - navW - memberW - 6 // borders/padding
	if m.current == viewDM {
		chatW = w - navW - 4
	}
	if chatW < 30 {
		chatW = 30
	}
	composerH := 3
	chatH := h - composerH - 5 // title bar + borders + status bar
	if chatH < 5 {
		chatH = 5
	}
	m.vp.Width = chatW
	m.vp.Height = chatH
	m.composer.SetWidth(chatW)
}

func (m *AppModel) openRoom(ctx context.Context, roomID int64) {
	m.current = viewRoom
	m.currentRoomID = roomID
	m.currentPeerID = 0
	hist, _ := m.deps.Store.RoomHistoryForUser(ctx, m.user.ID, roomID, 200)
	m.messages[m.curKey()] = hist
	m.refreshMembers(ctx, roomID)
	_ = m.deps.Store.MarkRoomRead(ctx, m.user.ID, roomID)
	delete(m.unreadRoom, roomID)
	delete(m.mentionRoom, roomID)
	m.renderHistory()
}

func (m *AppModel) openDM(ctx context.Context, peerID int64) {
	m.current = viewDM
	m.currentRoomID = 0
	m.currentPeerID = peerID
	hist, _ := m.deps.Store.DMHistoryForUser(ctx, m.user.ID, peerID, 200)
	m.messages[m.curKey()] = hist
	_ = m.deps.Store.MarkDMRead(ctx, m.user.ID, peerID)
	delete(m.unreadDM, peerID)
	m.members = nil
	m.renderHistory()
}

func (m *AppModel) refreshMembers(ctx context.Context, roomID int64) {
	mem, err := m.deps.Store.RoomMembersForUser(ctx, m.user.ID, roomID)
	if err == nil {
		for i := range mem {
			mem[i].Online = m.online[mem[i].UserID]
		}
		m.members = mem
	}
}

func (m *AppModel) curKey() string {
	if m.current == viewRoom {
		return fmt.Sprintf("room:%d", m.currentRoomID)
	}
	if m.current == viewDM {
		return fmt.Sprintf("dm:%d", m.currentPeerID)
	}
	return ""
}

func (m *AppModel) renderHistory() {
	msgs := m.messages[m.curKey()]
	var lines []string
	for _, msg := range msgs {
		lines = append(lines, m.renderMessage(msg))
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.vp.GotoBottom()
}

func (m *AppModel) renderMessage(msg domain.Message) string {
	ts := stTimestamp.Render(msg.CreatedAt.Local().Format("15:04"))
	if msg.Deleted {
		return fmt.Sprintf("%s %s %s", ts, stHandle.Render(msg.Author), stMuted.Render("[deleted]"))
	}
	if m.blocked[msg.AuthorID] {
		return fmt.Sprintf("%s %s", ts, stMuted.Render("[blocked user]"))
	}
	body := highlightMentions(msg.Body, m.user.Handle)
	body = strings.ReplaceAll(body, "\n", "\n        ")
	idStr := stMuted.Render(fmt.Sprintf("#%d", msg.ID))
	return fmt.Sprintf("%s %s %s %s", ts, idStr, stHandle.Render(msg.Author), body)
}

func highlightMentions(body, selfHandle string) string {
	matches := store.ParseMentions(body)
	if len(matches) == 0 {
		return body
	}
	out := body
	for _, h := range matches {
		needle := "@" + h
		// case-insensitive replace
		var sb strings.Builder
		idx := 0
		lower := strings.ToLower(out)
		for {
			i := strings.Index(lower[idx:], needle)
			if i == -1 {
				sb.WriteString(out[idx:])
				break
			}
			abs := idx + i
			sb.WriteString(out[idx:abs])
			match := out[abs : abs+len(needle)]
			if strings.EqualFold(h, selfHandle) {
				sb.WriteString(stMention.Render(match))
			} else {
				sb.WriteString(stAccent.Render(match))
			}
			idx = abs + len(needle)
		}
		out = sb.String()
		lower = strings.ToLower(out)
	}
	return out
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.recalcLayout()
		m.renderHistory()

	case tickMsg:
		// refresh presence map for member list
		online := map[int64]bool{}
		for _, id := range m.deps.Hub.OnlineUserIDs() {
			online[id] = true
		}
		m.online = online
		for i := range m.members {
			m.members[i].Online = online[m.members[i].UserID]
		}
		cmds = append(cmds, m.tickCmd())

	case hubEventMsg:
		m.handleHubEvent(hub.Event(msg))
		cmds = append(cmds, m.listenHub())

	case disconnectMsg:
		m.cleanup()
		return m, tea.Quit

	case infoMsg:
		m.flashLine = msg.text
		if msg.err {
			m.flashLine = stError.Render("! " + msg.text)
		} else {
			m.flashLine = stOk.Render(msg.text)
		}
		m.flashUntil = time.Now().Add(4 * time.Second)

	case tea.KeyMsg:
		if m.help {
			m.help = false
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return m, m.quit()
		case "esc":
			if m.composer.Focused() {
				m.composer.Blur()
			} else {
				m.composer.Focus()
			}
			return m, nil
		case "?":
			if !m.composer.Focused() {
				m.help = true
				return m, nil
			}
		case "tab":
			m.pane = (m.pane + 1) % 3
			if m.pane == paneChat {
				m.composer.Focus()
			} else {
				m.composer.Blur()
			}
			return m, nil
		case "shift+tab":
			m.pane = (m.pane + 2) % 3
			if m.pane == paneChat {
				m.composer.Focus()
			} else {
				m.composer.Blur()
			}
			return m, nil
		case "pgup":
			m.vp.HalfViewUp()
			return m, nil
		case "pgdown":
			m.vp.HalfViewDown()
			return m, nil
		}

		if m.pane == paneNav {
			return m.updateNav(msg)
		}
		if m.pane == paneChat {
			return m.updateChat(msg)
		}
	}
	if m.pane == paneChat {
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *AppModel) updateNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.navMove(+1)
	case "k", "up":
		m.navMove(-1)
	case "enter":
		m.navActivate()
	}
	return m, nil
}

func (m *AppModel) navItems() []chanItem {
	var out []chanItem
	for _, r := range m.rooms {
		out = append(out, chanItem{kind: viewRoom, roomID: r.ID, label: "#" + r.Name,
			unread: m.unreadRoom[r.ID], mention: m.mentionRoom[r.ID]})
	}
	for _, p := range m.dmPeers {
		out = append(out, chanItem{kind: viewDM, peerID: p.ID, label: "@" + p.Handle,
			unread: m.unreadDM[p.ID], online: m.online[p.ID]})
	}
	return out
}

func (m *AppModel) currentNavIdx() int {
	items := m.navItems()
	for i, it := range items {
		if (it.kind == m.current) && ((it.kind == viewRoom && it.roomID == m.currentRoomID) || (it.kind == viewDM && it.peerID == m.currentPeerID)) {
			return i
		}
	}
	return 0
}

func (m *AppModel) navMove(d int) {
	items := m.navItems()
	if len(items) == 0 {
		return
	}
	i := (m.currentNavIdx() + d + len(items)) % len(items)
	m.activate(items[i])
}

func (m *AppModel) navActivate() {
	items := m.navItems()
	if len(items) == 0 {
		return
	}
	m.activate(items[m.currentNavIdx()])
}

func (m *AppModel) activate(it chanItem) {
	ctx := context.Background()
	if it.kind == viewRoom {
		m.openRoom(ctx, it.roomID)
	} else {
		m.openDM(ctx, it.peerID)
	}
}

func (m *AppModel) updateChat(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := SanitizeUserText(strings.TrimSpace(m.composer.Value()))
		if text == "" {
			return m, nil
		}
		m.composer.Reset()
		if strings.HasPrefix(text, "/") {
			cmd := m.runSlash(text)
			return m, cmd
		}
		m.cmdHistory = append(m.cmdHistory, text)
		m.cmdHistoryIdx = len(m.cmdHistory)
		return m, m.sendCurrent(text)
	case "up":
		if m.composer.Value() == "" && len(m.cmdHistory) > 0 {
			if m.cmdHistoryIdx > 0 {
				m.cmdHistoryIdx--
			}
			m.composer.SetValue(m.cmdHistory[m.cmdHistoryIdx])
			return m, nil
		}
	case "down":
		if m.composer.Value() != "" {
			break
		}
		if m.cmdHistoryIdx < len(m.cmdHistory)-1 {
			m.cmdHistoryIdx++
			m.composer.SetValue(m.cmdHistory[m.cmdHistoryIdx])
		} else {
			m.cmdHistoryIdx = len(m.cmdHistory)
			m.composer.Reset()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

func (m *AppModel) sendCurrent(body string) tea.Cmd {
	if m.deps.RateMsg != nil && !m.deps.RateMsg() {
		return flash("rate limit: slow down", true)
	}
	if len(body) > domain.MaxBody {
		body = body[:domain.MaxBody]
	}
	ctx := context.Background()
	switch m.current {
	case viewRoom:
		if ok, _, err := m.deps.Store.IsMember(ctx, m.currentRoomID, m.user.ID); err != nil {
			return flash("send failed: "+err.Error(), true)
		} else if !ok {
			return flash("not a member of this room", true)
		}
		handles := store.ParseMentions(body)
		ids, _ := m.deps.Store.ResolveHandles(ctx, handles)
		msg, notified, err := m.deps.Store.InsertRoomMessage(ctx, m.currentRoomID, m.user.ID, body, ids)
		if err != nil {
			return flash("send failed: "+err.Error(), true)
		}
		// Mark which notified members the room_msg event should *not* increment
		// the room unread for; the explicit notify event below owns that bump.
		mset := make(map[int64]bool, len(notified))
		for _, uid := range notified {
			mset[uid] = true
		}
		m.deps.Hub.PublishRoom(m.currentRoomID, hub.Event{Kind: "room_msg", RoomID: m.currentRoomID, Message: msg, MentionSet: mset})
		for _, uid := range notified {
			m.deps.Hub.PublishUser(uid, hub.Event{Kind: "notify", UserID: uid, Notif: &domain.Notification{Kind: "mention", RoomID: ptrI(m.currentRoomID)}})
		}
	case viewDM:
		if blocked, _ := m.deps.Store.IsBlocked(ctx, m.currentPeerID, m.user.ID); blocked {
			return flash("cannot DM this user", true)
		}
		msg, err := m.deps.Store.InsertDM(ctx, m.user.ID, m.currentPeerID, body)
		if err != nil {
			return flash("send failed: "+err.Error(), true)
		}
		m.deps.Hub.PublishUser(m.currentPeerID, hub.Event{Kind: "dm_msg", UserID: m.currentPeerID, PeerID: m.user.ID, Message: msg})
		m.deps.Hub.PublishUser(m.user.ID, hub.Event{Kind: "dm_msg", UserID: m.user.ID, PeerID: m.currentPeerID, Message: msg})
	}
	return nil
}

func ptrI(v int64) *int64 { return &v }

func (m *AppModel) handleHubEvent(ev hub.Event) {
	ctx := context.Background()
	switch ev.Kind {
	case "room_msg":
		key := fmt.Sprintf("room:%d", ev.RoomID)
		m.messages[key] = append(m.messages[key], *ev.Message)
		if m.current == viewRoom && m.currentRoomID == ev.RoomID {
			m.renderHistory()
			_ = m.deps.Store.MarkRoomRead(ctx, m.user.ID, ev.RoomID)
		} else if !ev.MentionSet[m.user.ID] {
			// the matching `notify` event will bump the count for mentioned
			// recipients, so don't double-count here
			m.unreadRoom[ev.RoomID]++
		}
	case "dm_msg":
		peer := ev.PeerID
		key := fmt.Sprintf("dm:%d", peer)
		m.messages[key] = append(m.messages[key], *ev.Message)
		// ensure peer is in list
		known := false
		for _, p := range m.dmPeers {
			if p.ID == peer {
				known = true
				break
			}
		}
		if !known {
			if u, err := m.cachedUser(ctx, peer); err == nil && u != nil {
				m.dmPeers = append([]*domain.User{u}, m.dmPeers...)
			}
		}
		if m.current == viewDM && m.currentPeerID == peer {
			m.renderHistory()
			_ = m.deps.Store.MarkDMRead(ctx, m.user.ID, peer)
		} else if ev.Message.AuthorID != m.user.ID {
			m.unreadDM[peer]++
		}
	case "msg_deleted":
		// rebuild current view
		if m.current == viewRoom {
			hist, _ := m.deps.Store.RoomHistoryForUser(ctx, m.user.ID, m.currentRoomID, 200)
			m.messages[m.curKey()] = hist
		} else if m.current == viewDM {
			hist, _ := m.deps.Store.DMHistoryForUser(ctx, m.user.ID, m.currentPeerID, 200)
			m.messages[m.curKey()] = hist
		}
		m.renderHistory()
	case "presence":
		m.online[ev.UserID] = ev.Online
		for i := range m.members {
			if m.members[i].UserID == ev.UserID {
				m.members[i].Online = ev.Online
			}
		}
	case "notify":
		if ev.Notif != nil && ev.Notif.RoomID != nil {
			m.mentionRoom[*ev.Notif.RoomID] = true
			m.unreadRoom[*ev.Notif.RoomID]++
		}
	case "room_meta":
		m.refreshRooms(ctx)
		if m.current == viewRoom && m.currentRoomID == ev.RoomID {
			m.refreshMembers(ctx, ev.RoomID)
		}
	case "room_removed":
		m.deps.Hub.LeaveRoom(m.hubSub, ev.RoomID)
		m.refreshRooms(ctx)
		delete(m.messages, fmt.Sprintf("room:%d", ev.RoomID))
		if m.current == viewRoom && m.currentRoomID == ev.RoomID {
			if len(m.rooms) > 0 {
				m.openRoom(ctx, m.rooms[0].ID)
			} else {
				m.current = viewNone
				m.currentRoomID = 0
				m.members = nil
				m.renderHistory()
			}
		}
	}
}

func (m *AppModel) View() string {
	if m.help {
		return m.viewHelp()
	}
	if m.width < 80 || m.height < 24 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stMuted.Render("please resize your terminal to at least 80×24"))
	}
	title := m.viewTitle()
	nav := m.viewNav()
	chat := m.viewChat()
	right := m.viewMembers()
	row := lipgloss.JoinHorizontal(lipgloss.Top, nav, chat, right)
	status := m.viewStatus()
	return lipgloss.JoinVertical(lipgloss.Left, title, row, status)
}

func (m *AppModel) viewTitle() string {
	return stTitleBar.Width(m.width).Render("Terminal Social")
}

func (m *AppModel) viewNav() string {
	w := 22
	h := m.vp.Height + m.composer.Height() + 2
	var lines []string
	header := "Rooms"
	if m.pane == paneNav {
		header = "▸ " + header
	}
	lines = append(lines, stTitle.Render(header))
	curIdx := m.currentNavIdx()
	items := m.navItems()
	for i, it := range items {
		if it.kind != viewRoom {
			continue
		}
		marker := " "
		if it.mention {
			marker = stMention.Render("•")
		}
		badge := ""
		if it.unread > 0 {
			badge = stMuted.Render(fmt.Sprintf(" %d", it.unread))
		}
		label := it.label + badge
		if i == curIdx && m.pane == paneNav {
			label = stSelected.Render("› " + label)
		} else if it.kind == m.current && it.roomID == m.currentRoomID {
			label = stAccent.Render("  " + label)
		} else {
			label = "  " + label
		}
		lines = append(lines, marker+label)
	}
	lines = append(lines, "", stTitle.Render("Messages"))
	for i, it := range items {
		if it.kind != viewDM {
			continue
		}
		marker := stOfflineDot.Render("○")
		if it.online {
			marker = stOnlineDot.Render("●")
		}
		badge := ""
		if it.unread > 0 {
			badge = stMention.Render(fmt.Sprintf(" %d", it.unread))
		}
		label := it.label + badge
		if i == curIdx && m.pane == paneNav {
			label = stSelected.Render("› " + label)
		} else if it.kind == m.current && it.peerID == m.currentPeerID {
			label = stAccent.Render("  " + label)
		} else {
			label = "  " + label
		}
		lines = append(lines, marker+label)
	}
	body := strings.Join(lines, "\n")
	return paneBorder(m.pane == paneNav).Width(w).Height(h).Render(body)
}

func (m *AppModel) viewChat() string {
	header := ""
	switch m.current {
	case viewRoom:
		if r, _ := m.deps.Store.RoomByID(context.Background(), m.currentRoomID); r != nil {
			header = stTitle.Render("#"+r.Name) + " " + stMuted.Render("— "+r.Topic)
		}
	case viewDM:
		if u, _ := m.cachedUser(context.Background(), m.currentPeerID); u != nil {
			dot := stOfflineDot.Render("○")
			if m.online[u.ID] {
				dot = stOnlineDot.Render("●")
			}
			header = dot + " " + stTitle.Render("@"+u.Handle)
		}
	default:
		header = stMuted.Render("no channel selected — Tab to nav, Enter to pick")
	}
	hist := m.vp.View()
	composer := m.composer.View()
	w := m.vp.Width
	if m.pane == paneChat {
		header = stAccent.Render("▸ ") + header
	}
	body := lipgloss.JoinVertical(lipgloss.Left, header, hist, composer)
	return paneBorder(m.pane == paneChat).Width(w).Render(body)
}

func (m *AppModel) viewMembers() string {
	if m.current != viewRoom {
		return ""
	}
	w := 20
	h := m.vp.Height + m.composer.Height() + 2
	online := []domain.Member{}
	offline := []domain.Member{}
	for _, mm := range m.members {
		if mm.Online {
			online = append(online, mm)
		} else {
			offline = append(offline, mm)
		}
	}
	sort.Slice(online, func(i, j int) bool { return online[i].Handle < online[j].Handle })
	sort.Slice(offline, func(i, j int) bool { return offline[i].Handle < offline[j].Handle })
	var lines []string
	memHeader := fmt.Sprintf("Members (%d)", len(m.members))
	if m.pane == paneMembers {
		memHeader = "▸ " + memHeader
	}
	lines = append(lines, stTitle.Render(memHeader))
	for _, u := range online {
		role := ""
		if u.Role == domain.RoleOwner {
			role = stMuted.Render(" ★")
		} else if u.Role == domain.RoleModerator {
			role = stMuted.Render(" ✦")
		}
		lines = append(lines, stOnlineDot.Render("●")+" "+u.Handle+role)
	}
	for _, u := range offline {
		lines = append(lines, stOfflineDot.Render("○")+" "+stMuted.Render(u.Handle))
	}
	return paneBorder(m.pane == paneMembers).Width(w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m *AppModel) viewStatus() string {
	paneName := "rooms"
	switch m.pane {
	case paneChat:
		paneName = "chat"
	case paneMembers:
		paneName = "members"
	}
	left := fmt.Sprintf("online as @%s  ·  pane: %s", m.user.Handle, stAccent.Render(paneName))
	right := "[tab] panes  [?] help  [/] cmd  [ctrl+c] quit"
	flash := ""
	if time.Now().Before(m.flashUntil) {
		flash = "  " + m.flashLine
	}
	return stStatusBar.Width(m.width).Render(left + "  |  " + right + flash)
}

func (m *AppModel) viewHelp() string {
	body := `terminal-social — help

Panes:
  Tab / Shift+Tab    cycle Rooms ↔ Chat ↔ Members
  In nav: j/k or ↑/↓ to move, Enter to open

Composer:
  Enter              send
  ↑ / ↓ (empty)      recall previous sent
  PgUp / PgDn        scroll history

Slash commands:
  /join <room>           join a public room
  /leave                 leave current room
  /create <room> [public|private]
  /invite <handle>       invite to private room
  /kick <handle>         (mod) remove from room
  /ban  <handle> [reason]
  /topic <text>          set room topic
  /msg  <handle> <text>  start/append a DM
  /whois <handle>
  /block <handle>  /unblock <handle>
  /report <message-id> <reason>
  /me <text>
  /search <query>        search current room
  /addkey <pubkey-line>  attach another SSH public key to your account
  /keys                  list keys on your account
  /profile <name>|<bio>|<pronouns>  edit profile
  /quit                  disconnect

press any key to dismiss`
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		stBorder.Padding(1, 2).Render(body))
}

func (m *AppModel) quit() tea.Cmd {
	m.cleanup()
	return tea.Quit
}

func (m *AppModel) cleanup() {
	if m.hubSub != nil {
		m.deps.Hub.Unsubscribe(m.hubSub)
		m.hubSub = nil
	}
	if m.deps.OnDisconnect != nil {
		m.deps.OnDisconnect()
	}
}

func flash(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return infoMsg{text: text, err: isErr} }
}
