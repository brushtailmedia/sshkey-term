# sshkey-term

Terminal client for [sshkey](https://github.com/brushtailmedia/sshkey) -- a private messaging server over SSH with E2E encryption.

## Features

- End-to-end encrypted rooms and DMs (AES-256-GCM, X25519 key wrapping) 
- SSH key is your identity -- no accounts, no passwords
- Rooms with epoch-based key rotation, DMs with per-message keys
- File sharing, reactions, typing indicators, read receipts, presence
- Inline images via sixel/kitty/iterm2 protocols
- Local encrypted database (SQLCipher) with full-text search
- Multi-server support
- Offline message history (lazy scroll-back)

## Architecture

```
┌──────────────────────────────────────┐
│  sshkey-chat (terminal client)       │
├──────────────────────────────────────┤
│  Bubble Tea         UI chrome        │
│  libghostty (Zig)   terminal render  │
├──────────────────────────────────────┤
│  Go core                             │
│  x/crypto/ssh       SSH connection   │
│  AES-256-GCM        encryption       │
│  X25519 + HKDF      key wrapping     │
│  Ed25519            signatures       │
│  SQLCipher          local DB         │
└──────────────────────────────────────┘
          │
          │ SSH (:2222)
          │
┌──────────────────────────────────────┐
│  sshkey-server (blind relay)         │
│  sees metadata, never content        │
└──────────────────────────────────────┘
```

- **Bubble Tea** -- sidebar, room list, input bar, navigation
- **libghostty** (embedded, via cgo) -- terminal rendering, image protocols, scrollback
- **Go core** -- SSH connection, protocol handling, E2E crypto, local encrypted DB

## Requirements

- Go 1.25 or later
- Zig toolchain (for libghostty compilation)

## Recommended Terminals

Inline image rendering requires a terminal that supports an image protocol. Everything else (text, reactions, TUI layout, navigation) works in any terminal.

| Terminal | Images | Protocol | Platform |
|---|---|---|---|
| **kitty** | ✓ | kitty graphics | Linux, macOS |
| **iTerm2** | ✓ | iTerm2 inline | macOS |
| **WezTerm** | ✓ | sixel, kitty | Linux, macOS, Windows |
| **foot** | ✓ | sixel | Linux (Wayland) |
| **Ghostty** | ✓ | kitty graphics | Linux, macOS |
| **Contour** | ✓ | sixel | Linux, macOS |
| Terminal.app | text only | -- | macOS |
| Windows Terminal | text only | -- | Windows |
| basic xterm | text only | -- | Linux |

The client auto-detects your terminal and uses the best available image protocol. Unsupported terminals fall back to text placeholders (`📎 photo.jpg 230KB`).

Works over SSH -- the image protocol passes through to your local terminal. Use one of the recommended terminals locally for the full experience.

## Quick start

```bash
go build -o sshkey-chat .

./sshkey-chat
```

On first launch, the client prompts to select or generate an Ed25519 SSH key, then connect to a server.

## Configuration

```
~/.sshkey-chat/
��── config.toml              global config, server list, device ID
├── chat.example.com/
│   ├── messages.db          encrypted local DB (all rooms + DMs)
│   └── files/               cached attachments
└── work.company.com/
    ├── messages.db
    └── files/
```

```toml
# ~/.sshkey-chat/config.toml

[device]
id = "dev_V1StGXR8_Z5jdHi6B-myT"

[[servers]]
name = "Personal"
host = "chat.example.com"
port = 2222
key = "~/.ssh/id_ed25519"

[[servers]]
name = "Work"
host = "work.company.com"
port = 2222
key = "~/.ssh/work_key"
```

Each server is independent -- different keys, different rooms, different users. Local DB is per-server.

## Protocol

See [PROTOCOL.md](PROTOCOL.md) for the complete wire format, message types, and crypto specifications. The terminal client implements the full sshkey protocol.

## Related repositories

| Repo | Description |
|---|---|
| [sshkey](https://github.com/brushtailmedia/sshkey) | Server + admin tool (Go) |
| [sshkey-term](https://github.com/brushtailmedia/sshkey-term) | Terminal client (this repo) |
| [sshkey-app](https://github.com/brushtailmedia/sshkey-app) | Desktop + mobile GUI client (Rust + egui) |

## License

MIT
