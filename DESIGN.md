# sshkey-term — Architecture and Implementation Reference

> For contributors to the sshkey-term terminal client. See the server's [PROTOCOL.md](https://github.com/brushtailmedia/sshkey-chat/blob/main/PROTOCOL.md) for the wire protocol and [PROJECT.md](https://github.com/brushtailmedia/sshkey-chat/blob/main/PROJECT.md) for the server architecture.

---

## Local Database

Single SQLCipher-encrypted SQLite database per server, stored at `~/.sshkey-chat/<server-host>/messages.db`. Encryption key derived from the SSH private key via HKDF-SHA256. WAL mode enabled, foreign keys enforced.

The local DB is a cache, not the source of truth. It can be wiped — anything within the server's retention window is re-fetched via `history` requests on next connect.

### Schema

```sql
-- Messages (decrypted locally, one table for all three context types)
CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    sender          TEXT NOT NULL,
    body            TEXT NOT NULL,
    ts              INTEGER NOT NULL,
    room            TEXT NOT NULL DEFAULT '',    -- room nanoid (set for room messages, empty otherwise)
    group_id        TEXT NOT NULL DEFAULT '',    -- group DM nanoid
    dm_id           TEXT NOT NULL DEFAULT '',    -- 1:1 DM nanoid
    epoch           INTEGER NOT NULL DEFAULT 0,  -- room epoch number (0 for DMs)
    reply_to        TEXT NOT NULL DEFAULT '',    -- message ID being replied to
    mentions        TEXT NOT NULL DEFAULT '',    -- JSON array of mentioned user IDs
    has_attachments INTEGER NOT NULL DEFAULT 0,
    raw_payload     TEXT NOT NULL DEFAULT '',    -- full encrypted payload (for signature verification)
    deleted         INTEGER NOT NULL DEFAULT 0,
    deleted_by      TEXT NOT NULL DEFAULT '',
    attachments     TEXT NOT NULL DEFAULT ''     -- JSON attachment metadata
);

-- Exactly one of room/group_id/dm_id is non-empty per row.
-- Partial indexes keep queries fast for each context type:
CREATE INDEX idx_messages_room_ts ON messages(room, ts) WHERE room != '';
CREATE INDEX idx_messages_group_ts ON messages(group_id, ts) WHERE group_id != '';
CREATE INDEX idx_messages_dm_ts ON messages(dm_id, ts) WHERE dm_id != '';

-- Reactions (decrypted emoji stored locally)
CREATE TABLE reactions (
    reaction_id TEXT PRIMARY KEY,
    message_id  TEXT NOT NULL,
    user        TEXT NOT NULL,
    emoji       TEXT NOT NULL,
    ts          INTEGER NOT NULL
);

-- Epoch keys (unwrapped, stored for future decryption of room messages)
CREATE TABLE epoch_keys (
    room  TEXT NOT NULL,    -- room nanoid
    epoch INTEGER NOT NULL,
    key   BLOB NOT NULL,    -- raw decrypted AES-256 key bytes
    PRIMARY KEY (room, epoch)
);

-- Pinned public keys (TOFU key pinning + safety number verification)
CREATE TABLE pinned_keys (
    user        TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,    -- SHA256 hash of public key
    pubkey      TEXT NOT NULL,    -- base64-encoded Ed25519 public key
    verified    INTEGER NOT NULL DEFAULT 0,  -- user explicitly verified via safety number
    first_seen  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Read positions (for unread divider + badge tracking)
CREATE TABLE read_positions (
    target    TEXT PRIMARY KEY,  -- room/group/dm nanoid
    last_read TEXT NOT NULL,     -- message ID
    ts        INTEGER NOT NULL
);

-- Sequence high-water marks (replay detection)
CREATE TABLE seq_marks (
    key TEXT PRIMARY KEY,  -- "room:<id>" or "group:<id>" or "dm:<id>"
    seq INTEGER NOT NULL   -- highest seq seen from this sender+device in this context
);

-- Rooms (cached from server's room_list)
CREATE TABLE rooms (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    topic      TEXT NOT NULL DEFAULT '',
    members    INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    left_at    INTEGER NOT NULL DEFAULT 0,   -- 0 = active; >0 = user has /leave'd
    retired_at INTEGER NOT NULL DEFAULT 0    -- 0 = active; >0 = admin retired the room
);
-- left_at and retired_at are independent flags (Q9 design). A user can be in a
-- retired room (retired_at > 0, left_at = 0) or have left a retired room (both > 0).

-- Group DMs (cached from server's group_list)
CREATE TABLE groups (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL DEFAULT '',
    members  TEXT NOT NULL DEFAULT '',  -- comma-separated user IDs
    left_at  INTEGER NOT NULL DEFAULT 0,
    is_admin INTEGER NOT NULL DEFAULT 0  -- Phase 14: local user is admin of this group
);

-- Group audit events (Phase 14: populated from live group_event broadcasts
-- and sync_batch.Events replay; feeds the /audit overlay)
CREATE TABLE group_events (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id TEXT NOT NULL,
    event    TEXT NOT NULL,     -- "join" | "leave" | "promote" | "demote" | "rename"
    user     TEXT NOT NULL,     -- target of the event
    by       TEXT NOT NULL,     -- acting admin (empty for self-leave/retirement)
    reason   TEXT NOT NULL,     -- "" | "removed" | "retirement" | "retirement_succession"
    name     TEXT NOT NULL,     -- new name for rename events
    quiet    INTEGER NOT NULL,  -- 1 = suppress inline system message
    ts       INTEGER NOT NULL
);

-- 1:1 DMs (cached from server's dm_list)
CREATE TABLE direct_messages (
    id         TEXT PRIMARY KEY,
    user_a     TEXT NOT NULL,
    user_b     TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT 0,
    left_at    INTEGER NOT NULL DEFAULT 0  -- 0 = active; >0 = user has /delete'd
);

-- Client state (key-value store)
CREATE TABLE state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Currently stores: "last_synced" (ISO8601 timestamp for reconnect catchup)

-- Full-text search index (optional — requires FTS5-enabled SQLite build)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    body, sender, id UNINDEXED,
    content='messages', content_rowid='rowid'
);
-- Auto-populated by triggers on INSERT/DELETE. Falls back to LIKE queries if unavailable.
```

