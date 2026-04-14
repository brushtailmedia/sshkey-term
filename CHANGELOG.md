# Changelog

## [Unreleased]

### Changed
- **Group DMs gained an in-group admin model (Phase 14)** — matches the server-side reversal of the "immutable peer DMs" decision. Group creators become the first admin; any admin can add/remove/promote/demote members. New `/add`, `/kick`, `/promote`, `/demote`, `/transfer` slash commands with confirmation dialogs. See `groups_admin.md` in the server repo for the full design.
- **Info panel per-member admin flag** now reads from the in-memory group admin set (populated by `group_list` catchup + live `group_event{promote,demote}` broadcasts) instead of the global `profile.Admin` flag (which tracks server-wide admin status, unrelated to per-group governance).
- **`/rename` now admin-gated client-side** — non-admin attempts surface a friendly "you are not an admin" message without hitting the wire. Matches the server-side admin gate landed in Phase 14.
- **Group `group_renamed` + `group_event{rename}` dual broadcast** — the client now handles both shapes during the single-repo upgrade window. Sync replay uses `group_event{rename}` via the new `SyncBatch.Events` field.
- **System message rendering for group events** — all five event types (`join`, `leave`, `promote`, `demote`, `rename`) render as system messages in the message view with specific wording ("alice added bob to the group", "alice removed bob", etc.). Honors the Phase 14 `Quiet` flag.
- Room identity switched to nanoid IDs (`room_` prefix) — display names resolved at TUI layer
- All protocol `Room` fields now carry nanoid IDs instead of display names
- `room_list` handled at client layer (persists room metadata to local DB)
- Info panel hints: active rooms and groups show both `/leave` and `/delete`; left/retired rooms show `/delete` only; obsolete "(coming in a later phase)" placeholder removed
- Read-only banner wording distinguishes self-leave ("you left this room") from admin retirement ("this room was archived by an admin")

### Added
- **Phase 14 group admin slash commands**:
  - Admin verbs: `/add @user`, `/kick @user`, `/promote @user`, `/demote @user`, `/transfer @user` (atomic promote-then-leave handoff). Each with confirmation dialog. All admin verbs pre-check the local `is_admin` flag and surface a friendly rejection before hitting the wire.
  - Status commands: `/members`, `/admins`, `/role @user`, `/whoami`, `/groupinfo`, `/audit [N]` (recent admin actions, default 10), `/undo` (revert last kick within 30 seconds).
  - Creation commands: `/groupcreate ["name"] @a @b @c` (inline group DM creation, bypasses the wizard), `/dmcreate @user` (inline 1:1 DM creation).
