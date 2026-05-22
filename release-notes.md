# sshkey-term v0.4.0

A terminal client for [sshkey-chat](https://github.com/brushtailmedia/sshkey-chat) — private messaging over SSH with end-to-end encryption. Your SSH key is your identity: no accounts, no passwords, no signup. The server is a blind relay that sees routing metadata but never message content.

> **Pre-1.0 software.** Expect breaking changes until v1.0. v0.4.0 is the current baseline release.

## What it does

- **End-to-end encrypted** rooms, 1:1 DMs, and group DMs (AES-256-GCM, X25519 key wrapping, Ed25519 signatures)
- **Forward secrecy** — rooms use epoch-based key rotation; DMs use per-message keys (Signal-level)
- **SSH key = permanent identity** — the server never sees your private key or passphrase, only the public key
- **Local-first** — encrypted local database (SQLCipher) with full-text search; offline history with lazy scroll-back, server fallback when needed
- **In-group admin model** for group DMs — `/add`, `/kick`, `/promote`, `/demote`, `/transfer` with confirmation dialogs, `/audit` for recent admin actions, `/undo` for 30-second kick revert
- **Multi-server** — `Ctrl+1`–`9` to switch between configured servers
- **Inline images** — native-resolution via the kitty / iTerm2 / WezTerm / Ghostty graphics protocols, with a universal block-cell fallback that works on *every* terminal — no "your terminal isn't supported" cliff
- File sharing, reactions, typing indicators, read receipts, presence, pinned messages, thread view, reply preview, quick switch (`Ctrl+K` fuzzy search)
- Self-service account retirement and device management from Settings
- First-run wizard with key generation, optional passphrase, and a mandatory backup acknowledgement

## Highlights in v0.4.0

### Reliability

- **Encrypted SSH keys now work on first launch.** Previously, generating a passphrase-protected key via the wizard (the recommended path) caused the app to hang at `Connecting…` forever with no passphrase prompt — a deadlock between the key loader and the connect flow, compounded by the passphrase dialog never rendering pre-connect. Both halves are fixed: the app now detects an encrypted key before connecting and prompts correctly.
- **Attachment files no longer leak on bulk delete.** `/delete` on a room, group, or DM (and the hidden-DM sync path) previously dropped messages from the local database but left their attachment files orphaned on disk — a privacy gap (deleted images stayed on your machine) and an unbounded disk-growth issue. Bulk delete now cleans up attachment files the same way single-message delete already did, with proper error logging.
- **Removing a server no longer mis-targets the active server.** Removing a server listed *above* the one you're connected to previously shifted the internal active-server pointer, so subsequent settings actions silently affected the wrong server. Fixed with correct reindexing.
- **Hardened the Add Server key scan** against hand-edited config files with malformed host values.
- **`/topic <new topic>` no longer silently drops the argument** — multi-word topics now reach the server correctly (the parser was discarding the text after the command).

### Topic updates, end-to-end

Setting a room topic with `/topic` now closes the full UX loop with no leave-and-rejoin required: the messages-pane header refreshes live, the status bar transitions from "pending" to "Topic updated," and an inline system message ("alice changed the topic to …") appears in the room — all driven by the server's broadcast.

### Storage layout (clean baseline)

v0.4.0 ships a fully per-server on-disk layout. Each configured server owns a self-contained folder holding its keys, host-key pin, log, encrypted database, and attachment cache. Removing a server cleanly deletes everything it owned in one operation. The `config.toml` schema is simpler — there is no `key` field; the key path is derived from the per-server layout automatically. On Add Server / Wizard the chosen key is **always copied** into the new server's folder, so the app never depends on the original file's location afterward.

```
~/.sshkey-term/
├── config.toml              shared: server list, device ID
└── <host>/
    ├── keys/
    │   ├── id_ed25519
    │   └── id_ed25519.pub
    ├── known_host           pinned SSH host key (TOFU)
    ├── client.log
    ├── messages.db          encrypted local DB
    └── files/               cached attachments
```

Internally this was a large path-centralization refactor (all managed-path construction now flows through one canonical layer, enforced by a structural test). It also aligned several protocol structs with the server side and removed dead handshake-capability wiring — no functional change to messaging or sync.

## Security model

Three layers of protection, used in combination:

| Layer | Protects against | How |
|---|---|---|
| **Passphrase** | Stolen device — key at rest | Set one when generating your key (wizard prompts by default) |
| **Device revocation** | Stolen device, key/passphrase believed intact | Settings → Manage devices, or ask your admin to revoke |
| **Account retirement** | Key compromise | Settings → Retire account (typed confirmation) |

Retirement is monotonic and irreversible. Back up your key — if you lose both the key and its passphrase with no backup, the server cannot recover your account. The first-run wizard requires you to acknowledge this before connecting.

## Install

```bash
CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm" \
  go install github.com/brushtailmedia/sshkey-term@latest
```

Or grab a pre-built binary from the release assets below.

**Requirements:** Go 1.25+, a C compiler (CGO is required for the encrypted SQLCipher database).

## Known limitations

- **Same host, different ports collide.** Two server entries with the same `host` but different `port` share `<configDir>/<host>/` and will overwrite each other's data. Workaround: give them distinct host values (e.g. DNS aliases) until per-port disambiguation lands.
- Pre-1.0 — config schema and on-disk layout may still change before v1.0.

## Terminal support

Text, layout, navigation, and reactions work in every ANSI terminal. High-fidelity inline images light up automatically on kitty, iTerm2, WezTerm, and Ghostty; everything else (Terminal.app, Windows Terminal, foot, Contour, xterm, …) uses the universal block-cell fallback. Works over SSH — the graphics protocol passes through to your local terminal.