### Design points

- **One table for all messages.** Room, group, and DM messages share the `messages` table, distinguished by which context column is non-empty. Partial indexes keep each query path efficient.
- **Decrypted at rest.** Message bodies, emoji, and epoch keys are stored in decrypted form. The SQLCipher encryption protects the whole DB file. This allows local search (FTS5) and offline browsing without re-decrypting.
- **`left_at` and `retired_at` are independent.** A room can be left, retired, both, or neither. The TUI renders them differently: left rooms show "(left)", retired rooms show "(retired)", both show "(retired)" since retirement is the underlying cause.

---

## In-Memory Caching

The `Client` struct (`internal/client/client.go`) maintains several in-memory caches that survive for the duration of the connection. All are rebuilt from the server's handshake messages on each connect/reconnect.

| Cache | Type | Populated by | Invalidated by |
|---|---|---|---|
| `profiles` | `map[string]*protocol.Profile` | `profile` messages during handshake + real-time broadcasts | Overwritten per-user on new `profile` arrival |
| `groupMembers` | `map[string][]string` | `group_list` + `group_created` | Rebuilt on reconnect from `group_list` |
| `groupAdmins` (Phase 14) | `map[string]map[string]bool` | `group_list.Admins` + live `group_event{promote,demote}` + `sync_batch.Events` replay | Rebuilt on reconnect from `group_list`, updated live on broadcasts |
| `dms` | `map[string][2]string` | `dm_list` + `dm_created` | Rebuilt on reconnect from `dm_list` |
| `retired` | `map[string]string` | `retired_users` catchup + `user_retired` real-time + `profile` with `retired: true` | Append-only (retirement is monotonic) |
| `epochKeys` | `map[string]map[int64][]byte` | `epoch_key` messages + `sync_batch` epoch keys | Append-only (new epochs added, old ones never removed) |
| `currentEpoch` | `map[string]int64` | `epoch_key` messages | Overwritten when a newer epoch arrives for the same room |
| `seqCounters` | `map[string]int64` | Loaded from `seq_marks` table on startup | Updated on every received message (high-water mark) |

**Phase 14 admin accessors:** `Client.GroupAdmins(groupID) []string` returns the sorted admin list for a group; `Client.IsGroupAdmin(groupID, userID) bool` is the point query used by the TUI pre-check before sending admin verbs. `Client.FindUserByName(name) string` resolves a display name to a user ID — used by `/add`, `/kick`, and friends to map `@alice` arguments to `usr_abc123`.

