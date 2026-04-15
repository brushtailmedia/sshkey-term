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

### Member Panel Admin Shortcuts (when a member is selected in group context)

Active only when the current context is a group DM and the local user is an admin of that group. Inactive in rooms and 1:1 DMs.

| Key | Action |
|---|---|
| `A` | Open `/add` dialog (ignores focused row; opens target picker) |
| `K` | Open `/kick` confirmation for focused member |
| `P` | Open `/promote` confirmation for focused member |
| `X` | Open `/demote` confirmation for focused member. `X` is used because `d` means delete elsewhere in the app. |

### Confirmation Dialogs (y/n)

All confirmation dialogs — leave, delete, kick, promote, demote, add, transfer, quit, retire — share the same key bindings:

| Key | Action |
|---|---|
| `y` or `Enter` | Confirm action |
| `n` or `Esc` | Cancel |

The retirement dialog additionally requires typing `RETIRE MY ACCOUNT` into a text field before `Enter` is accepted.

### Last-Admin Promote Picker

When a sole admin runs `/leave` or `/delete` on a group with other members, the last-admin picker opens instead of the leave/delete dialog. The picker lists all non-admin members; the user picks a successor who is promoted before the leave/delete completes.

| Key | Action |
|---|---|
| `↑` / `↓` | Navigate candidate list |
| `Enter` | Promote selected member and continue with the leave/delete |
| `Esc` | Cancel (stays in the group) |

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
| `/rename [name]` | Group only (admin) | Rename current group DM. Non-admin attempts get a friendly client-side rejection. |

> Note: Reply (`r`), react (`e`), pin (`p`), delete (`d`), and unreact (`u`) are keyboard shortcuts on selected messages, not slash commands. See the Message Actions table above.

### Conversation Management

| Command | Context | Action |
|---|---|---|
| `/leave` | Room, group | Leave current room or group (confirmation dialog). Rejected for 1:1 DMs — use `/delete`. Groups: last-admin attempts open a promote picker before the leave. |
| `/delete` | Room, group, DM | Delete conversation from your view (confirmation dialog). Purges local messages + tells server. Active vs retired rooms get different dialog wording. Groups: last-admin attempts open a promote picker before the delete. |
| `/mute` | Room, group | Toggle mute (local only, suppresses notifications + bell) |

### Group admin commands (Phase 14)

All five admin verbs are scoped to the current group. Each runs a local pre-check against the cached `is_admin` flag — non-admins get a friendly rejection before the request hits the wire. On the server side, non-admin rejections collapse to the same `ErrUnknownGroup` frame as non-member rejections (byte-identical privacy).

| Command | Context | Action |
|---|---|---|
| `/add @user` | Group (admin) | Add a user to the current group. Opens confirmation dialog showing target + resulting member count. |
| `/kick @user` | Group (admin) | Remove a user from the current group. Opens confirmation dialog showing target + resulting member count. Tracked for `/undo`. |
| `/promote @user` | Group (admin) | Grant admin to a member of the current group. Opens confirmation dialog explaining the flat-peer model ("all admins are peers — there is no protected tier"). |
| `/demote @user` | Group (admin) | Revoke admin from a member of the current group. Confirmation dialog warns when the group would drop to one remaining admin. |
| `/transfer @user` | Group (admin) | Atomic promote-then-leave handoff. If target is already an admin, flow collapses to just leaving. |

### Group status commands (Phase 14)

| Command | Context | Action |
|---|---|---|
| `/members` | Group | Read-only overlay listing members with ★ admin markers |
| `/admins` | Group | Read-only overlay pre-filtered to just admins |
| `/role @user` | Group | Shows whether the target is admin or regular member |
| `/whoami` | Group | Shows your own role in the current group |
| `/groupinfo` | Group | Opens the info panel (Ctrl+I equivalent) |
| `/audit [N]` | Group | Read-only overlay showing recent admin actions (default 10). Reads from local `group_events` table — populated from live broadcasts and offline sync replay. |
| `/undo` | Group (admin) | Revert your most recent kick within 30 seconds — re-adds via `add_to_group`. Exactly one kick tracked, no stack. |

### Creation commands

| Command | Context | Action |
|---|---|---|
| `/groupcreate ["name"] @a @b @c` | Any | Inline group DM creation. Bypasses the new-conversation wizard. |
| `/dmcreate @user` | Any | Inline 1:1 DM creation |

### Navigation & Info

| Command | Context | Action |
|---|---|---|
| `/search [query]` | Any | Open search overlay (FTS5 if available, LIKE fallback) |
| `/topic` | Room only | Show the current room topic in the status bar (read-only). Groups and 1:1 DMs show "only available in rooms". Rooms with no topic set show "has no topic set". Changing a topic is deferred to a future phase. |
| `/help` | Any | Show available commands. In a group context where you are admin, admin verbs are included; elsewhere they are hidden (context-aware filtering). |
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
| `@` | User display names | Current room/group/DM member list (context-aware; see below) |
| `/` | Slash commands | Command list (with descriptions) |
| `#` | Room names | Room list |

**Behaviour:**
- Popup appears after 1 character past the trigger (`@b`, `/r`, `#e`)
- `Tab` or `Enter` inserts the selected match
- `Esc` or continued typing past a non-match dismisses
- Max 5 suggestions shown, scrollable if more match
- Space after a completed @mention adds the username to the payload's `mentions` field

**Context-aware `@` completion in group commands (Phase 14):**

| Leading verb | Autocomplete source |
|---|---|
| `/kick`, `/promote`, `/demote`, `/transfer`, `/role` | **Current group members** — `@` autocompletes against the member list |
| `/add` | **Non-members** — `@` autocompletes against users who are NOT currently in the group |
| `@` without a leading admin verb | Default member list (unchanged) |

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
