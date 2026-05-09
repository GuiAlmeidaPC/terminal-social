package hub

import (
	"sync"

	"github.com/tsocial/tsocial/internal/domain"
)

// Event is anything broadcast to subscribers. Subscribers receive messages
// for rooms they joined and DMs addressed to them, plus presence updates.
type Event struct {
	Kind    string // "room_msg" | "dm_msg" | "msg_deleted" | "presence" | "notify" | "room_meta" | "room_removed"
	RoomID  int64
	UserID  int64 // for presence/notify or DM target
	PeerID  int64 // for DM (other party)
	Message *domain.Message
	Online  bool // for presence
	Notif   *domain.Notification
	// MentionSet, when set on a room_msg event, lists the user IDs the message
	// also produced an explicit `notify` event for. Those recipients should
	// rely on the notify event for their unread bump and skip it on this
	// event to avoid double-counting.
	MentionSet map[int64]bool
}

type Sub struct {
	UserID int64
	Ch     chan Event
}

type Hub struct {
	mu          sync.RWMutex
	rooms       map[int64]map[*Sub]struct{} // room id -> subs
	users       map[int64]map[*Sub]struct{} // user id -> subs (sessions)
	presenceCnt map[int64]int               // active session count per user
}

func New() *Hub {
	return &Hub{
		rooms:       map[int64]map[*Sub]struct{}{},
		users:       map[int64]map[*Sub]struct{}{},
		presenceCnt: map[int64]int{},
	}
}

func (h *Hub) Subscribe(userID int64) *Sub {
	s := &Sub{UserID: userID, Ch: make(chan Event, 64)}
	h.mu.Lock()
	if h.users[userID] == nil {
		h.users[userID] = map[*Sub]struct{}{}
	}
	h.users[userID][s] = struct{}{}
	h.presenceCnt[userID]++
	first := h.presenceCnt[userID] == 1
	h.mu.Unlock()
	if first {
		h.broadcastPresence(userID, true)
	}
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	for rid, set := range h.rooms {
		if _, ok := set[s]; ok {
			delete(set, s)
			if len(set) == 0 {
				delete(h.rooms, rid)
			}
		}
	}
	if set, ok := h.users[s.UserID]; ok {
		delete(set, s)
		if len(set) == 0 {
			delete(h.users, s.UserID)
		}
	}
	h.presenceCnt[s.UserID]--
	last := h.presenceCnt[s.UserID] <= 0
	if last {
		delete(h.presenceCnt, s.UserID)
	}
	h.mu.Unlock()
	close(s.Ch)
	if last {
		h.broadcastPresence(s.UserID, false)
	}
}

func (h *Hub) JoinRoom(s *Sub, roomID int64) {
	h.mu.Lock()
	if h.rooms[roomID] == nil {
		h.rooms[roomID] = map[*Sub]struct{}{}
	}
	h.rooms[roomID][s] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) LeaveRoom(s *Sub, roomID int64) {
	h.mu.Lock()
	if set, ok := h.rooms[roomID]; ok {
		delete(set, s)
		if len(set) == 0 {
			delete(h.rooms, roomID)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) RemoveUserFromRoom(userID, roomID int64) {
	h.mu.Lock()
	var subs []*Sub
	if set, ok := h.rooms[roomID]; ok {
		for s := range set {
			if s.UserID == userID {
				delete(set, s)
				subs = append(subs, s)
			}
		}
		if len(set) == 0 {
			delete(h.rooms, roomID)
		}
	}
	h.mu.Unlock()
	ev := Event{Kind: "room_removed", RoomID: roomID, UserID: userID}
	for _, s := range subs {
		send(s, ev)
	}
}

func (h *Hub) IsOnline(userID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.presenceCnt[userID] > 0
}

func (h *Hub) OnlineUserIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]int64, 0, len(h.presenceCnt))
	for id := range h.presenceCnt {
		out = append(out, id)
	}
	return out
}

func (h *Hub) PublishRoom(roomID int64, ev Event) {
	h.mu.RLock()
	subs := make([]*Sub, 0, len(h.rooms[roomID]))
	for s := range h.rooms[roomID] {
		subs = append(subs, s)
	}
	h.mu.RUnlock()
	for _, s := range subs {
		send(s, ev)
	}
}

// PublishUser delivers an event to all sessions of a single user (e.g. DM target, notifications).
func (h *Hub) PublishUser(userID int64, ev Event) {
	h.mu.RLock()
	subs := make([]*Sub, 0, len(h.users[userID]))
	for s := range h.users[userID] {
		subs = append(subs, s)
	}
	h.mu.RUnlock()
	for _, s := range subs {
		send(s, ev)
	}
}

func (h *Hub) broadcastPresence(userID int64, online bool) {
	// Deliver presence to everyone currently subscribed to a room.
	// Cheap & effective: clients filter by membership.
	h.mu.RLock()
	seen := map[*Sub]struct{}{}
	for _, set := range h.rooms {
		for s := range set {
			seen[s] = struct{}{}
		}
	}
	h.mu.RUnlock()
	ev := Event{Kind: "presence", UserID: userID, Online: online}
	for s := range seen {
		send(s, ev)
	}
}

func send(s *Sub, ev Event) {
	select {
	case s.Ch <- ev:
	default:
		// drop if subscriber is slow; client will see catch-up on next reload
	}
}
