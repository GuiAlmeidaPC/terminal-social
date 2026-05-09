# terminal-social — Specification

A community-focused social network delivered as a Terminal UI over SSH. Users connect with an SSH client, are authenticated by their public key, and interact with the application entirely in the terminal — no web frontend, no client install.

Inspired in delivery model by [terminal.shop](https://terminal.shop) (Wish + Bubble Tea).

---

## 1. Goals & Non-Goals

### Goals
- SSH is the only entry point. `ssh social.example.com -p 2222` drops the user straight into the TUI.
- Community-first: users gather in **rooms/channels**, not in a personal feed.
- **Hybrid messaging**: real-time delivery to connected users, full persistent history for everyone else.
- **Private messages (DMs)** between any two users.
- SSH public key = identity. No passwords. First connection registers.
- Single static Go binary, deployable to an existing VPS without disturbing OpenSSH (port 22), nginx (80/443), or other apps.

### Non-Goals (v1)
- No personal timeline/feed, follows, likes, reposts.
- No web UI, no REST/GraphQL API.
- No file/image uploads. Text only.
- No federation (ActivityPub etc.).
- No voice/video.
- No mobile/desktop client — the only client is `ssh`.

---

## 2. Deployment Constraints

The target VPS already runs:
- OpenSSH on port **22** (admin access — must remain untouched).
- nginx on **80/443** (other apps via HTTP reverse proxy).

### Resulting requirements
- App listens for SSH connections on a **dedicated non-22 port**, default **`2222`** (configurable).
- App listens **only on the SSH port** — no HTTP listener, no nginx integration needed.
- Runs as a non-root systemd service under the existing **`deploy`** user on the VPS.
- Data stored under `/var/lib/tsocial/` (db + host key), owned by `deploy`.
- Firewall must allow inbound TCP on the chosen port (e.g. `ufw allow 2222/tcp`).
- Service binds to `0.0.0.0:2222` (or `[::]:2222`); high port → no CAP_NET_BIND_SERVICE.
- Connection: `ssh user@host -p 2222`. The `user@` part is cosmetic — identity comes from the SSH public key.

---

## 3. Tech Stack

| Concern | Choice | Rationale |
|---|---|---|
| Language | **Go** (1.22+) | Single binary, strong stdlib, matches terminal.shop. |
| SSH server | **`github.com/charmbracelet/wish`** | Serves Bubble Tea programs per SSH session. |
| TUI framework | **`github.com/charmbracelet/bubbletea`** | Elm-architecture TUI. |
| Components | **`github.com/charmbracelet/bubbles`** | Textinput, viewport, list, etc. |
| Styling | **`github.com/charmbracelet/lipgloss`** | Layout + colors. |
| Storage | **SQLite** via `modernc.org/sqlite` | Pure Go, no CGO, single-file db, plenty for VPS scale. |
| Migrations | `goose` or hand-rolled SQL versioning | Embedded `.sql` files via `embed`. |
| Logging | `log/slog` | Structured, stdlib. |
| Config | env vars + flags | No config files needed. |

---

## 4. Authentication & Identity

- The user's **SSH public key fingerprint** (SHA256) is the primary identity key.
- On first connection with a new fingerprint:
  1. User is prompted to choose a **handle** (3–20 chars, `[a-z0-9_-]`, unique, case-insensitive).
  2. User account created, fingerprint stored.
- On subsequent connections, fingerprint → user lookup → straight into the app.
- A user may register **additional public keys** later (settings screen) so they can connect from multiple machines.
- No passwords, no email, no password reset. If a user loses all their keys, they lose the account (documented limitation for v1).
- The `ssh` user-part (`anything@host`) is ignored.

---

## 5. Core Features

### 5.1 Rooms (Channels)
- Named rooms, e.g. `#general`, `#golang`, `#random`.
- Room name: `[a-z0-9_-]`, 1–32 chars, unique.
- Two kinds:
  - **Public** — listed in directory, anyone can join.
  - **Private** — invite-only, not listed.
- Roles per room: `owner`, `moderator`, `member`.
- Any registered user can create a room (configurable rate limit; default 5/day per user).
- Owner can: rename, delete, set topic, invite, kick, ban, promote moderators, change public/private.
- Moderator can: kick, ban, delete messages.
- Topic: free-form string up to 200 chars, shown in room header.

### 5.2 Messaging — Hybrid (live + history)
- Messages sent to a room are **persisted** and **broadcast** to all currently connected members of that room.
- Disconnected users see them on next connect via history scroll-back.
- Message: plain UTF-8 text, max 2000 bytes. No markdown rendering in v1 (raw text + simple `@handle` highlighting).
- **No editing** in v1. Author and moderators may **delete** (soft-delete; tombstone shown as `[deleted]`).
- Per-room rate limit: e.g. 10 messages / 10 seconds per user (configurable).
- Mentions: `@handle` is detected and (a) highlighted in render, (b) generates a notification for the mentioned user (see §5.5).

### 5.3 Direct Messages (DMs)
- Any user can DM any other user by handle, unless the recipient has blocked the sender or has DMs set to "contacts only" (future; v1 = open).
- DMs are 1:1 only in v1 (no group DMs).
- Same hybrid model: live delivery if recipient is connected, persisted otherwise.
- Same size/rate limits as room messages.
- DM threads are listed in a "Messages" pane sorted by most recent activity, with unread counts.

### 5.4 Presence
- A user is "online" if they have ≥1 active SSH session.
- Online indicator shown in member list, DM list, and `@handle` lookup.
- No "typing…" indicator in v1.
- No last-seen timestamp exposed publicly in v1 (privacy default).

### 5.5 Notifications
- In-app only; no email/push (no email is collected).
- Triggered by: `@handle` mention in any room the user is in, new DM, room invite.
- Surfaced as: counter badge on relevant nav item + a "Notifications" view.
- Cleared when the user views the source.

### 5.6 User Profile
- Editable: display name (≤ 40 chars), bio (≤ 280 chars), pronouns (≤ 20 chars).
- Read-only: handle, join date, public-key fingerprints (last 8 hex chars shown).
- Viewable via `/whois <handle>` or selecting a user from a member list.

### 5.7 Blocking
- A user can block another user.
- Blocked user cannot DM them, and their messages in shared rooms are visually collapsed (`[blocked user]`, expandable).

### 5.8 Search & History
- Scroll-back in any room/DM is unlimited (subject to retention policy below).
- Per-room text search across that room's history (SQLite FTS5 if available; fallback `LIKE`).
- Global search across rooms the user is a member of.

### 5.9 Moderation (Site-wide)
- A small set of `admin` users (flagged in DB, set via CLI subcommand on the server).
- Admins can: suspend/unsuspend users, delete any message, delete any room, view reports.
- Any user can **report** a message or user with a reason; reports go into an admin queue.
- Suspended users may still SSH in but see only a notice screen.

### 5.10 Retention
- Messages retained indefinitely by default in v1.
- Soft-deleted messages purged after 30 days.
- Operator may run a retention job (e.g. trim rooms beyond N days) via CLI.

---

## 6. UX / TUI Layout

Inspired by IRC clients (weechat, irssi) and Discord's terminal feel.

### 6.1 Main screen (3-pane)

```
┌──────────────┬───────────────────────────────────────────────┬───────────────┐
│ Rooms        │ #general — topic: be excellent to each other  │ Members (12)  │
│  #general  • │                                               │  • alice      │
│  #golang     │ 14:02 alice  hey folks                        │  • bob        │
│  #random   3 │ 14:03 bob    morning ☀                        │    carol      │
│              │ 14:04 carol  @alice did you see the PR?       │    dave       │
│ Messages     │ 14:05 alice  yes — looks great                │ ...           │
│  bob       1 │                                               │               │
│  carol       │                                               │               │
│              │                                               │               │
│ [n]ew  [/]   │ > _                                           │               │
└──────────────┴───────────────────────────────────────────────┴───────────────┘
 status: online as @gui   |   [tab] switch pane   [?] help   [q] quit
```

- Left pane: room list + DM list (with unread badges, `•` = mention, number = unread count).
- Center pane: message history (viewport) + composer at bottom.
- Right pane: member list of current room (hidden in DMs).
- Status bar: own handle, key hints, connection state.

### 6.2 Keybindings (default)
| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | Cycle panes |
| `Ctrl+N` | New: room / DM (modal) |
| `Ctrl+K` | Quick-switch (fuzzy room/DM/user) |
| `/` | Slash command |
| `Enter` | Send message |
| `Shift+Enter` | Newline in composer |
| `Up`/`Down` in composer (empty) | Recall previous sent messages |
| `PgUp`/`PgDn` | Scroll history |
| `?` | Help overlay |
| `Ctrl+C` / `q` (when not composing) | Quit |

### 6.3 Slash commands
| Command | Effect |
|---|---|
| `/join <#room>` | Join public room |
| `/leave` | Leave current room |
| `/create <#room> [public|private]` | Create room |
| `/invite <@handle>` | Invite to current (private) room |
| `/kick <@handle> [reason]` | Mod action |
| `/ban <@handle> [reason]` | Mod action |
| `/topic <text>` | Set room topic |
| `/msg <@handle> <text>` | Open/append DM |
| `/whois <@handle>` | Show profile |
| `/block <@handle>` | Block user |
| `/unblock <@handle>` | Unblock user |
| `/report <message-id> <reason>` | Report content |
| `/me <text>` | Action message |
| `/quit` | Disconnect |

### 6.4 First-run flow
1. Connect via SSH.
2. Welcome screen: terms + handle prompt.
3. On submit → account created → main screen, auto-joined to `#general`.

### 6.5 Resizing & terminal compatibility
- Min terminal: **80x24**. Below this, show "please resize" notice.
- Adapt to wider terminals by expanding center pane.
- Detect color support via `lipgloss`; degrade to monochrome on dumb terminals.
- Unicode assumed; emoji rendered as terminal allows.

---

## 7. Data Model (SQLite)

```sql
-- users
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  handle        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name  TEXT NOT NULL DEFAULT '',
  bio           TEXT NOT NULL DEFAULT '',
  pronouns      TEXT NOT NULL DEFAULT '',
  is_admin      INTEGER NOT NULL DEFAULT 0,
  is_suspended  INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  last_seen_at  INTEGER
);

-- one user → many SSH public keys
CREATE TABLE user_keys (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  fingerprint     TEXT NOT NULL UNIQUE,   -- SHA256:base64
  public_key      TEXT NOT NULL,          -- authorized_keys-style
  label           TEXT NOT NULL DEFAULT '',
  added_at        INTEGER NOT NULL
);

-- rooms
CREATE TABLE rooms (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  topic       TEXT NOT NULL DEFAULT '',
  is_private  INTEGER NOT NULL DEFAULT 0,
  created_by  INTEGER NOT NULL REFERENCES users(id),
  created_at  INTEGER NOT NULL,
  deleted_at  INTEGER
);

-- membership + role
CREATE TABLE room_members (
  room_id    INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK(role IN ('owner','moderator','member')),
  joined_at  INTEGER NOT NULL,
  last_read_msg_id INTEGER,
  PRIMARY KEY (room_id, user_id)
);

CREATE TABLE room_bans (
  room_id    INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  banned_by  INTEGER NOT NULL REFERENCES users(id),
  reason     TEXT NOT NULL DEFAULT '',
  banned_at  INTEGER NOT NULL,
  PRIMARY KEY (room_id, user_id)
);

-- messages: room messages and DMs share this table, distinguished by target
CREATE TABLE messages (
  id           INTEGER PRIMARY KEY,
  kind         TEXT NOT NULL CHECK(kind IN ('room','dm')),
  room_id      INTEGER REFERENCES rooms(id) ON DELETE CASCADE, -- NULL for DM
  dm_peer_a    INTEGER REFERENCES users(id),                   -- DM only; min(user_id)
  dm_peer_b    INTEGER REFERENCES users(id),                   -- DM only; max(user_id)
  author_id    INTEGER NOT NULL REFERENCES users(id),
  body         TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  deleted_at   INTEGER,
  deleted_by   INTEGER REFERENCES users(id)
);
CREATE INDEX idx_messages_room ON messages(room_id, id);
CREATE INDEX idx_messages_dm   ON messages(dm_peer_a, dm_peer_b, id);

-- mentions denormalized for fast notification lookup
CREATE TABLE mentions (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (message_id, user_id)
);

-- block list
CREATE TABLE blocks (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, blocked_id)
);

-- notifications (small, per-user)
CREATE TABLE notifications (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK(kind IN ('mention','dm','invite')),
  message_id INTEGER REFERENCES messages(id) ON DELETE CASCADE,
  room_id    INTEGER REFERENCES rooms(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  read_at    INTEGER
);

-- moderation reports
CREATE TABLE reports (
  id          INTEGER PRIMARY KEY,
  reporter_id INTEGER NOT NULL REFERENCES users(id),
  target_kind TEXT NOT NULL CHECK(target_kind IN ('message','user')),
  target_id   INTEGER NOT NULL,
  reason      TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  resolved_at INTEGER,
  resolved_by INTEGER REFERENCES users(id)
);

-- optional: FTS5 virtual table mirroring messages.body for search
CREATE VIRTUAL TABLE messages_fts USING fts5(body, content='messages', content_rowid='id');
```

DM canonicalization: store `dm_peer_a = MIN(u1, u2)`, `dm_peer_b = MAX(u1, u2)` so a thread is one stable key.

---

## 8. Architecture

### 8.1 Process model
Single Go process, three concurrent layers:

1. **SSH front (Wish)** — accepts connections, authenticates via pubkey, spawns a Bubble Tea program per session.
2. **Hub (in-memory pub/sub)** — central goroutine that routes events between sessions:
   - `RoomMessage`, `DMMessage`, `Presence`, `Mention`, `Notification`.
   - Each session subscribes to topics it cares about (its rooms, its DMs, its user-id).
3. **Store (SQLite)** — persistence behind a small repo layer; writes are serialized through a single writer goroutine to avoid SQLITE_BUSY.

Event flow on send:
```
session → Store.Insert(message) → Hub.Publish(event) → fan-out to subscribed sessions
```

### 8.2 Package layout
```
cmd/
  tsocial/        # main: flags, config, wire-up
  tsocial-admin/  # CLI: create-admin, suspend, retention, etc.
internal/
  ssh/            # Wish server, pubkey → user resolution, session bootstrap
  tui/            # Bubble Tea models: app, room view, dm view, modals
    components/   # composer, message list, member list, etc.
  hub/            # in-memory pub/sub
  store/          # sqlite repo + migrations (embed)
  domain/         # types: User, Room, Message, ...
  auth/           # registration flow, fingerprint helpers
  rate/           # rate limiters
  config/         # env + flags
migrations/       # *.sql, embedded
```

### 8.3 Concurrency rules
- One **writer** goroutine for SQLite, fed by a buffered channel; readers use a separate connection pool.
- Hub is a single goroutine owning subscriber maps; communication via channels only.
- Per-session goroutine handles its Bubble Tea loop and a subscription channel; on disconnect, unsubscribe + presence update.

### 8.4 Graceful shutdown
- SIGTERM → stop accepting new SSH connections → notify sessions ("server restarting") → drain hub → close DB.

---

## 9. Configuration

Env vars (override with flags of the same name lowercased):

| Variable | Default | Description |
|---|---|---|
| `TSOCIAL_LISTEN` | `0.0.0.0:2222` | SSH bind address |
| `TSOCIAL_HOST_KEY` | `/var/lib/tsocial/host_key` | SSH host key path; auto-generated (ed25519) if missing |
| `TSOCIAL_DB` | `/var/lib/tsocial/tsocial.db` | SQLite path |
| `TSOCIAL_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `TSOCIAL_MAX_SESSIONS` | `500` | Hard cap on concurrent SSH sessions |
| `TSOCIAL_RATE_MSG_PER_10S` | `10` | Per-user message rate limit |
| `TSOCIAL_DEFAULT_ROOM` | `general` | Auto-join on registration |

---

## 10. Security

- **Host key**: ed25519, generated on first run, persisted at `TSOCIAL_HOST_KEY`. Treat as sensitive.
- **Pubkey-only auth**: password auth disabled in Wish config. No interactive keyboard auth.
- **No shell, no exec, no port-forward, no SFTP**: Wish session must reject `exec`/`subsystem` requests; only the PTY-attached TUI is served.
- **Input validation**: handle and room name regex enforced server-side. Body capped at 2000 bytes; reject control chars except `\n`, `\t`.
- **Rate limits**: per-connection (auth attempts), per-user (messages, room creation), per-IP (new connections / minute) — all in-memory token buckets.
- **Resource limits**: max sessions cap (drop new conns over cap with a polite message). Per-session memory bounded by viewport ring buffer.
- **SQL**: parameterized queries only.
- **TUI rendering**: strip / escape ANSI control sequences from user-supplied content before rendering, to prevent terminal injection.
- **Logging**: never log message bodies; log fingerprints, user ids, event types, durations.
- **Backup**: operator-run `sqlite3 .backup`; doc'd in README.
- **No telemetry**.

---

## 11. Operations

### 11.1 Build & run
```bash
go build -o tsocial ./cmd/tsocial
./tsocial   # honors TSOCIAL_* env vars
```

### 11.2 systemd unit (sketch)
```ini
[Unit]
Description=terminal_social
After=network.target

[Service]
User=deploy
Group=deploy
ExecStart=/usr/local/bin/tsocial
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/tsocial
PrivateTmp=true
Environment=TSOCIAL_LISTEN=0.0.0.0:2222
Environment=TSOCIAL_DB=/var/lib/tsocial/tsocial.db
Environment=TSOCIAL_HOST_KEY=/var/lib/tsocial/host_key

[Install]
WantedBy=multi-user.target
```

### 11.3 Firewall
```
ufw allow 2222/tcp
```
Port 22 stays as-is for admin.

### 11.4 Admin CLI (`tsocial-admin`)
- `make-admin <handle>`
- `suspend <handle> [--reason]` / `unsuspend <handle>`
- `delete-room <name>`
- `prune-deleted --older-than 30d`
- `stats` (users, rooms, messages, online)

All operate directly on the SQLite file; safe to run while server is up (WAL mode).

### 11.5 Backups
- SQLite in WAL mode.
- Cron: `sqlite3 /var/lib/tsocial/tsocial.db ".backup /backups/tsocial-$(date +\%F).db"` daily.

---

## 12. Testing Strategy

- **Unit**: store repos (against in-memory SQLite), domain validation, rate limiters, mention parsing, ANSI stripping.
- **Integration**: spin up the full server on an ephemeral port, connect with `golang.org/x/crypto/ssh` as a client, drive scripted scenarios (register, join, send, DM, mention, block).
- **TUI**: snapshot tests of Bubble Tea views via `teatest`.
- **Load**: a small harness that opens N SSH sessions and posts messages to measure broadcast latency and DB write throughput.

---

## 13. Milestones

1. **M1 — Skeleton**: Wish server on 2222, pubkey echo, "hello @handle" Bubble Tea screen.
2. **M2 — Identity**: registration flow, SQLite store, multiple keys per user.
3. **M3 — Rooms & messages**: create/join/leave, persistent history, hybrid live broadcast via Hub.
4. **M4 — DMs**: 1:1 messaging with same hybrid model.
5. **M5 — Mentions, notifications, presence**.
6. **M6 — Moderation**: roles, kick/ban, reports, admin CLI.
7. **M7 — Polish**: search (FTS5), quick-switcher, help overlay, resize handling, color themes.
8. **M8 — Hardening**: rate limits, ANSI sanitization, load test, systemd packaging, backup docs.

---

## 14. Open Questions (to revisit)

- Group DMs / threads — defer to v2?
- Markdown or limited formatting (`*bold*`, `` `code` ``) — nice to have; design must keep TUI rendering simple.
- Public-room directory pagination/sort — by activity vs alphabetical.
- Whether to expose `last_seen` even to room co-members.
- Anti-abuse: CAPTCHA-equivalent for new account creation isn't really possible over SSH; rely on per-IP rate limit + manual admin tools.