**Phase 18 topic accessor:** `Client.DisplayRoomTopic(roomID) string` returns the topic for a room nanoid ID, reading from the local `rooms` table (populated on every `room_list` refresh). Parallel to `Client.DisplayRoomName`. Returns empty string when no topic is set or when the room isn't cached yet — the render layer uses `if topic != ""` to omit the topic line cleanly. Used by both the messages pane two-line header and the info panel `Topic:` line. Live topic updates after the initial `room_list` are deferred to a future phase (CLI audit + `room_updated` broadcast); today's resolver reads whatever the most recent `room_list` persisted, which is "current topic as of last reconnect".

**Startup sequence:** On connect, the client receives the handshake messages in order (see PROTOCOL.md handshake section). Each handler populates its cache AND persists to the local DB where applicable. On reconnect, the same sequence runs — server sends only what changed since `last_synced`.

**Room display names** are resolved via `DisplayRoomName(roomID)` which reads from the `rooms` table. The TUI's `resolveRoomName` callback wraps this for render-time resolution. No separate in-memory room name cache — the DB is fast enough via SQLCipher's page cache.

---

## Slash Commands

All slash commands are parsed in `internal/tui/input.go` `handleCommand()`. They fall into three routing categories:

### Direct sends (no confirmation dialog)

| Command | Context | Action |
|---|---|---|
| `/typing` | Room, group, DM | Sends typing indicator to server |
| `/rename <name>` | Group only (admin) | Pre-checks local `is_admin`; sends `rename_group` if admin, otherwise shows a friendly status bar rejection |

### App-level dialogs (routed via `SlashCommandMsg`)

| Command | Context | Action |
|---|---|---|
| `/leave` | Room, group | Opens confirmation dialog, sends `leave_room` or `leave_group` on confirm. Groups: if the local user is the sole admin and the group has other members, the last-admin promote picker opens instead. |
| `/leave` | DM | Rejected — status bar shows "/leave is not available for 1:1 DMs — use /delete" |
| `/delete` | Room, group, DM | Opens context-aware confirmation dialog (active vs retired wording for rooms), sends appropriate delete verb on confirm. Groups: same last-admin picker interception as `/leave`. |
| `/verify <user>` | Any | Opens safety number verification overlay |
| `/unverify <user>` | Any | Clears verification status, status bar confirmation |
| `/search` | Any | Opens search overlay |
| `/settings` | Any | Opens settings overlay |
| `/help` | Any | Opens help screen (context-aware: admin command block shown only in groups where the user is an admin) |
| `/pending` | Any (admin) | Opens pending key requests panel |
| `/mykey` | Any | Shows current user's public key + fingerprint |
| `/upload <path>` | Room, group, DM | Initiates file upload flow |

### Group admin verbs (Phase 14) — dialog-mediated

| Command | Context | Action |
|---|---|---|
| `/add @user` | Group (admin) | Opens `AddConfirmModel`; on confirm sends `add_to_group` |
| `/kick @user` | Group (admin) | Opens `KickConfirmModel` with current member count; on confirm sends `remove_from_group` and records the kick for `/undo` |
| `/promote @user` | Group (admin) | Opens `PromoteConfirmModel`; on confirm sends `promote_group_admin` |
| `/demote @user` | Group (admin) | Opens `DemoteConfirmModel` with pre-demote admin count; on confirm sends `demote_group_admin` |
| `/transfer @user` | Group (admin) | Opens `TransferConfirmModel`; on confirm sends `promote_group_admin` then `leave_group` atomically. If target is already an admin, flow collapses to just leaving. |

All five pre-check the local `is_admin` flag before opening the dialog. Non-admin attempts produce an inline status-bar rejection before hitting the wire. The actual membership/admin check is enforced server-side on each verb — the client pre-check is UX polish, not security.

### Group status commands (Phase 14) — local-only overlays

| Command | Context | Action |
|---|---|---|
| `/members` | Group | Read-only overlay listing members with ★ admin markers |
| `/admins` | Group | Read-only overlay pre-filtered to admins only |
| `/role @user` | Group | Status bar: "Bob is an admin" / "Bob is a regular member" |
| `/whoami` | Group | Status bar: "You are an admin of Project Alpha" / "You are a regular member" |
| `/groupinfo` | Group | Opens the info panel (Ctrl+I equivalent) |
| `/audit [N]` | Group | Read-only overlay, reads recent rows from local `group_events` table |
| `/undo` | Group (admin) | Reverts the local user's most recent kick within 30 seconds by sending `add_to_group` for the kicked target |

