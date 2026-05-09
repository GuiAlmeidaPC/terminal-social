# FIXES

Follow-up to the post-implementation review. Each section maps to one of the
prioritized items called out at the end of the review.

## 1. Double-counting unread on mentions

**Symptom.** When a user A mentioned user B (who was online and a member of the
room but not currently viewing it), B's room unread badge incremented by 2:
once via the `room_msg` hub event and once via the explicit `notify` event.
Initial-load counts from the DB were correct, so the drift only manifested
during a live session.

**Fix.** The hub `Event` now carries a `MentionSet map[int64]bool` — the
set of user IDs that the same room send also produced an explicit `notify`
event for. The `room_msg` handler skips its unread bump for any user whose ID
appears in that set, leaving the `notify` event as the single source of truth
for mention bumps. Non-mention recipients still bump on `room_msg` as before.

Files: `internal/hub/hub.go`, `internal/tui/app.go`.

## 2. Mention publishes leaked to non-members

**Symptom.** `sendCurrent` previously called
`Hub.PublishUser(uid, kind: "notify", room: X)` for every mentioned user the
store resolved by handle, *even if that user was not a member of room X*.
Combined with the unread bump in the `notify` handler, mentioning `@alice` in
a room she was not in caused her client to display a non-zero unread badge for
that room — leaking the room's existence and name (after a fresh
`refreshRooms`, the badge would dangle).

**Fix.** `Store.InsertRoomMessage` now returns the actually-notified IDs (it
already filtered by membership when writing the `mentions` and `notifications`
rows; that filtered slice is now plumbed back to the caller). The TUI passes
that slice — not the raw resolved-handle IDs — to both the `MentionSet` of the
room broadcast and the per-user `notify` publish. Self-mentions are also
skipped in the store. The signature changed from
`InsertRoomMessage(...) (*Message, error)` to
`InsertRoomMessage(...) (*Message, []int64, error)`. All callers and tests
updated.

Files: `internal/store/store.go`, `internal/tui/app.go`,
`internal/store/store_test.go`. New test:
`TestMentionsOnlyForRoomMembers`.

## 3. Admin couldn't delete messages outside their rooms

**Symptom.** `/delete <id>` first called `MessageByIDForUser`, which always
runs `canReadMessage` — a non-member of a private room could not even *read*
the message via that path, so an admin who had not joined the room got a
`not found` flash, contradicting the spec's "Admins can: ... delete any
message".

**Fix.** `/delete` now branches on `IsAdmin`: admins go through the un-guarded
`MessageByID` to fetch the row for the post-delete `room_meta`/`msg_deleted`
broadcast; non-admins go through the same visibility-checked path as before.
The actual delete still flows through `DeleteMessageForUser` which already
understands `isAdmin` and handles the authorization correctly.

Files: `internal/tui/commands.go`. New test:
`TestAdminCanDeleteOutsideRoom`.

## 4. Stray scratch file and dead fields

**Symptom.** A debug `test_sanitize.go` (`package main`) sat at the repo root
and was being picked up by `go build ./...`. Two struct fields were dead:
`AppModel.deps2 *deps2` (and the `deps2` type itself) and `RegisterModel.done
bool`.

**Fix.** Deleted the scratch file and both dead fields. Added a minimal
`.gitignore` covering the binary outputs and SQLite/host-key state files so
that future smoke runs don't pollute the working tree.

Files removed: `test_sanitize.go`. Modified:
`internal/tui/app.go`, `internal/tui/register.go`. Added: `.gitignore`.

## 5. Per-user message rate limit was actually per-session

**Symptom.** `internal/ssh/server.go` allocated a fresh `rate.Bucket` per Wish
session (`RateMsg: rate.New(...)`). A user with N concurrent SSH sessions
therefore got N× the configured throughput, which contradicts the spec's
"per-user 10 msgs / 10s" requirement.

**Fix.** The server now owns a single `*rate.Limiter` (`s.msgRate`)
constructed at startup with the configured capacity/window, keyed on user-id
once the SSH session has resolved a fingerprint to a user, or on the
fingerprint itself for a not-yet-registered first-time connection. The TUI
receives a `func() bool` (`tui.RateAllow`) instead of a concrete `*Bucket`,
so the layering is also cleaner: TUI no longer imports `internal/rate`.

