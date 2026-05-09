package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

type User struct {
	ID          int64
	Handle      string
	DisplayName string
	Bio         string
	Pronouns    string
	IsAdmin     bool
	IsSuspended bool
	CreatedAt   time.Time
	LastSeenAt  *time.Time
}

type UserKey struct {
	ID          int64
	UserID      int64
	Fingerprint string
	PublicKey   string
	Label       string
	AddedAt     time.Time
}

type Room struct {
	ID        int64
	Name      string
	Topic     string
	IsPrivate bool
	CreatedBy int64
	CreatedAt time.Time
}

type Member struct {
	UserID   int64
	Handle   string
	Role     string
	JoinedAt time.Time
	Online   bool
}

type Message struct {
	ID        int64
	Kind      string // room | dm
	RoomID    *int64
	DMPeerA   *int64
	DMPeerB   *int64
	AuthorID  int64
	Author    string // handle
	Body      string
	CreatedAt time.Time
	Deleted   bool
}

type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	MessageID *int64
	RoomID    *int64
	CreatedAt time.Time
	Read      bool
}

const (
	MaxBody        = 2000
	MaxHandleLen   = 20
	MaxRoomNameLen = 32
	MaxTopicLen    = 200
	MaxDisplayName = 40
	MaxBio         = 280
	MaxPronouns    = 20
	RoleOwner      = "owner"
	RoleModerator  = "moderator"
	RoleMember     = "member"
)

var (
	handleRe = regexp.MustCompile(`^[a-z0-9_-]{3,20}$`)
	roomRe   = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

func ValidHandle(h string) bool   { return handleRe.MatchString(h) }
func ValidRoomName(n string) bool { return roomRe.MatchString(n) }

// DMKey returns canonical (a, b) with a < b.
func DMKey(u1, u2 int64) (int64, int64) {
	if u1 < u2 {
		return u1, u2
	}
	return u2, u1
}

// SanitizeUserText strips terminal control sequences and disallowed control
// chars from user-provided text before persistence/rendering. Allows \n and \t.
func SanitizeUserText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b {
			i = skipEscape(runes, i)
			continue
		}
		if r == 0x9b {
			i = skipControlSequence(runes, i+1)
			continue
		}
		if r >= 0x80 && r <= 0x9f {
			continue
		}
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), "\n\t ")
}

func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	i++
	switch runes[i] {
	case '[':
		i++
		for i < len(runes) {
			r := runes[i]
			if r >= 0x40 && r <= 0x7e {
				return i
			}
			i++
		}
		return len(runes) - 1
	case ']', 'P', '^', '_':
		return skipStringControl(runes, i+1)
	case '(', ')':
		if i+1 < len(runes) {
			return i + 1
		}
		return i
	default:
		return i
	}
}

func skipStringControl(runes []rune, i int) int {
	for i < len(runes) {
		if runes[i] == 0x07 {
			return i
		}
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '\\' {
			return i + 1
		}
		i++
	}
	return len(runes) - 1
}

func skipControlSequence(runes []rune, i int) int {
	for i < len(runes) {
		r := runes[i]
		if r >= 0x40 && r <= 0x7e {
			return i
		}
		i++
	}
	return len(runes) - 1
}