### Topic command (Phase 18) — local-only, rooms only

| Command | Context | Action |
|---|---|---|
| `/topic` | Room | Status bar: "#general — General chat, please be nice" or "#general has no topic set". In group/DM contexts, rejects with "/topic is only available in rooms". Pure local read via `Client.DisplayRoomTopic`; no server interaction. Changing a topic (`/topic <new text>`) is deferred to the Admin CLI audit phase. |

### Message editing (Phase 15) — input-layer key bindings

The edit shortcut lives on the input layer rather than the slash command table because it's invoked by a key press (`Up`) on an empty input, not by typing a command. Three app-layer hooks wire it together:

| Hook | Action |
|---|---|
| `Up` on empty `FocusInput` (and context is not archived / DM-retired) | `tryEnterEditMode` scans backwards through the in-memory message list for the user's most recent non-deleted message; if found, `InputModel.EnterEditMode(msgID, body)` populates the buffer and flips `editMode = true` |
| `Enter` while `InputModel.IsEditing()` | `dispatchEdit` routes to `Client.EditRoomMessage` / `EditGroupMessage` / `EditDMMessage` based on active context; buffer clears, edit mode exits |
| `Esc` while `InputModel.IsEditing()` | Clear buffer, exit edit mode, no dispatch |
| Any context switch (sidebar nav, quick switch, search jump) | `applyRoomTopic` also calls `ExitEditMode` + `ClearInput` so a half-finished edit never dispatches to the wrong conversation |

**Preserve-and-replace pattern.** The three `Client.EditXMessage` methods all fetch the original decrypted message from the local store and copy `ReplyTo`, `Attachments`, `Previews` verbatim into the new payload before re-encrypting. `Mentions` are re-extracted from the new body (the new body is the authoritative source for highlight rendering + notification targeting). The server never sees these payload-internal fields — preservation is 100% client-side.

**Key preservation for attachments.** Rooms: `FileEpoch` stays pinned to the original upload epoch so the file is still decryptable even when the edit targets the current epoch in the grace window. Groups/DMs: `FileKey` (K_file) is independent of K_msg and is copied verbatim, so a fresh K_msg on edit doesn't invalidate existing attachment decryption.

**Error handling.** `edit_window_expired` and `edit_not_most_recent` errors from the server surface as status bar messages and cleanly exit edit mode; the input buffer is cleared so the user can retype. Byte-identical `unknown_room` / `unknown_group` / `unknown_dm` responses appear as the generic "not a member" error — the server never leaks "that message doesn't belong to you" or "that message is deleted" to a probing client.

### Local toggle (no server interaction)

| Command | Context | Action |
|---|---|---|
| `/mute` | Room, group | Toggles local mute state (stored in app config, not DB) |

### Wait-for-echo pattern

All commands that mutate server state follow the same pattern: send the request, do NOT touch local state, wait for the server's echo message (`room_left`, `group_left`, `dm_left`, `room_deleted`, `group_deleted`, `add_group_result`, `remove_group_result`, `promote_admin_result`, `demote_admin_result`), THEN update local state. This ensures multi-device consistency — if the server rejects the request, no local state was corrupted.

---

## Focus Model

The TUI has four focus states defined in `internal/tui/app.go`:

```
FocusInput (0) → FocusSidebar (1) → FocusMessages (2) → FocusMembers (3) → FocusInput
```

**Tab** cycles through them. `FocusMembers` is skipped if the member panel is not visible (Ctrl+M toggles it). **Esc** always returns to `FocusInput` and dismisses any open modal.

Each focus state determines which key bindings are active:
- `FocusInput` — text entry, slash command parsing, @mention tab completion
- `FocusSidebar` — arrow keys navigate rooms/groups/DMs, Enter selects
- `FocusMessages` — arrow keys scroll, r/e/p/d/c/g/t for message actions (reply, emoji, pin, delete, copy, go-to-parent, thread)
- `FocusMembers` — arrow keys navigate members, Enter opens DM or member menu

**Modal override:** When any overlay is open (help, search, settings, info panel, verification, emoji picker, quick switch, confirmation dialogs, etc.), all key presses are intercepted by the overlay. The underlying focus state is preserved and restored when the overlay closes.

**Archived context block:** When the current context is archived (`IsLeft() || IsRoomRetired()`), normal text input is blocked. Only slash commands (starting with `/`) pass through, and only `/delete` is useful. The input gate checks both flags in `app.go`'s Update handler.

