# Changelog

## Unreleased

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
