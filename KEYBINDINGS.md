# sshkey-term — Keyboard Shortcuts and Slash Commands

> Quick reference. For architecture details see [DESIGN.md](DESIGN.md). For visual layout see [APPLICATION-LAYOUT.md](APPLICATION-LAYOUT.md).

---

## Keyboard Shortcuts

`Ctrl` on Linux/Windows, `Cmd` on macOS. Bubble Tea maps both — the code uses `tea.KeyCtrl*` which works on all platforms.

### Global

| Key | macOS | Action |
|---|---|---|
| `Enter` | `Enter` | Send message |
| `Esc` | `Esc` | Close panel / cancel / back to input |
| `Tab` | `Tab` | Cycle focus: Input → Sidebar → Messages → Members → Input |
| `Ctrl+K` | `Cmd+K` | Quick switch (fuzzy room/DM picker) |
| `Ctrl+N` | `Cmd+N` | New conversation (DM or group DM) |
| `Ctrl+M` | `Cmd+M` | Toggle member panel |
| `Ctrl+P` | `Cmd+P` | Toggle pinned messages |
| `Ctrl+I` | `Cmd+I` | Room/group info panel |
| `Ctrl+,` | `Cmd+,` | Settings |
| `Ctrl+F` | `Cmd+F` | Search |
| `Ctrl+1-9` | `Cmd+1-9` | Switch server tab |
| `Ctrl+Q` | `Cmd+Q` | Quit (confirmation dialog) |
| `Ctrl+C` | `Ctrl+C` | Force quit (alternative to Ctrl+Q) |
| `?` | `?` | Open help screen (when not in input) |

### Navigation

| Key | macOS | Action |
|---|---|---|
| `↑/↓` | `↑/↓` | Navigate sidebar / scroll messages (context-dependent) |
| `j/k` | `j/k` | Navigate (vim-style, when not in input) |
| `PgUp` | `Fn+↑` | Scroll message history up (triggers lazy scroll-back from server) |
| `Alt+↑/↓` | `Opt+↑/↓` | Switch to previous/next room or DM |
| `Enter` | `Enter` | Confirm sidebar selection (when FocusSidebar) |

### Message Actions (when a message is selected in FocusMessages)

| Key | Action |
|---|---|
| `r` | Reply to selected message |
| `e` | Open emoji reaction picker |
| `u` | Remove your reaction from selected message |
| `p` | Pin/unpin selected message (rooms only) |
| `d` | Delete selected message (own only; admin: any in rooms) |
| `c` | Copy message text to clipboard |
| `g` | Jump to parent message (on a reply) |
| `t` | Open thread view (message + all replies) |
| `o` | Open attachment in system viewer |
| `s` | Save attachment to disk |
| `Enter` | Open context menu for selected message |

### Input Bar (when FocusInput)

| Key | macOS | Action |
|---|---|---|
| `Enter` | `Enter` | Send message (or execute slash command) |
| `Tab` | `Tab` | Accept top tab-completion suggestion |
| `Ctrl+A` | `Cmd+A` | Select all text in input |
| `Ctrl+U` | `Cmd+U` | Clear input |
| `Ctrl+V` | `Cmd+V` | Paste (text or file path for upload) |

### Reaction Picker (when open)

| Key | Action |
|---|---|
| `←/→/↑/↓` | Navigate emoji grid |
| `Enter` | Select emoji |
| Type | Filter emoji by name |
| `Esc` | Cancel |
| `1`-`8` | Quick select from top row (👍 👎 😂 ❤️ 🎉 😮 😢 🔥) |

---

## Slash Commands

### Messaging

| Command | Context | Action |
|---|---|---|
| `/upload [path]` | Room, group, DM | Upload a file |
| `/rename [name]` | Group only | Rename current group DM |

> Note: Reply (`r`), react (`e`), pin (`p`), delete (`d`), and unreact (`u`) are keyboard shortcuts on selected messages, not slash commands. See the Message Actions table above.

### Conversation Management

| Command | Context | Action |
|---|---|---|
| `/leave` | Room, group | Leave current room or group (confirmation dialog). Rejected for 1:1 DMs — use `/delete`. |
| `/delete` | Room, group, DM | Delete conversation from your view (confirmation dialog). Purges local messages + tells server. Active vs retired rooms get different dialog wording. |
| `/mute` | Room, group | Toggle mute (local only, suppresses notifications + bell) |

### Navigation & Info

| Command | Context | Action |
|---|---|---|
| `/search [query]` | Any | Open search overlay (FTS5 if available, LIKE fallback) |
| `/help` | Any | Show available commands |
| `/settings` | Any | Open settings panel |
| `/verify [user]` | Any | Show safety number for user verification |
| `/unverify [user]` | Any | Clear verification status |
| `/mykey` | Any | Show your own public key + fingerprint |
| `/pending` | Any (admin) | Show pending key approval requests |

---

## Tab Completion

Inline popup triggered by prefix characters in the input bar. `Tab` accepts the top suggestion, `↑/↓` to navigate, `Esc` to dismiss.

| Trigger | Completes | Source |
|---|---|---|
| `@` | User display names | Current room/group/DM member list |
| `/` | Slash commands | Command list (with descriptions) |
| `#` | Room names | Room list |

**Behaviour:**
- Popup appears after 1 character past the trigger (`@b`, `/r`, `#e`)
- `Tab` or `Enter` inserts the selected match
- `Esc` or continued typing past a non-match dismisses
- Max 5 suggestions shown, scrollable if more match
- Space after a completed @mention adds the username to the payload's `mentions` field

---

## Keybinding Configuration

Custom keybindings via `~/.sshkey-chat/keybindings.toml`. Only uncommented lines override defaults. Format:

```toml
# ~/.sshkey-chat/keybindings.toml

# [global]
# quit = "ctrl+q"
# quick_switch = "ctrl+k"
# new_conversation = "ctrl+n"
# member_panel = "ctrl+m"
# pinned_messages = "ctrl+p"
# info_panel = "ctrl+i"
# settings = "ctrl+,"

# [navigation]
# prev_room = "alt+up"
# next_room = "alt+down"
# scroll_up = "pageup"
# scroll_down = "pagedown"

# [message]
# reply = "r"
# react = "e"
# unreact = "u"
# pin = "p"
# delete = "d"
# copy = "c"
# thread = "t"
# jump_to_parent = "g"
# open_attachment = "o"
# save_attachment = "s"

# [input]
# send = "enter"
```

Load order: built-in defaults → `keybindings.toml` overrides. Invalid bindings are logged and ignored.