- **`/audit` overlay** — one-shot read-only panel showing recent admin actions for the current group, read from the local `group_events` table. Populated from both live broadcasts and offline sync replay.
- **`/members` and `/admins` overlays** — one-shot read-only panels listing group members with ★ admin markers. `/admins` pre-filters to just admins.
- **Sidebar ★ admin indicator** — groups where the local user is an admin show a muted ★ glyph before the group name. Updates live on `group_event{promote,demote}` via the `resolveIsLocalAdmin` callback.
- **Info panel admin keyboard shortcuts** — A/K/P/X on a focused member row route to the admin verb dialogs (Add / Kick / Promote / demoteX). X is used for demote because D means delete elsewhere in the app. Active only in group contexts.
- **Event coalescing** — consecutive same-admin same-verb events within 10 seconds collapse into one system message ("alice added bob, carol, and dave"). Applies to join/promote/demote/removed; never coalesces self-leave, retirement, or rename. Individual events are still persisted un-coalesced to the local `group_events` table (visible in `/audit`).
- **Client `group_events` table** — single table with `group_id` column (client is single-DB-per-server). Populated from both live `group_event` broadcasts and the new `SyncBatch.Events` replay. Feeds the `/audit` overlay.
- **Client `groups.is_admin` column** — the local user's admin flag per group, persisted so the TUI pre-check survives restart. Not folded into the `StoreGroup` upsert so promote/demote events can't clobber the members list.
- **In-memory `groupAdmins` map on `Client`** — other members' admin state, sourced from `group_list` + live `group_event{promote,demote}` + `sync_batch.Events` replay. Accessed via `GroupAdmins(groupID)` and `IsGroupAdmin(groupID, userID)`.
- **New client store helpers**: `IsLocalUserGroupAdmin`, `SetLocalUserGroupAdmin`, `RecordGroupEvent`, `GetGroupEvents`, `GetRecentGroupEvents`, plus a `FindUserByName` accessor on `Client` for resolving `@user` arguments to user IDs.
- **Confirmation dialogs** — five new dialog models (`AddConfirmModel`, `KickConfirmModel`, `PromoteConfirmModel`, `DemoteConfirmModel`, `TransferConfirmModel`) following the existing `LeaveConfirmModel` shape. Transfer carries a `TargetAlreadyAdmin` flag so the text flips to "already admin, just leave?" when promote would be a no-op.
- **`group_added_to` handler** — when an admin adds the local user to an existing group, the client inserts the group into local state immediately and surfaces a toast-style status bar notification ("alice added you to 'Project X'").
- **`/undo` 30-second kick revert** — tracks the last kick the local user performed; `/undo` within the window re-adds via `add_to_group`. Exactly one kick tracked, no stack.
- **Protocol type mirrors** — nine new message types in `sshkey-term/internal/protocol/messages.go` (`AddToGroup`, `RemoveFromGroup`, `PromoteGroupAdmin`, `DemoteGroupAdmin`, four result echoes, and `GroupAddedTo`), plus extensions to `GroupEvent` (`By`/`Name`/`Quiet`), `GroupLeft` (`By`), `GroupCreated` (`Admins`), `GroupInfo` (`Admins`), `RenameGroup` (`Quiet`), `SyncBatch` (`Events`).
- `rooms` table in client DB for room metadata persistence (id, name, topic, members)
- `DisplayRoomName()` resolver — reads from local DB, falls back to raw ID
- `resolveRoomName` callbacks in sidebar, messages header, quickswitch, infopanel, notifications
- **Room retirement + `/delete` for rooms (Phase 12)** — clients receive `room_retired` / `retired_rooms` and `room_deleted` / `deleted_rooms` broadcasts and catchup lists; UI flips affected rooms to read-only or removes them entirely
- `DeleteRoomConfirmModel` — confirmation dialog with distinct wording for active vs retired rooms
- Sidebar: retired rooms render with `(retired)` marker (takes priority over `(left)`); unread counts suppressed; `RemoveRoom` helper parallel to `RemoveGroup`
- Messages view: `SetRoomRetired` state + banner for the read-only admin-archived case
- `rooms.retired_at` column (no migration — empty client DBs); `MarkRoomRetired`, `IsRoomRetired`, `PurgeRoomMessages` store helpers
- `DeleteRoom` client method; `case "room_retired" / "retired_rooms" / "room_deleted" / "deleted_rooms"` in client dispatch loop

## v0.1.1 — 2026-04-07

