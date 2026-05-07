# sshkey-term

Expect breaking changes until v1.0.

Terminal client for [sshkey-chat](https://github.com/brushtailmedia/sshkey-chat) -- a private messaging server over SSH with E2E encryption.

## Features

- End-to-end encrypted rooms, 1:1 DMs, and group DMs (AES-256-GCM, X25519 key wrapping)
- SSH key is your permanent identity -- no accounts, no passwords, no key rotation
- Rooms with epoch-based key rotation, DMs with per-message keys (Signal-level forward secrecy)
- **In-group admin model for group DMs** (Phase 14) — creators become the first admin; admins can add, remove, promote, demote, and rename via `/add`, `/kick`, `/promote`, `/demote`, `/transfer` with confirmation dialogs, `/audit` for recent admin actions, and `/undo` for 30-second kick revert
- `/leave` and `/delete` for rooms, 1:1 DMs, and group DMs with multi-device sync
- Retired-room read-only state (admin-archived rooms show a distinct banner)
- File sharing, reactions, typing indicators, read receipts, presence, pinned messages
- Soft-delete: deleted messages show as tombstones in the stream, not disappearances
- Inline image previews: native-resolution via rasterm (kitty / iTerm2 / WezTerm / Ghostty), with a universal block-cell fallback on every other terminal — never a "your terminal isn't supported" cliff
- Local encrypted database (SQLCipher) with full-text search (FTS5)
- Multi-server support (Ctrl+1-9 to switch)
- Offline message history with lazy scroll-back (local-first, server fallback)
- Quick switch (Ctrl+K fuzzy search across rooms and conversations)
- Thread view, reply preview, jump-to-parent
- Alt+Up/Down fast room navigation
- Self-service account retirement (settings → Retire account) with typed confirmation
- Self-service device management (settings → Manage devices) — list and revoke your own devices
- First-run wizard with key generation + passphrase + mandatory backup acknowledgement

## Architecture

```
┌──────────────────────────────────────┐
│  sshkey-term (terminal client)       │
├──────────────────────────────────────┤
│  Bubble Tea         UI chrome        │
│  block-cell         inline images    │
│  x/image/draw       thumbnail resize │
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
- **rasterm** -- when the terminal advertises kitty / iTerm2 / WezTerm / Ghostty graphics-protocol support, the sidebar preview pane renders images at native protocol resolution via [BourgeoisBear/rasterm](https://github.com/BourgeoisBear/rasterm). Detected at startup via env-var probes (no terminal-attribute query, which would conflict with bubbletea's stdin reader). Modal-state-aware deselect emits a kitty graphics-protocol delete escape so images don't persist behind overlays.
- **block-cell inline images** (universal fallback) -- truecolor (or 256-color) Unicode quadrant blocks rendered as ordinary text cells. Lives in bubbletea's text-cell layer, no graphics-protocol overlay required, so it works on every terminal. For crisp output, set your terminal's line-height to 1.0 (see KEYBINDINGS.md).
- **golang.org/x/image/draw** -- downscaling source images to thumbnail size for both encoders
- **Go core** -- SSH connection, protocol handling, E2E crypto, local encrypted DB (go-sqlcipher, requires cgo)

## Requirements

- Go 1.25 or later
- C compiler (for go-sqlcipher / CGO -- gcc, clang, or Xcode command line tools)

## Recommended Terminals

Image previews work in **every** terminal via the universal block-cell fallback. The high-fidelity rasterm path (native-resolution kitty / iTerm2 graphics-protocol placement) lights up automatically when the terminal advertises support; everything else falls back to block-cell. Text, reactions, TUI layout, and navigation work everywhere regardless.

| Terminal | Image path | Protocol | Platform |
|---|---|---|---|
| **kitty** | rasterm | kitty graphics | Linux, macOS |
| **iTerm2** | rasterm | iTerm2 inline | macOS |
| **WezTerm** | rasterm | kitty graphics | Linux, macOS, Windows |
| **Ghostty** | rasterm | kitty graphics | Linux, macOS |
| **foot** | block-cell | -- (sixel-only terminals; sixel probing is unsafe inside bubbletea, so foot uses the universal fallback) | Linux (Wayland) |
| **Contour** | block-cell | -- (same reason as foot) | Linux, macOS |
| Terminal.app | block-cell | -- | macOS |
| Windows Terminal | block-cell | -- | Windows |
| basic xterm | block-cell | -- | Linux |

The client auto-detects rasterm-capable terminals via env vars at startup (`$KITTY_WINDOW_ID`, `$TERM_PROGRAM`) — no terminal probing, which would conflict with bubbletea's stdin reader. Set `SSHKEY_NO_RASTERM=1` to force the block-cell path even on capable terminals (useful for diagnostics or aesthetic preference).

Works over SSH -- the graphics protocol passes through to your local terminal. Use one of the rasterm-capable terminals locally for the highest-fidelity preview experience; SSH'ing into a server from any of these terminals will use rasterm in the local terminal regardless of what's installed on the server side.

## Install

```bash
# Install via go install (requires cgo for SQLCipher)
CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go install github.com/brushtailmedia/sshkey-term@latest
```

Or download pre-built binaries from [Releases](https://github.com/brushtailmedia/sshkey-term/releases).

## Build from source

```bash
# Build with FTS5 full-text search support (recommended)
CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" go build -o sshkey-term .

# Or build without FTS5 (search falls back to LIKE queries)
go build -o sshkey-term .

./sshkey-term
```

On first launch, the client prompts to select or generate an Ed25519 SSH key, then connect to a server.

## Configuration

```
~/.sshkey-term/
└── config.toml              global config, server list, device ID
├── chat.example.com/
│   ├── messages.db          encrypted local DB (all rooms + DMs)
│   └── files/               cached attachments
└── work.company.com/
    ├── messages.db
    └── files/
```

```toml
# ~/.sshkey-term/config.toml

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

**Your Ed25519 SSH key is your permanent identity.** 

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

## Documentation

| File | Contents |
|---|---|
| [DESIGN.md](DESIGN.md) | Client architecture: local DB schema, caching, slash commands, focus model, sync flow, offline mode |
| [KEYBINDINGS.md](KEYBINDINGS.md) | Keyboard shortcuts and slash commands — quick reference |
| [APPLICATION-LAYOUT.md](APPLICATION-LAYOUT.md) | Visual layout: panels, message rendering, color palette |

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