---

## Sync-on-Reconnect

### Connection lifecycle

1. **Initial connect:** `ConnectWithReconnect()` dials SSH, opens 3 channels, runs the handshake
2. **Disconnect detected:** reader goroutine hits EOF/error, signals the reconnect loop
3. **Reconnect loop:** exponential backoff (1s → 2s → 4s → ... → 60s cap, unlimited attempts)
4. **Re-handshake:** client sends `client_hello` with `LastSyncedAt` — the server only sends messages newer than this timestamp
5. **Catchup:** server sends `deleted_rooms` → `retired_rooms` → `room_list` → `deleted_groups` → `group_list` → `dm_list` → `profile`s → `retired_users` → `epoch_key`s → `sync_batch`es → `sync_complete`
6. **Resume:** real-time message push resumes after `sync_complete`

### Catchup ordering

The server sends catchup/deletion lists BEFORE active-membership lists. This ordering is critical: a room the user `/delete`d on their phone must be purged from the laptop's local state BEFORE `room_list` populates the sidebar, otherwise the deleted room would briefly appear.

### Multi-device /leave reconciliation

When a user `/leave`s a room on one device, the other device learns about it indirectly: the server's `room_list` on reconnect omits the room. The client's `room_list` handler walks its locally-active rooms and marks any missing from the server's response as `left_at = now`. This is a known architectural quirk — it works but is fragile. Phase 20 plans to replace it with server-authoritative `left_rooms` / `left_groups` sidecars.

### `last_synced` state

Stored in the `state` table as key `"last_synced"` with an ISO8601 value. Updated on every `sync_complete`. Loaded on startup to populate the `client_hello` `LastSyncedAt` field.

---

## Safety Numbers and Key Pinning

The client uses Trust-On-First-Use (TOFU) for peer identity. Each user's Ed25519 public key is pinned in the `pinned_keys` table on first encounter (from `profile` messages).

**Key change detection:** On every `profile` message, the client compares the arriving fingerprint against the pinned value. Mismatch triggers a hard warning dialog (`KeyWarningModel`) — "This user's key has changed. This could mean their key was compromised."

**Safety numbers:** `SHA256(sort(alice_pubkey_bytes, bob_pubkey_bytes))` truncated to 24 digits, displayed as six groups of four. Users compare via `/verify <user>` overlay.

**Verified flag:** Users can mark a peer as "verified" after comparing safety numbers in person. The `verified` column in `pinned_keys` tracks this. Verified peers show a ✓ badge in the sidebar and info panel.

---

## File Upload

Three entry points:
1. `/upload <path>` — slash command with explicit path
2. Drag-and-drop (if terminal supports it)
3. Paste from clipboard (if terminal supports it)

The flow: client calls `upload_start` on Channel 1 with a BLAKE2b-256 content hash, waits for `upload_ready`, writes encrypted bytes to Channel 3 as a length-prefixed binary frame, waits for `upload_complete` with the server-assigned `file_id`, then references the `file_id` in the message envelope.

**Room files** are encrypted with the current epoch key. **DM/group files** are encrypted with a fresh per-file key (`K_file`) stored in the `file_key` field inside the encrypted message payload.

---

## First-Run Wizard

9-step onboarding flow on first launch:
1. Welcome screen
2. SSH key selection (list existing Ed25519 keys) or generation
3. Passphrase setup (optional, recommended)
4. Key backup acknowledgement (mandatory — "I understand there is no recovery")
5. Display name entry
6. Public key display (for sharing with admin)
7. Server host + port entry
8. Connection test
9. Ready screen

The wizard enforces the backup acknowledgement before allowing connection. This is the last chance to understand the "no recovery" model — once the key is lost, the account is gone.

---

## Search

Local full-text search using SQLite FTS5 when available, with LIKE fallback.

- **FTS5 path:** `SELECT ... FROM messages_fts WHERE messages_fts MATCH ?` — fast, ranked by relevance
- **LIKE fallback:** `SELECT ... FROM messages WHERE body LIKE ?` — slower, no ranking
- **Scope:** current context only (the room/group/DM the user is viewing when they open search)
- **UI:** search results appear in a scrollable overlay, Enter on a result jumps to that message in the stream

The TUI shows a "search may be slow (FTS5 not available)" indicator when using the LIKE fallback.

---

## Connection State and Offline Mode