- **Soft-delete messages** — deleted messages show as tombstones in the conversation stream instead of disappearing. Self-deletes show "message deleted"; admin deletes show "message removed by [name]". Preserves conversation flow. Replies to deleted messages show "Deleted message" as the parent preview. Thread view handles deleted roots.
- **Persistent status bar errors** — server errors (rate limits, conflicts, etc.) persist until the user's next action instead of vanishing after 5 seconds. User-friendly messages ("Slow down — too many messages" instead of "rate_limited").
- **Rate limits** — deletes (10/min user, 50/min admin), reactions (30/min), DM creation (5/min), profile changes (5/min), pin/unpin (10/min)
- **Attachment persistence** — attachment metadata (file ID, name, size, mime, decrypt key) persisted in local DB. Attachments survive restarts and room switches. Previously lost on DB reload.
- **File cleanup on delete** — cached files deleted when messages are deleted. Server cleans up file blobs, hashes, and pins on message delete and purge.
- **Upload epoch race fix** — `UploadFile` returns the epoch used for encryption, preventing a race where epoch rotation between upload and send could make files undecryptable.
- **Reply preview** — replies show parent message snippet instead of raw ID
- **Jump-to-parent** — press `g` on a reply to jump to the parent message
- **Thread view** — press `t` to see a message and all its replies
- **Quick switch** — `Ctrl+K` fuzzy search across rooms and conversations
- **Alt+Up/Down** — fast room navigation from any panel
- **SSH keepalive** — 30s interval, auto-reconnect after 3 failures
- **Exponential backoff** — reconnect delays: 1s, 2s, 4s, 8s, 16s, 30s cap
- **FTS5 indicator** — search UI shows warning when full-text search is unavailable
- **Typing indicator** — compact "3 people are typing..." for 3+ users
- **Sidebar unread badges** — update in real-time for non-active rooms
- **Viewport auto-scroll** — message list follows cursor on keyboard navigation
- **Scroll-to-message** — search results and pinned message clicks jump to the message
- **Overlay focus** — all overlays restore focus to input on close
- **Mention word boundaries** — `@alice` no longer matches mid-word
- **Wizard navigation** — `Esc`=back, `q`=quit, mouse support on all steps
- **Room membership** — `room_members` protocol for accurate member lists in info/member panels

## v0.1.0 — 2026-04-07

Initial release.

### Features

- E2E encrypted rooms (epoch keys) and DMs (per-message keys)
- SSH key is your permanent identity — no accounts, no passwords
- Encrypted local database (SQLCipher, HKDF-derived key from SSH private key)
- Full-text search (FTS5 when available, LIKE fallback with user-visible indicator)
- File sharing with BLAKE2b-256 content hash verification
- Inline image rendering (kitty, iTerm2, sixel protocols)
- Reactions, typing indicators, read receipts, presence
- Pinned messages with clickable pin bar
- @mention completion with word-boundary detection
- Multi-server support (Ctrl+1-9 to switch)
- Offline message history with lazy scroll-back (local-first, server fallback)
- Thread view (press t on any message to see root + all replies)
- Reply preview (shows parent message snippet instead of raw ID)
- Jump-to-parent (press g on a reply)
- Quick switch (Ctrl+K fuzzy search across rooms and conversations)
- Alt+Up/Down for fast room navigation
- Search with jump-to-message
- Scroll-to-message on search result and pinned message click

### First-Run Wizard

- 9-step setup: name, key select/generate/import, passphrase, backup, share, server
- Key generation with optional passphrase (Ed25519)
- Display name embedded in public key comment for admin
- Mandatory backup acknowledgement before connecting
- Full keyboard + mouse support on all wizard steps
- Esc=back, q=quit navigation throughout

### Account Management

- Self-service account retirement (Settings, typed confirmation)
- Self-service device management (list + revoke own devices)
- Key verification with safety numbers
- Connection failure overlay with public key copy for admin sharing

### Connection

- SSH keepalive (30s interval, 3 failures = reconnect)
- Exponential backoff reconnect (1s, 2s, 4s, 8s, 16s, 30s cap)
- 3-channel SSH (protocol, downloads, uploads)

### TUI

- Sidebar, messages, input, member panel, status bar
- 20+ overlays (help, search, settings, quick switch, thread view, new conversation, info panel, pending keys, emoji picker, verify, device manager, retire confirm, etc.)
- Focus restoration on overlay close
- Mouse support: sidebar, messages, pinned bar, settings, wizard, connect-failed
- Typing indicator: compact "3 people are typing..." for 3+ users
- Unread badges update in real-time for non-active rooms
- Message viewport follows cursor on keyboard navigation
