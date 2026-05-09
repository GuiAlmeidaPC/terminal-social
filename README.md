# terminal-social

A community-first social network delivered as a Terminal UI over SSH.
Users connect with their SSH client, are authenticated by their public key,
and interact entirely in the terminal — no web frontend, no client install.

```
ssh anything@your-host -p 2222
```

Inspired by [terminal.shop](https://terminal.shop). Built with
[wish](https://github.com/charmbracelet/wish), [bubbletea](https://github.com/charmbracelet/bubbletea),
and SQLite (pure Go via `modernc.org/sqlite`).

See [SPEC.md](./SPEC.md) for the full design.

## Build

```
go build -o tsocial ./cmd/tsocial
go build -o tsocial-admin ./cmd/tsocial-admin
```

Single static binary, no CGO.

## Run (dev)

```
TSOCIAL_LISTEN=127.0.0.1:2222 \
TSOCIAL_HOST_KEY=./dev-host-key \
TSOCIAL_DB=./tsocial.db \
./tsocial
```

Then in another terminal:

```
ssh me@127.0.0.1 -p 2222
```

First connection prompts for a handle; subsequent connections drop you straight
into the TUI.

## Configuration

| Env var | Default | Description |
|---|---|---|
| `TSOCIAL_LISTEN` | `0.0.0.0:2222` | SSH bind address |
| `TSOCIAL_HOST_KEY` | `/var/lib/tsocial/host_key` | Host key path (auto-generated ed25519 if missing) |
| `TSOCIAL_DB` | `/var/lib/tsocial/tsocial.db` | SQLite path |
| `TSOCIAL_LOG_LEVEL` | `info` | debug / info / warn / error |
| `TSOCIAL_MAX_SESSIONS` | `500` | concurrent SSH session cap |
| `TSOCIAL_RATE_MSG_PER_10S` | `10` | per-user message rate |
| `TSOCIAL_DEFAULT_ROOM` | `general` | room auto-joined on registration |

Each env var has a matching `-flag` (lowercased).

## Deployment (Linux VPS, port 2222 alongside OpenSSH)

The app does not touch port 22, nginx, or any HTTP. It binds only to its own
high port.

```bash
# 1. user/data dir (uses existing deploy user)
sudo install -d -m 0700 -o deploy -g deploy /var/lib/tsocial

# 2. binary
sudo install -m 0755 -o root -g root tsocial /usr/local/bin/tsocial

# 3. systemd
sudo cp systemd/tsocial.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now tsocial

# 4. firewall
sudo ufw allow 2222/tcp
```

### Subsequent deploys

Use the bundled `deploy.sh`: it cross-compiles, copies the binary, swaps it,
and restarts the systemd unit. Schema migrations are idempotent so a
restart-in-place is safe.

```bash
VPS=deploy@your-vps ./deploy.sh           # ship just the server
VPS=deploy@your-vps ./deploy.sh admin     # also refresh tsocial-admin
ARCH=arm64 VPS=deploy@your-vps ./deploy.sh   # ARM VPSes
```

## Admin

```
tsocial-admin make-admin <handle>
tsocial-admin suspend <handle>
tsocial-admin delete-room <name>
tsocial-admin prune-deleted --older-than-days 30
tsocial-admin stats
```

Safe to run while the server is up (SQLite WAL mode).

## Backup

```
sqlite3 /var/lib/tsocial/tsocial.db ".backup /backups/tsocial-$(date +%F).db"
```

## Slash commands (in the TUI)

```
/join <room>     /leave           /create <room> [public|private]
/topic <text>    /invite <handle> /kick <handle>  /ban <handle> [reason]
/msg <handle>    /whois <handle>  /block <handle> /unblock <handle>
/report <id> <reason>             /delete <id>
/me <text>       /search <query>  /profile name|bio|pronouns
/notifications   /help            /quit
```

## Security

- pubkey auth only, no passwords, no keyboard-interactive
- exec / subsystem / port-forward all rejected; PTY-only
- terminal control sequences stripped from user input before storage
- per-user message rate limit, per-IP connect rate limit
- parameterized SQL only
- never logs message bodies
- room history/search/members/message actions are checked against membership
- room sends are blocked for non-members and banned users
- DMs honor recipient block lists before persistence
- kicked/banned online sessions are removed from the room subscription immediately
- normal TUI quits and SSH disconnects run session cleanup to release presence/session counts
- `/var/lib/tsocial` should be mode `0700`; the packaged systemd unit applies additional sandboxing

## Hardening changes

This implementation includes a security hardening pass beyond the initial spec:

- Store-level authorization wrappers were added for room history, search, member lists, message lookup, deletion, reports, room sends, and DMs. The TUI uses these guarded paths so access rules are not only UI state.
- User-controlled text is sanitized at persistence boundaries for messages, profiles, topics, report reasons, and ban reasons. The sanitizer strips ESC and C1 terminal controls.
- Per-IP connection limiting now keys on the remote host address instead of `ip:port`, and idle limiter buckets are pruned.
- SSH session cleanup is idempotent and wired to disconnect signals for app, registration, and suspended-account screens.
- The hub can remove all active sessions for a user from a room on kick/ban.
- Tests cover room membership enforcement, bans, block-protected DMs, report visibility, persisted text sanitization, and remote-IP extraction.