- **Online:** normal operation, all features available
- **Reconnecting:** status bar shows "Reconnecting (attempt N, next retry in Xs)". Input disabled. Local history fully browsable (read-only).
- **Offline:** SSH keepalive (30s interval, 3 missed = disconnect) detects dead connections faster than TCP timeout. Exponential backoff reconnect (1s → 60s cap).

All local data (messages, reactions, rooms, search) is accessible offline. The only things that require a connection are sending messages, uploading files, and receiving real-time updates.

---

## Mute Controls

Per-room and per-group mute state, stored in the app config file (not the DB, not the server). Toggled via `/mute` or the info panel's `m` key.

When muted:
- Desktop notifications suppressed for that context
- Bell sound suppressed
- Unread badge still updates (the user can see they have unreread messages without being notified)
- No server interaction — mute is purely local

---

## Desktop Notifications

OS-level notifications via `notify-send` (Linux), `osascript` (macOS), or terminal bell fallback.

**Notification rules:**
- Only for messages in non-active contexts (the context the user is NOT currently viewing)
- Suppressed for muted contexts
- Suppressed for the user's own messages (multi-device echo)
- Content: sender display name + message body preview (truncated)

---

## Error Feedback

Errors surface in the status bar at the bottom of the TUI. Types:
- **Server errors:** displayed verbatim from the server's `error.message` field (rate limits, policy denials, auth failures)
- **Client errors:** connection failures, file upload errors, encryption failures
- **Persistent until next action:** errors don't auto-dismiss on a timer; they clear when the user types or navigates. This ensures the user sees the error even if they were looking away.

---

## Soft Thresholds

- **50-member group warning:** when creating a group DM with 50+ total members, the status bar shows "Large group (N members) — consider using a room for better performance." The group is still created — the warning is advisory. The server hard-caps at 150 members.

---

## Non-Obvious Architecture Points

Three behaviors that are counterintuitive enough to document explicitly. Each is a deliberate design choice, not a bug.

### 1:1 DM `/delete` has no dedicated server handler

There is no `delete_dm` protocol verb. The flow is:

1. Client sends `leave_dm`
2. Server sets a per-user `left_at` cutoff on the DM row
3. Server echoes `dm_left` to the caller's sessions
4. Client purges all local messages for that DM on receipt of the echo
5. Server-side cleanup (delete row, unlink `dm-<id>.db`) runs automatically inside `handleLeaveDM` when BOTH users' cutoffs are set

The design preserves the silent-leave model: a probing client cannot distinguish "the other party deleted the DM" from "the other party is just quiet." Anyone looking for `handleDeleteDM` in the server code will not find it — `leave_dm` is the only DM-exit verb.

### `SetDMLeftAt` is a one-way ratchet

The store function refuses to overwrite a non-zero `left_at` value — a second `SetDMLeftAt` call on an already-left DM is a silent no-op. This prevents a re-leave from re-revealing pre-delete history.

Scenario: User A deletes the DM at T=100 (cutoff = 100). B sends a message at T=200 which reappears in A's sidebar as a fresh conversation. A deletes again. Without the ratchet, the second delete would advance the cutoff to T=200, which would move past messages B sent between T=100 and T=200 that A hadn't seen yet. The ratchet freezes the cutoff at its earliest value.

### Multi-device `/leave` catchup uses client-side reconciliation

When a user `/leave`s a room on one device, other devices learn about it indirectly: the server's `room_list` on reconnect omits the room. The client's `room_list` handler walks its locally-active rooms and marks any missing from the server's response as `left_at = now`.

This is a known architectural quirk — it works but is fragile because "not in the server list" can mean five different things (self-leave, self-delete, admin-kicked, retirement-removal, group-deleted-by-last-member) and the client cannot distinguish them. Phase 20 plans to replace this with server-authoritative `left_rooms` / `left_groups` sidecars.

### `storeGroupMessage` defense in depth

When a group message arrives but `DecryptGroupMessage` returns an error (no wrapped key for the local user, or key material missing), `storeGroupMessage` drops the row entirely rather than calling `InsertMessage` with an empty body. This mirrors `storeReaction`'s existing "can't decrypt — don't persist garbage" pattern and catches the edge case where a server regression sends a pre-join group message: the wrapped-key slot doesn't exist for the new member, decrypt fails, row drops.

The primary defence against pre-join message leakage is the server-side `joined_at` gate in `syncGroup` and `handleHistory`; this client-side drop is the second layer. See the server's `groups_admin.md` "Pre-join history gate" section for the full write-up.
