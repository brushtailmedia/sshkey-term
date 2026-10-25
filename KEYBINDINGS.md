# sshkey-term — Keyboard Shortcuts and Slash Commands

> Quick reference. For architecture details see [DESIGN.md](DESIGN.md). For visual layout see [APPLICATION-LAYOUT.md](APPLICATION-LAYOUT.md).

---

## Keyboard Shortcuts

`Ctrl` is the Control key on every platform, macOS included — `Cmd` never reaches a terminal app (`Cmd+Q` quits the terminal itself). `Alt` is `Option` on macOS, and only when the terminal sends Escape for it (Terminal.app "Use Option as Meta key", iTerm2 "Left Option key: Esc+").

### Global

| Key | macOS | Action |
|---|---|---|
| `Enter` | `Enter` | Send message |
| `Esc` | `Esc` | Close panel / cancel / back to input |
| `Tab` / `Shift+Tab` | `Tab` / `Shift+Tab` | Cycle focus forwards / backwards: Input → Sidebar → Messages → Members → Input. In the input, `Tab` completes when it can: at the end of a `/command` or `@name` word it opens the completion popup (or accepts the highlighted suggestion); anywhere else in the draft it cycles |
| `Ctrl+g` | `Ctrl+g` | Enter navigation prefix mode |
| `Ctrl+P` | `Ctrl+P` | Toggle pinned messages |
| `Ctrl+Q` | `Ctrl+Q` | Quit (confirmation dialog) |
| `Ctrl+C` | `Ctrl+C` | Force quit (alternative to Ctrl+Q) |
| `?` | `?` | Open help screen (when not in input) |

### Navigation Prefix Mode (`Ctrl+g`)

Press `Ctrl+g`, then one of:

| Key | Action |
|---|---|
| `k` | Quick switch |
| `n` | New conversation (DM or group DM) |
| `m` | Toggle member panel |
| `i` | Room/group/DM info panel |
| `s` | Settings |
| `d` | Your devices panel |
| `p` | Your profile panel |
| `/` | Search |
| `1`-`9` | Switch to server N (a digit past the configured count opens Add Server) |
| `h` / `l` | Previous / next server in the ring (Add Server is a ring slot after the last server) |
| `j` | Server quick-switch overlay (configured servers + an Add server row) |
| `o` | Toggle online/offline for the current server — Offline → connect; Online or Connecting → disconnect (or cancel the attempt) and keep browsing local history |
| `r` | Reconnect: drop the current session and open a fresh one — Online → reconnect; Offline → connect; inert while connecting |
| `g` or `Esc` | Cancel nav mode |

After `Ctrl+g`, a which-key popup lists these continuations once you pause (300 ms). Nav mode does not auto-exit: press a listed key to run it, `g`/`Esc` to cancel, or any other key/click to dismiss.

### Navigation

| Key | macOS | Action |
|---|---|---|
| `↑/↓` | `↑/↓` | Navigate sidebar / scroll messages (context-dependent). `↓` on the last message moves focus to the input |
| `j/k` | `j/k` | Move down / up (j/k navigation, when not in the input). `j` on the last message moves focus to the input |
| `←/→` | `←/→` | Move focus to the panel in that direction: Sidebar ↔ Messages ↔ Members (when open). From the input only when the draft is empty; with text they move the cursor |
| `h/l` | `h/l` | Same as `←/→` when not in the input (j/k navigation) |
| `PgUp` | `Fn+↑` | Scroll message history up (triggers lazy scroll-back from server) |
| `Alt+↑/↓` | `Opt+↑/↓` | Switch to previous/next room or DM |
| `Enter` | `Enter` | Confirm sidebar selection (when FocusSidebar) |

### Message Actions (when a message is selected in FocusMessages)

