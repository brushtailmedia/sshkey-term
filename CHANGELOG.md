# Changelog

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
