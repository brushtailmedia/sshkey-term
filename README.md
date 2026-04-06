# sshkey-term

v0.1.0 — Expect breaking changes until v1.0.

Terminal client for [sshkey-chat](https://github.com/brushtailmedia/sshkey-chat) -- a private messaging server over SSH with E2E encryption.

## Features

- End-to-end encrypted rooms and DMs (AES-256-GCM, X25519 key wrapping)
- SSH key is your permanent identity -- no accounts, no passwords, no key rotation
- Rooms with epoch-based key rotation, DMs with per-message keys
- File sharing, reactions, typing indicators, read receipts, presence
- Inline images via sixel/kitty/iterm2 protocols
- Local encrypted database (SQLCipher) with full-text search
- Multi-server support
- Offline message history (lazy scroll-back)
- Self-service account retirement (settings → Retire account) with typed confirmation
- Self-service device management (settings → Manage devices) — list and revoke your own devices
- First-run wizard with key generation + passphrase + mandatory backup acknowledgement

## Architecture

```
┌──────────────────────────────────────┐
│  sshkey-chat (terminal client)       │
├──────────────────────────────────────┤
│  Bubble Tea         UI chrome        │
│  rasterm            inline images    │
├──────────────────────────────────────┤
│  Go core                             │
│  x/crypto/ssh       SSH connection   │
│  AES-256-GCM        encryption       │
│  X25519 + HKDF      key wrapping     │
│  Ed25519            signatures       │
│  go-sqlcipher       encrypted DB     │
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
- **rasterm** -- inline image rendering (kitty, iTerm2, sixel protocols)
- **Go core** -- SSH connection, protocol handling, E2E crypto, local encrypted DB (go-sqlcipher, requires cgo)

## Requirements

- Go 1.25 or later
- C compiler (for go-sqlcipher / CGO -- gcc, clang, or Xcode command line tools)

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
# Build with FTS5 full-text search support (recommended)
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" go build -o sshkey-chat .

# Or build without FTS5 (search falls back to LIKE queries)
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

## Security model

**Your Ed25519 key is your permanent identity.** 

The server never sees your private key or passphrase, only the public key. The client handles all encryption, decryption, signing, and verification locally. The server is a blind relay that routes messages and enforces access control based on public keys.

Three layers of protection, used in combination:

| Layer | Protects against | How to use |
|---|---|---|
| **Passphrase** | Stolen device — key at rest | Set a passphrase when generating your key (wizard prompts by default) |
| **Device revocation** | Stolen device where you're confident the key/passphrase held | **Settings → Manage devices on this server** (self-service) or ask your admin to `sshkey-ctl revoke-device --user you --device dev_...` |
| **Account retirement** | Key compromise (copied, leaked, passphrase cracked) | **Settings → Retire account** (requires typing `RETIRE MY ACCOUNT` to confirm) |

Device revocation is operational cleanup — it doesn't stop an attacker who has your private key and knows your key passphrase, this is why it is important to protect your key with a passphrase. If you suspect the key itself is compromised, retire the account.

**Retirement is monotonic and irreversible.** A retired account cannot be reactivated. To use the server again, the admin adds you as a new account (same or different username) with your new key. You lose access to your previous chat history, any existing DMs with you become read-only for the other party.

**Back up your key.** If you lose both the key and your passphrase with no backup, your account ends — the server cannot help you recover it. The first-run wizard enforces an explicit acknowledgement of this before letting you connect.

See the server's [PROTOCOL.md](https://github.com/brushtailmedia/sshkey-chat/blob/main/PROTOCOL.md) section "Account Retirement" for the wire protocol and [PROJECT.md "Account Lifecycle"](https://github.com/brushtailmedia/sshkey-chat/blob/main/PROJECT.md) for the full design rationale.

## Protocol

See the server's [PROTOCOL.md](https://github.com/brushtailmedia/sshkey-chat/blob/main/PROTOCOL.md) for the complete wire format, message types, and crypto specifications. The terminal client implements the full sshkey protocol.

## Related repositories

| Repo | Description |
|---|---|
| [sshkey-chat](https://github.com/brushtailmedia/sshkey-chat) | Server + admin tool (Go) |
| [sshkey-term](https://github.com/brushtailmedia/sshkey-term) | Terminal client (this repo) |
| [sshkey-app](https://github.com/brushtailmedia/sshkey-app) | Desktop + mobile GUI client (Rust + egui) |

## License

MIT