| Key | Action |
|---|---|
| `r` | Reply to selected message |
| `a` | Edit ("alter") your most recent message in the conversation — the only one the server accepts an edit for. That message shows an `a=edit` hint when selected; on any other message the key does nothing. The body goes into the input in edit mode: `Enter` sends the edit, `Esc` cancels. Also in the thread view, and as Edit in the context menu |
| `e` | Open emoji reaction picker |
| `u` | Remove your reaction from selected message |
| `p` | Pin/unpin selected message (rooms only) |
| `d` | Delete selected message (own only; admin: any in rooms) |
| `c` or `y` | Copy message text to clipboard (`y`: the vim/terminal copy-mode habit; same action) |
| `C` or `Y` | Copy the link in the message to the clipboard; with several links a picker lists them (`Enter` chooses, `q` closes) |
| `g` | Jump to parent message (on a reply) |
| `t` | Open the thread view on the whole thread the selected message belongs to (it walks up to the top-level message; the cursor lands on the selected message), as a tree three levels deep with who reacted with what; deeper replies are hidden behind a gray `↳ N more replies ↓` stub drawn beneath the row at the cap, where the first hidden reply would sit (the trailing `↓` says the thread continues below the cap). Inside it: `t` re-opens the view rooted at the highlighted reply (any reply that has replies — the hidden ones become visible); `Enter` closes the view and goes to the highlighted message in the pane; on the `..` row at the top of a nested view both `t` and `Enter` open the parent thread; `r` replies to the highlighted message, `e` opens the emoji picker on it and `a` edits it when it is your latest message (its row shows `a=edit`), all closing the view first so the input (or the picker) is in front; `↑`/`↓` or `k`/`j` move the highlight and pull the window only at its edges, `PgUp`/`PgDn` scroll a screen without moving it, `Home` scrolls to the top, `End` jumps to the last row; `c`/`y` copy the highlighted message's text, `C`/`Y` copy its link and `o` opens its attachment or link (the footer confirms, since the status bar is behind the view; several links close the view for the picker); `Esc` or `q` closes |
| `o` | Open: the attachment in the system viewer when the message has one, otherwise the link in the default browser (several links → a picker). Over SSH the browser is on your own machine, so `o` copies the link instead and says so; links are also OSC 8 hyperlinks, which most terminals open on `Cmd`/`Ctrl`+click |
| `s` | Save attachment to disk |
| `Enter` | Open the context menu for the selected message (Reply; Edit on your most recent message in the conversation — the only one the server accepts an edit for; React; remove a reaction; Thread when the message has replies, is a reply or has reactions; Pin/Unpin; Open/Save attachment; Delete; Copy). `j`/`k` move, `Enter` chooses, `q` or `Esc` closes |

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
| `Enter` | `Enter` | Send message (or execute slash command). If the input is in edit mode, dispatches the appropriate edit verb instead. With the completion popup open, `Enter` accepts the highlighted suggestion when that would change the text, otherwise it sends — a command typed in full never needs `Enter` twice. |
| `Ctrl+J` (preferred) or `Alt+Enter` | `Ctrl+J` (preferred) or `Option+Enter` | Insert a newline in the draft (multi-line message). **Prefer `Ctrl+J`**: it is a single byte that every terminal sends unchanged. `Alt+Enter` needs Option set to send Esc on macOS, is taken for fullscreen by some terminals (Windows Terminal, WezTerm), and on a slow link can arrive as Esc followed by Enter. Shift+Enter cannot be used: a terminal sends the same byte for it as for Enter. |
| `Tab` | `Tab` | Complete when it can, else cycle focus. The completion popup opens on its own as you type a `/command` (first word) or an `@name` (at a word start), from the first character: a bare `/` lists every command (alphabetically) and a bare `@` every other member (never yourself), in a window of eight rows with `↑ N more` / `↓ N more` markers; typing narrows it, `Backspace` widens it, a space or a word nothing matches closes it. It is drawn just above the input box. With the popup open, `Tab` or `→` accepts the highlighted suggestion (`↑`/`↓` move through it); after `Esc` dismissed it for the word, `Tab` reopens it. Anywhere else in the draft, `Tab` cycles focus |
| `Up` | `Up` | Moves focus up to the message pane, draft or no draft — the composer is a single line, so `Up` has nothing to do in it; the draft and its cursor stay, and `↓` from the last message (or `Esc`) brings you back to them, e.g. to copy text from another message mid-draft. The pane cursor snaps to the last message when none is selected and is kept otherwise, so `↓` then `↑` is a round trip. The one exception: while the `@`/`/` completion popup is open, `Up` is its previous suggestion. Editing your latest message is `a` in the pane (or Edit in the context menu); it used to be this key. |
| `Esc` | `Esc` | With the completion popup open, closes the popup and keeps the draft; it does not reopen for that word (a space ends the word, and `Tab` reopens it). Otherwise, **Phase 15**: if in edit mode, clears the buffer and exits edit mode without dispatching; otherwise moves focus to the sidebar. |
| `Ctrl+a` | `Ctrl+a` | Move cursor to start of line (inherited textinput/readline behavior) |
| `Ctrl+u` | `Ctrl+u` | Delete from cursor to start of line (inherited textinput/readline behavior) |
| `Cmd+v` / terminal paste key | `Cmd+v` | Paste text from the terminal/OS clipboard. This is terminal-level behavior, not an app-remappable keybinding. |

### Reaction Picker (when open)

| Key | Action |
|---|---|
| `←/→/↑/↓` | Navigate emoji grid |
| `Enter` | Select emoji |
| Type | Filter emoji by name |
| `Esc` | Cancel |
| `1`-`8` | Quick select from top row (👍 👎 😂 ❤️ 🎉 😮 😢 🔥) |

### File Picker (when open — bare `/upload` or `/u`)

A keyboard-only tree explorer over the local filesystem (of the machine the client runs on), starting at your home directory and remembering where you last browsed for the session. Folders expand in place (`▸`/`▾`); only the rows you have expanded are searched by the filter. Hidden (dot) files are not listed.

| Key | Action |
|---|---|
| Type | Filter the loaded (expanded) rows |
| `↑` / `↓` / `PgUp` / `PgDn` / `Home` / `End` | Move the cursor |
| `→` | Expand the folder under the cursor |
| `←` | Collapse the folder; on a file, jump to its parent folder |
| `Enter` | `../` = up one level · folder = expand/collapse · file = upload it |
| `Backspace` | Edit the filter (never navigates) |
| `Esc` | Cancel |

---

## Slash Commands

### Connection (offline browse)

When a configured server cannot be reached, the app still opens on that server's cached history (sidebar, rooms, groups, DMs) with an `offline ●` footer. Sends and other server actions are blocked until you reconnect; `/help`, `/search`, `/mykey`, `/typing` and typed `/whois <user>` keep working offline.

| Command | Context | Action |
|---|---|---|
| `/online` or `/connect` | Any | Connect (or reconnect) the current server. Also `Ctrl+g o` while offline. |
| `/offline` or `/disconnect` | Any | Disconnect and keep browsing local history. Also `Ctrl+g o` while online or connecting. |

### Messaging

| Command | Context | Action |
|---|---|---|
| `/upload [path]` | Room, group, DM | Upload a file. Bare `/upload` opens the file picker (below); a typed path accepts `~/…`, quoted and backslash-escaped drag-and-drop paths. |
| `/u [path]` | Room, group, DM | Alias for `/upload` |
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
| `/setstatus <status>` | Any | Set your presence status. Locked set: `available` (green dot), `away` (amber dot), `busy` (red dot). Reflected in the presence dot next to your name everywhere it appears (sidebar groups/DMs, member panel, profile). |
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

Keybindings are fixed; there is no configuration file for them.