Files: `internal/ssh/server.go`, `internal/tui/app.go`.

## 6. /addkey was a stub — multi-key UX is now real

**Symptom.** The `/addkey` command was a flash message telling users to "ssh
in from new key once and ask an admin." There was no in-app way to attach
additional SSH public keys, and the registration screen would happily create
a *new* account for a returning user who happened to be using a different
key — a real account-confusion vector.

**Fix.** Implemented a proper `/addkey <pubkey-line>` flow.

The user pastes a complete authorized_keys-style line (`ssh-ed25519 AAAA…
optional-comment`) into the composer. The command:
1. Parses the line via `auth.ParseAuthorizedKey` (uses
   `golang.org/x/crypto/ssh.ParseAuthorizedKey`).
2. Computes the SHA256 fingerprint and rejects the input if the same
   fingerprint is already attached to *any* user (so a key cannot bind to two
   accounts, and the duplicate message is informative if it's already on
   yours).
3. Persists the key via `Store.AddKey`. The optional comment field of the
   pasted line is used as the `label` in `user_keys` so users can tell
   "laptop" from "workstation" later.

A companion `/keys` command lists the keys currently attached to your
account, marking the one you connected with for this session with `*`.

Files: `internal/auth/auth.go` (new `ParseAuthorizedKey`),
`internal/tui/commands.go` (new `/addkey`, `/keys`, `keyComment`),
`internal/tui/app.go` (help text).

**Caveat.** This does *not* solve the "I already created a duplicate account
with my second key" recovery case. A returning user is still routed to the
register screen by SSH-key match alone. A proper recovery flow would require
a one-time confirmation token issued from the original session — out of
scope here; documented as a follow-up.

## 7. TouchLastSeen was unwired

**Symptom.** `Store.TouchLastSeen` existed but was never called. The
`users.last_seen_at` column was therefore always NULL.

**Fix.** Called from `internal/ssh/server.go` immediately after a known
fingerprint resolves to a user (i.e. on every successful reconnection of an
existing user). This is best-effort and non-fatal: the call uses
`context.Background()` and ignores errors. We do *not* write `last_seen_at`
on disconnect — the spec keeps `last_seen` privacy-defaulted in v1, so the
column is bookkeeping only and isn't exposed in the UI. If we later choose to
expose it, we should also update on disconnect.

Files: `internal/ssh/server.go`.

---

## Verification

```
go build -buildvcs=false ./...
go test  -buildvcs=false -race ./...
```

All packages build cleanly. The store test suite gained
`TestMentionsOnlyForRoomMembers` and `TestAdminCanDeleteOutsideRoom` covering
fixes #2 and #3 respectively; the existing tests continue to pass after the
`InsertRoomMessage` signature change.

## Items left from the review

These were identified during review but not part of the priority pass:

- Sparse test coverage vs. spec §12 (no `teatest` snapshots, no end-to-end
  SSH client integration test, no load harness).
- `m.messages` / `m.userCache` grow unbounded per session; trim on overflow.
- Deleted `test_sanitize.go` — but no equivalent property test was added for
  `SanitizeUserText`. The targeted `TestSanitizeUserTextStripsTerminalControls`
  in `internal/domain` covers the obvious cases.
- `Limiter.Allow` still does an O(N) sweep on every call; under a connection
  storm this becomes a bottleneck. Move to lazy / periodic eviction.
- `messages_fts MATCH ?` exposes raw FTS5 query syntax to user input. Not a
  security issue (membership is checked separately), but produces confusing
  errors. Consider quoting or pre-escaping.
- The `Hub.send` channel-drop on full buffer is silent; consider tagging the
  session as lossy and forcing a history refresh on next render.
- `RoomMembersForUser(ctx, userID, roomID)` is `(userID, roomID)` while
  `IsMember(ctx, roomID, userID)` is the other way around — minor footgun.
- Admin in-app surface (the reports queue, `/admin` commands) is still
  DB-only; admin CLI exists but TUI doesn't expose it.
