# Changelog

## [Unreleased]

### Changed
- Room identity switched to nanoid IDs (`room_` prefix) — display names resolved at TUI layer
- All protocol `Room` fields now carry nanoid IDs instead of display names
- `room_list` handled at client layer (persists room metadata to local DB)
- Info panel hints: active rooms and groups show both `/leave` and `/delete`; left/retired rooms show `/delete` only; obsolete "(coming in a later phase)" placeholder removed
- Read-only banner wording distinguishes self-leave ("you left this room") from admin retirement ("this room was archived by an admin")

### Added
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
