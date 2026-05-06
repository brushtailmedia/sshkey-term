# sshkey-term — Application Layout

> Visual layout specification for the sshkey-term terminal UI. For architecture see [DESIGN.md](DESIGN.md). For keyboard shortcuts see [KEYBINDINGS.md](KEYBINDINGS.md).

---

## Layout

Three-panel layout. Member panel is toggleable.

```
┌─ sshkey-term ─── ● Personal │ Work ──────────────────────────────────────┐
│                                                                           │
│  ┌─ Sidebar ────┐  ┌──────────────────────────────────────────────────┐  │
│  │              │  │   #general                                        │  │
│  │  Rooms       │  │   General chat — please be nice                   │  │
│  │  # general  ◀│  │                                                   │  │
│  │  # engineer  │  │  ▶ N pinned messages (rooms only, if any)         │  │
│  │  # design    │  ├───────────────────────────────────────────────────┤  │
│  │              │  │                                                   │  │
│  ├──────────────┤  │  message stream                                   │  │
│  │  Messages    │  │                                                   │  │
│  │  ● Bob       │  │                                                   │  │
│  │  Carol    (2)│  │  ── alice is typing... ─────────────────────      │  │
│  │  Project A   │  │                                                   │  │
│  ├──────────────┤  ├───────────────────────────────────────────────────┤  │
│  │  DMs         │  │ > input                                           │  │
│  │  ● Dave    ✓ │  └───────────────────────────────────────────────────┘  │
│  └──────────────┘                                                         │
│                                                                           │
│  E2E encrypted · 3 members · epoch 12                          alice ●   │
└───────────────────────────────────────────────────────────────────────────┘
```

**Messages pane header (Phase 18).** The messages pane shows a two-line header pinned at the top:
- **Line 1** — bold context title. Room display name in room contexts, group name in group contexts, other party's display name in 1:1 DMs. Falls back to "no room selected" when nothing is selected.
- **Line 2** — dim italic room topic. **Rooms only.** Omitted entirely for groups, 1:1 DMs, and rooms that have no topic set. When present, it shows the topic value the server served in the most recent `room_list` refresh (via `RoomInfo.Topic`).

Phase 18 shipped the display-only path: the client already persists topics in the local `rooms` table (Phase 7b) and the server sends them on every `room_list`. Phase 18 just wires that data through to the TUI via `Client.DisplayRoomTopic(roomID)` → `MessagesModel.SetRoomTopic`. Changing a topic post-creation (via a CLI verb) and broadcasting a live update (`room_updated` event) is deferred to the Admin CLI audit phase.

Header line 1 uses the `searchHeaderStyle` (bold, accent-colored) for visual consistency with the info panel title bar. Line 2 uses `helpDescStyle` (muted, dim italic) to visually subordinate the topic to the room name — the name is the primary identifier, the topic is context. A blank line separates the header from the message stream below.

**Server tabs:** top bar shows tabs for each configured server. Active tab is highlighted. Connection dot to the left of the active server name. `Ctrl+1`/`Ctrl+2`/etc or click to switch servers. Single server = no tabs, just the server name.

**Connection status dot (left of server name):**

```
● green   connected
● amber   reconnecting (pulses: ● ○ ● ○)
● red     disconnected / offline
```

With member panel (Ctrl+M):

```
┌─ Sidebar ────┐  ┌─ #general ──────────────────────┐  ┌─ Members ──┐
│  ...          │  │  messages...                     │  │  ● alice   │
│              │  │                                  │  │  ● bob   ◀ │
│              │  │                                  │  │  ○ carol   │
│              │  ├──────────────────────────────────┤  │            │
│              │  │ > input                          │  │            │
└──────────────┘  └──────────────────────────────────┘  └────────────┘
```

Member panel is navigable with `↑/↓` when focused (`Tab` to focus). `Enter` or click on a member opens the member context menu:

```
┌──────────────────────┐
│  Message bob          │   ← opens 1:1 DM
│  Create group with... │   ← opens new conversation dialog with bob pre-selected
│  Verify bob           │   ← safety numbers
│  View profile         │
└──────────────────────┘
```

---

## Sidebar

Three sections: Rooms, Messages (group DMs), DMs (1:1).

### Rooms section

```
┌─ Rooms ───────────┐
│  # general       ◀│   ← selected (highlighted)
│  # engineer       │
│  # design     (3) │   ← unread count
│  # old-proj (retired)│  ← retired by admin, greyed, no unread
│  # archive  (left)│   ← user self-left on another device, greyed
│  # banned (removed)│  ← admin removed user, greyed
│  # defunct (retired)│  ← user's account was retired, greyed
```

- `#` prefix for rooms
- `◀` or highlight for current selection
- `(N)` unread badge (suppressed for retired and left rooms)
- `(retired)` (room-level) marker — room archived by admin (Phase 12). Takes priority over leave suffixes when both flags are set.
- **Phase 20 leave suffixes** — distinct per-reason suffix sourced from the server's authoritative `left_rooms` catchup:
  - `(left)` — user self-left on another device (`leave_reason=""`)
  - `(removed)` — admin removed the user via `sshkey-ctl remove-from-room` (`leave_reason="removed"`)
  - `(retired)` — user's account was retired (`leave_reason="user_retired"`)
- All greyed with `archivedStyle`.
- Sorted by config order

### Room transcript — inline system messages (Phase 20)

Room events are rendered inline as system messages, matching the group-side audit UX. The server authors the event on every admin action and sends it via both live `room_event` broadcasts and `SyncBatch.Events` replay on reconnect:

```
│ ─── Tuesday, April 16 ────────────────────────────
│ ◼ alice changed the topic to "Q2 planning"
│ ◼ alice added bob to the room
│ ◼ bob was removed from the room by an admin
│ ◼ carol's account was retired
│ ◼ alice renamed the room to "planning"
│ ◼ this room was retired by an admin
│ ─── Today ────────────────────────────────────────
│ alice: meeting at 3pm
```

- 5 event types: `leave` (with reason-specific wording), `join`, `topic`, `rename`, `retire`.
- Server-authored, plaintext metadata (see PROJECT.md — "What the server sees" for the encryption-boundary enumeration). Encrypted at rest on the client via SQLCipher.
- Pre-join events are filtered server-side via `room_members.joined_at` — new members don't see audit history from before they joined.

### Messages section (group DMs)

```
├─ Messages ──┤
│  ★ ● Project A│   ← group DM with name; ★ = you are an admin of this group
│  ● Team Chat  │   ← group DM with name (you are a regular member)
│  D, Eve, Fr   │   ← group DM without name, truncated member list
│  Old Group [retired] (left)│  ← member retired + user left
```

- Group DMs show the conversation name, or truncated member list if no name
- **`★` admin marker (Phase 14)** — muted star glyph before the group name when the local user is an admin of that group. Updates live on `group_event{promote,demote}` via the `resolveIsLocalAdmin` callback; persisted in `groups.is_admin` so the indicator survives restarts.
- Online indicator (●) if any member is online
- `[retired]` marker — shown when any member of the group has a retired account
- `(left)` marker — user self-left, greyed
- Unread badge suppressed for left groups

### DMs section (1:1)

```
├─ DMs ────────┤
│  ● Dave     ✓│   ← online, key verified
│  ○ Eve    (2)│   ← offline, 2 unread
│  ○ Frank [retired]│  ← other party retired, greyed
```

- Display name of the other party
- Online indicator (●/○)
- `✓` verification badge (green) — key verified via safety numbers
- `[retired]` marker — other party's account retired
- `(N)` unread badge

---

## Message Stream

### Regular message

```
alice  10:32 AM
Hey everyone, the deploy looks good
```

### Edited message (Phase 15)

```
alice  10:32 AM (edited)
Hey everyone, the deploy looks good (migration applied cleanly)
```

The `(edited)` marker in dim style next to the timestamp indicates the message has been edited since it was sent. The marker appears on any message where `edited_at > 0`. The original send timestamp is preserved (the message stays in its original stream position); `edited_at` is shown only via the marker, not as a separate timestamp. Clients that want to surface the edit time explicitly ("edited 3:06 PM") could do so — today's design is minimal.

When consecutive messages from the same sender are visually grouped (header hidden for messages within 5 minutes), the `(edited)` marker attaches as a trailing annotation on the edited message's body so the user can still tell at a glance which one was edited.

### Message from a retired user

```
alice  10:32 AM  [retired]
Hey everyone, the deploy looks good
```

`[retired]` marker in dim style next to the timestamp when the sender's account has been retired.

### With reply

```
alice  10:34 AM
None so far. Monitoring grafana now
  ↳ re: Nice. Any issues with the migration?
```

### With reactions

```
bob  10:33 AM
Nice. Any issues with the migration?
  👍 2  🎉 1
```

### With attachment

```
carol  10:35 AM
┌──────────────────────┐
│  screenshot.png       │
│  [inline image]       │   ← sixel/kitty/iterm2 if supported
└──────────────────────┘
Looks clean ^
```

Fallback for unsupported terminals:

```
carol  10:35 AM
📎 screenshot.png (230 KB, image/jpeg)
Looks clean ^
```

### System messages

```
  ── carol has joined the room ──
  ── alice renamed the group to "Project Alpha" ──
  ── bob left the group ──
  ── bob was removed from the group by alice ──
  ── alice added bob to the group ──
  ── alice promoted bob to admin ──
  ── alice removed admin from bob ──
  ── alice added bob, carol, and dave to the group ──   (coalesced)
  ── this room was archived by an admin ──
```

**Group event rendering (Phase 14).** All five `group_event` variants (`join`, `leave`, `promote`, `demote`, `rename`) render as system messages. The `by` field shows the acting admin ("alice promoted bob"), or is omitted for self-leave, retirement, and retirement-succession paths. The `quiet` flag suppresses inline rendering but still updates member/admin lists and persists to the local `group_events` table (visible in `/audit`).

**Event coalescing.** Consecutive same-admin same-verb events within 10 seconds collapse into one system message ("alice added bob, carol, and dave"). Applies to `join`, `promote`, `demote`, and `leave` with `reason="removed"`; never coalesces self-leave, retirement, or `rename`. Individual events are still persisted un-coalesced.

### Deleted message (tombstone)

Deleted messages render as inline tombstones in the stream, preserving conversation flow:

```
  ── message deleted ──
  ── message removed by alice ──
```

Self-deletes show "message deleted". Admin deletes show "message removed by {admin_name}".

### Pinned message indicator

```
📌 alice  10:32 AM
Deploy checklist: 1. Run migrations 2. Clear...
```

### Typing indicator

```
  ── alice is typing... ──
  ── alice and bob are typing... ──
  ── 3 people are typing... ──
```

Appears above the input bar. Expires after 5 seconds. Three or more typists collapse to a count.

### Edit mode indicator (Phase 15)

When the input is in edit mode (Up-arrow on empty input populated the buffer with a previously-sent message), an indicator appears above the text input, same style as the reply indicator:

```
 ✎ editing message — Esc to cancel
> Hey everyone, the deploy looks good (migration applied cleanly)█
```

Enter dispatches the edit envelope for the tracked message ID and exits edit mode. Esc clears the buffer and exits edit mode without dispatching. Any context switch (sidebar navigation, quick switch, search jump, slash command routing to a different context) also exits edit mode so a half-finished edit never dispatches to the wrong conversation.

### Read-only banners

Shown at the bottom of the message stream when the context is archived:

```
  ── you left this room — read-only — type /delete to remove from your view ──
  ── this room was archived by an admin — read-only — type /delete to remove from your view ──
  ── you left this group — read-only — type /delete to remove from your view ──
```

Retired-room banner takes priority over the self-leave banner when both flags are set.

### Signature warnings

```
⚠ bob  10:40 AM  [unsigned]
This message was not signed

⚠ bob  10:41 AM  [signature failed]
This message failed signature verification
```

### Replay warning

```
⚠ alice  10:42 AM  [possible replay]
Duplicate message detected
```

---

## Pinned Messages Bar

Collapsed (default):

```
┌─ #general ── pinned ─────────────────────────────────┐
│  ▶ 2 pinned message(s)  (Ctrl+P to expand)           │
├──────────────────────────────────────────────────────┤
```

Expanded (Ctrl+P or click):

```
┌─ #general ── pinned ─────────────────────────────────┐
│  📌 alice: Deploy checklist: 1. Run migrations...     │
│  📌 bob: API docs are at https://docs.example...      │
│  ─────────────────────────────────────────────────── │
```

Click a pinned message to jump to it. Esc to collapse.

---

## Status Bar

```
E2E encrypted · 3 members · epoch 12                          alice ● (admin)
```

- Encryption status (always E2E)
- Member count for current room/conversation
- Current epoch (rooms only)
- Current user + online indicator + `(admin)` badge if server admin (right-aligned)
- Pending keys indicator for admins when unapproved keys exist

**Persistent error messages:**

```
⚠ Forbidden — please contact an admin to leave this room
⚠ Left room
⚠ This room was archived by an admin — type /delete to remove from your view
```

Errors persist in the status bar until the user's next action (typing, navigation). They don't auto-dismiss on a timer.

---

## Context Menu (right-click or Enter on selected message)

**Your own message (room):**
```
┌──────────────────┐
│  Reply            │
│  React            │
│  Thread           │
│  Pin to room      │
│  Delete           │
│  Copy text        │
└──────────────────┘
```

**Someone else's message (room):**
```
┌──────────────────┐
│  Reply            │
│  React            │
│  Thread           │
│  Pin to room      │
│  Copy text        │
└──────────────────┘
```

**Someone else's message (DM/group):**
```
┌──────────────────┐
│  Reply            │
│  React            │
│  Thread           │
│  Copy text        │
└──────────────────┘
```

No pin in DMs or group DMs. No delete on others' messages (exception: server admins see delete on all room messages).

---

## Reaction Picker

After selecting "React" from context menu or pressing `e` on a selected message:

```
┌─ React ──────────────────────┐
│  👍 👎 😂 ❤️  🎉 😮 😢 🔥    │
│  [type to search...]         │
└──────────────────────────────┘
```

Click or arrow keys to select. Type to filter emoji by name. `1`-`8` quick-select from top row.

---

## Info Panels (Ctrl+I)

### Group DM Info Panel

```
┌─ Project Alpha ─── info ──────────────┐
│                                        │
│  Type /leave to stop receiving         │
│  messages, or /delete to remove        │
│  from your view.                       │
│                                        │
│  Muted: [off]  (press m to toggle)     │
│                                        │
│  Members (3):                          │
│   [Admins]                             │
│    ● alice (you) ★                     │
│   [Members]                            │
│    ● bob                               │
│    ○ carol                             │
│                                        │
│  Enter=message  m=mute  Esc=close      │
│  A=add  K=kick  P=promote  X=demote    │  (admin only)
└────────────────────────────────────────┘
```

- Members split into [Admins] and [Members] subsections, admins first
- **`★` marker** on admin rows (Phase 14). The local admin set is sourced from `group_list` catchup + live `group_event{promote,demote}` broadcasts + offline `sync_batch.Events` replay.
- Online/offline dot per member
- Arrow-key focusable member rows, Enter opens DM or member menu
- **Admin verb shortcuts** (Phase 14, group contexts only, admin only): `A`=add, `K`=kick, `P`=promote, `X`=demote on a focused member row. Each opens the corresponding confirmation dialog.
- /leave and /delete hint at top (context-aware: active, left, retired)

### Group DM Info Panel (left state)

```
┌─ Project Alpha ─── info ──────────────┐
│                                        │
│  Status: you left this group           │
│  (read-only)                           │
│  Type /delete to remove from your      │
│  view.                                 │
│  ...                                   │
└────────────────────────────────────────┘
```

### Room Info Panel

```
┌─ #general ─── info ───────────────────┐
│                                        │
│  Type /leave to stop receiving         │
│  messages, or /delete to remove        │
│  from your view.                       │
│                                        │
│  Topic: General chat — please be nice  │
│                                        │
│  Muted: [off]  (press m to toggle)     │
│                                        │
│  Members (12):                         │
│   [Admins]                             │
│    ● alice (you) ✓                     │
│   [Members]                            │
│    ● bob ✓                             │
│    ○ carol                             │
│    ○ dave                              │
│                                        │
│  Enter=message  m=mute  Esc=close      │
└────────────────────────────────────────┘
```

The `Topic:` line (Phase 18) is populated from the client's local `rooms` table via `Client.DisplayRoomTopic(roomID)`. When the room has no topic set, the line is omitted entirely — the existing `if i.topic != ""` guard has been in the render code since v0.1.0; Phase 18 just populates the field. Topic updates are picked up on reconnect (when the server re-sends `room_list`); live updates via `room_updated` broadcast are deferred to the Admin CLI audit phase.

### Room Info Panel (retired state)

```
┌─ #general_V1St ─── info ──────────────┐
│                                        │
│  Status: this room was archived by     │
│  an admin (read-only)                  │
│  Type /delete to remove from your      │
│  view.                                 │
│  ...                                   │
└────────────────────────────────────────┘
```

### 1:1 DM Info Panel

```
┌─ DM with Bob ─── info ───────────────┐
│                                        │
│  Type /delete to remove this           │
│  conversation from your view.          │
│                                        │
│  Muted: [off]  (press m to toggle)     │
│                                        │
│    ● alice (you) ✓                     │
│    ○ bob                               │
│                                        │
│  Enter=message  m=mute  Esc=close      │
└────────────────────────────────────────┘
```

No [Admins]/[Members] split for 1:1 DMs — flat two-member list.

---

## Thread Panel

Opened by pressing `t` on a message or selecting "Thread" from context menu:

```
┌─ Thread ─────────────────────────────┐
│                                       │
│  bob  10:33 AM                        │  ← root message
│  Nice. Any issues with the migration? │
│  ─────────────────────────────────── │
│  alice  10:34 AM                      │  ← reply 1
│  None so far. Monitoring grafana now  │
│                                       │
│  carol  10:36 AM                      │  ← reply 2
│  Looks clean from my end              │
│                                       │
│  Esc=close                            │
└───────────────────────────────────────┘
```

Shows the root message and all its replies in order. Press `g` on a reply in the main stream to jump to its parent.

---

## Quick Switch (Ctrl+K)

Fuzzy search across all rooms and conversations:

```
┌─ Switch to... ───────────────────────┐
│  > gen█                               │
│                                       │
│    # general                          │  ← room match
│    ● Gene                             │  ← DM match
│                                       │
└───────────────────────────────────────┘
```

Type to filter, arrow keys to navigate, Enter to switch.

---

## Confirmation Dialogs

### Quit Confirm (Ctrl+Q)

```
┌─────────────────────────────────────┐
│  Disconnect from server?             │
│  [y] Disconnect  [n] Cancel          │
└─────────────────────────────────────┘
```

### Leave Group Confirm

```
┌─────────────────────────────────────┐
│  Leave group?                        │
│                                      │
│  Leave Project Alpha?                │
│  You will stop receiving new         │
│  messages and cannot post.           │
│                                      │
│  [y] Leave  [n] Cancel              │
└─────────────────────────────────────┘
```

### Leave Room Confirm

Same shape as Leave Group, with room-specific wording.

### Delete DM Confirm

```
┌─────────────────────────────────────┐
│  Delete conversation with Bob?       │
│                                      │
│  This will remove the conversation   │
│  from every device on your account   │
│  and start a new conversation with   │
│  no history if Bob messages you       │
│  again.                              │
│                                      │
│  [y] Delete  [n] Cancel             │
└─────────────────────────────────────┘
```

### Delete Group Confirm

Same shape as Delete DM, with group-specific wording.

### Delete Room Confirm (active room)

```
┌─────────────────────────────────────┐
│  Delete room?                        │
│                                      │
│  This will remove you from the room  │
│  and delete all local messages.      │
│  An admin will need to add you back. │
│                                      │
│  [y] Delete  [n] Cancel             │
└─────────────────────────────────────┘
```

### Delete Room Confirm (retired room)

```
┌─────────────────────────────────────┐
│  Delete archived room?               │
│                                      │
│  This room is archived and           │
│  read-only. Deleting it cannot       │
│  be undone.                          │
│                                      │
│  [y] Delete  [n] Cancel             │
└─────────────────────────────────────┘
```

### Retire Account Confirm

```
┌─────────────────────────────────────┐
│  Retire your account?                │
│                                      │
│  This is PERMANENT and IRREVERSIBLE. │
│  Your SSH key will no longer work.   │
│  You will need a new account with    │
│  a new key to use this server again. │
│                                      │
│  Type RETIRE MY ACCOUNT to confirm:  │
│  > █                                 │
│                                      │
│  [Esc] Cancel                        │
└─────────────────────────────────────┘
```

### Group admin confirmation dialogs (Phase 14)

Five dialogs for the admin verbs — `AddConfirmModel`, `KickConfirmModel`, `PromoteConfirmModel`, `DemoteConfirmModel`, `TransferConfirmModel`. All five share the `y` / `Enter` / `n` / `Esc` convention and open over the current context without changing focus.

**Add member:**

```
┌─ Add member? ───────────────────────┐
│                                      │
│  Add Bob to Project Alpha?           │
│                                      │
│  Bob will see new messages from this │
│  point forward.                      │
│  They cannot decrypt messages sent   │
│  before they were added.             │
│                                      │
│  [y] Add  [n] Cancel                 │
└─────────────────────────────────────┘
```

**Remove member (kick):**

```
┌─ Remove member? ────────────────────┐
│                                      │
│  Remove Bob from Project Alpha?      │
│  After: 4 members will remain.       │
│                                      │
│  Bob will receive a notification     │
│  that they were removed.             │
│  They will lose access to new        │
│  messages in this group.             │
│  Remaining members see a system      │
│  message.                            │
│                                      │
│  [y] Remove  [n] Cancel              │
└─────────────────────────────────────┘
```

Post-kick member count comes from the local in-memory cache at dialog open time. The server may reject the kick if the target is no longer a member — no wire round-trip needed for the count display.

**Promote to admin:**

```
┌─ Promote to admin? ─────────────────┐
│                                      │
│  Promote Bob to admin?               │
│                                      │
│  Bob will be able to add, remove,    │
│  promote, and demote any member      │
│  (including you). All admins are     │
│  peers — there is no protected tier. │
│                                      │
│  [y] Promote  [n] Cancel             │
└─────────────────────────────────────┘
```

**Demote admin:**

```
┌─ Demote admin? ─────────────────────┐
│                                      │
│  Demote Bob from admin?              │
│  After: Project Alpha will have 1    │
│  admin (you).                        │
│  If you retire your account, the     │
│  oldest remaining member will be     │
│  auto-promoted as successor.         │
│                                      │
│  Bob will lose the ability to add,   │
│  remove, promote, or demote members. │
│  They remain a regular member of     │
│  the group.                          │
│                                      │
│  The server will reject this if it   │
│  would leave the group with zero     │
│  admins.                             │
│                                      │
│  [y] Demote  [n] Cancel              │
└─────────────────────────────────────┘
```

The "After: N admins remaining" line is computed from the local admin count before the demote. When the resulting count would be 1 (and the target is not the caller), the dialog also explains the retirement succession path.

**Transfer admin (atomic promote-then-leave):**

```
┌─ Transfer and leave? ───────────────┐
│                                      │
│  Promote Bob to admin and then       │
│  leave Project Alpha?                │
│                                      │
│  This is a two-step atomic handoff:  │
│  Bob becomes an admin, then you      │
│  leave the group.                    │
│                                      │
│  [y] Transfer  [n] Cancel            │
└─────────────────────────────────────┘
```

If Bob is already an admin, the dialog text flips to "Bob is already an admin. Leave the group?" and the action becomes just a leave.

### Last-admin promote picker (Phase 14)

When a sole admin runs `/leave` or `/delete` on a group that has other members, this picker opens instead of the standard leave/delete confirmation. The user picks a successor who is promoted before the leave completes.

```
┌─ Choose a new admin ────────────────┐
│                                      │
│  You are the only admin of           │
│  Project Alpha. Choose a member to   │
│  promote before you leave.           │
│                                      │
│  ▶ bob                               │
│    carol                             │
│    dave                              │
│                                      │
│  Enter=promote and leave  Esc=cancel │
└─────────────────────────────────────┘
```

List contains all non-admin members. Arrow keys navigate; Enter promotes the selected member then continues with the original leave/delete flow; Esc cancels (user stays in the group). The sole-member carve-out applies: if the group has only one member (the caller), the picker is skipped and `/leave` or `/delete` runs directly.

### `/audit` overlay (Phase 14)

```
┌─ Audit — Project Alpha ─────────────┐
│                                      │
│  alice added bob       2h ago        │
│  alice promoted bob    2h ago        │
│  alice renamed the group             │
│    "Project Alpha"     1h ago        │
│  bob demoted carol     45m ago       │
│  alice removed dave    10m ago       │
│                                      │
│  5 events · Esc=close                │
└─────────────────────────────────────┘
```

Read-only overlay populated from the local `group_events` table. Default limit is 10; `/audit 50` bumps it. The events are the same rows replayed via `sync_batch.Events` on reconnect, so an offline admin catching up sees the full history on next connect.

---

## Pending Keys Panel (admin only)

Opened by `/pending`:

```
┌─ Pending Key Requests ───────────────┐
│                                       │
│  SHA256:abc123...  3 attempts         │
│    First: 2026-04-03  Last: 2026-04-04│
│                                       │
│  SHA256:def456...  1 attempt          │
│    First: 2026-04-04  Last: 2026-04-04│
│                                       │
│  Approve via: sshkey-ctl approve      │
│  Esc=close                            │
└───────────────────────────────────────┘
```

Read-only. Approve/reject happens via `sshkey-ctl` on the server box.

---

## Device Manager

Opened from Settings → Manage devices:

```
┌─ Your Devices ───────────────────────┐
│                                       │
│  dev_laptop  (current)                │
│    Last synced: 2 minutes ago         │
│                                       │
│  dev_phone                            │
│    Last synced: 3 hours ago           │
│    [Revoke]                           │
│                                       │
│  dev_old  [revoked]                   │
│    Created: 2025-06-01                │
│                                       │
│  Esc=close                            │
└───────────────────────────────────────┘
```

---

## Connect-Failed Overlay

Shown on first-run when the server rejects the connection. Two
variants based on the server's error string:

**Pending approval** (unknown / pending / blocked key — the common
case for new users):

```
┌─ Pending Approval ───────────────────────────────────┐
│                                                       │
│  Your key isn't authorized on this server yet.        │
│  Your fingerprint has been added to the server's      │
│  pending-keys queue. Send your public key (below)     │
│  to the server operator and ask them to approve it.   │
│                                                       │
│  Once approved, press [r] to retry.                   │
│                                                       │
│  Fingerprint:                                         │
│  SHA256:abc123...                                     │
│                                                       │
│  Public key:                                          │
│  ssh-ed25519 AAAA...                                  │
│                                                       │
│  [r] Retry connection                                 │
│  [c] Copy public key to clipboard                     │
│  [q] Quit                                             │
└───────────────────────────────────────────────────────┘
```

**Retired account** (server returned "account retired"):

```
┌─ Account Retired ────────────────────────────────────┐
│                                                       │
│  This account has been retired by the server         │
│  operator. Logins are no longer accepted.             │
│                                                       │
│  If you believe this is in error, contact the         │
│  server operator out of band.                         │
│                                                       │
│  Fingerprint:                                         │
│  SHA256:abc123...                                     │
│                                                       │
│  Public key:                                          │
│  ssh-ed25519 AAAA...                                  │
│                                                       │
│  [r] Retry connection                                 │
│  [c] Copy public key to clipboard                     │
│  [q] Quit                                             │
└───────────────────────────────────────────────────────┘
```

---

## Key Change Warning

Shown when a peer's public key changes (potential MITM):

```
┌─ ⚠ Key Changed ─────────────────────┐
│                                       │
│  bob's SSH key has changed.           │
│                                       │
│  This could mean:                     │
│  • bob generated a new key            │
│  • someone is impersonating bob       │
│                                       │
│  Previous: SHA256:old123...           │
│  Current:  SHA256:new456...           │
│                                       │
│  [Accept new key]  [Disconnect]       │
└───────────────────────────────────────┘
```

---

## Device Revoked Alert

Shown when the server revokes the current device:

```
┌─ Device Revoked ─────────────────────┐
│                                       │
│  This device has been revoked.        │
│                                       │
│  Device: dev_laptop                   │
│  Reason: admin_action                 │
│                                       │
│  You will be disconnected.            │
│  [OK]                                 │
└───────────────────────────────────────┘
```

---

## @Mentions

When someone mentions you, the message gets a left border and your name is highlighted:

```
│  bob  10:33 AM                                    │
│  Has anyone looked at the logs?                    │
│                                                    │
│▐ bob  10:35 AM                                    │
│▐ Hey @alice can you take a look at the deploy?    │
│       ^^^^^^ accent color                          │
```

- Left border in accent violet on messages that mention you
- Your `@name` rendered in accent color within the body
- Other people's @mentions rendered in bold (no border)
- Notification generated even if room is muted (configurable)

In the sidebar, mentions get a stronger indicator than unread counts:

```
├─ Rooms ──────┤
│  # general @  │   ← @ = you were mentioned
│  # engineer(3)│   ← just unread, no mention
```

`@` badge clears when you view the message.

---

## Mouse Interactions

| Action | Result |
|---|---|
| Click room/DM in sidebar | Switch to it |
| Click message | Select it (shows context menu) |
| Click unread badge | Jump to first unread |
| Click member name (member panel) | Open member context menu (message, create group, verify, profile) |
| Click pinned bar | Expand/collapse pins |
| Click pinned message | Jump to it in history |
| Click link in message | Open in system browser |
| Scroll wheel in messages | Scroll history (lazy scroll-back at top) |

---

## First-Run Wizard

9-step guided setup on first launch. All steps render inside a rounded purple border (`dialogStyle`). Navigation: `Enter` to advance, `Esc` to go back, `q` to quit. Mouse-clickable on all steps.

### Step 0 — Welcome

```
┌────────────────────────────────────┐
│                                    │
│          sshkey-chat               │
│                                    │
│  Welcome to sshkey-chat            │
│  Private messaging over SSH with   │
│  end-to-end encryption.            │
│                                    │
│  Let's get you set up.             │
│                                    │
│          [Continue]                │
│                                    │
└────────────────────────────────────┘
```

### Step 1 — Choose Display Name

```
┌────────────────────────────────────┐
│                                    │
│       Choose Your Name             │
│                                    │
│  This will be your display name    │
│  on the server. Your admin can     │
│  change it if needed.              │
│                                    │
│  Display name:                     │
│  │alice█                           │
│                                    │
│  Enter=continue  Esc=back  q=quit  │
└────────────────────────────────────┘
```

Min 2, max 32 characters. Error shown in orange if invalid.

### Step 2 — Select SSH Key

```
┌────────────────────────────────────┐
│                                    │
│            SSH Key                 │
│                                    │
│  Select your SSH key:              │
│                                    │
│  ▶ ~/.ssh/id_ed25519              │  ← selected (highlighted)
│    ~/.ssh/work_ed25519             │
│    ~/.ssh/id_rsa (rsa)             │  ← grey, not Ed25519
│  ─────────────────────────────────│
│    Import from file                │
│    Generate new key                │
│                                    │
│  Only Ed25519 keys supported       │
└────────────────────────────────────┘
```

Arrow keys / `j`/`k` to navigate, `Enter` to select. Non-Ed25519 keys rejected with error. "Import" and "Generate" options below the separator.

### Step 3 — Import Key (if chosen)

```
┌────────────────────────────────────┐
│                                    │
│          Import Key                │
│                                    │
│  Path to SSH private key:          │
│  │~/path/to/private_key█          │
│                                    │
│  Enter=import  Esc=back            │
└────────────────────────────────────┘
```

Validates file exists and is Ed25519. Tilde-expanded.

### Step 4 — Generate Key (if chosen)

```
┌────────────────────────────────────┐
│                                    │
│         Generate Key               │
│                                    │
│  Save to:                          │
│  │~/.sshkey-term/keys/id_ed25519█ │
│                                    │
│  Passphrase (recommended):         │
│  │●●●●●●●●●●●●●●                  │
│  ✓ strong                          │  ← live strength hint
│                                    │
│  Confirm passphrase:               │
│  │●●●●●●●●●●●●●●                  │
│                                    │
│  ⚠ A passphrase protects your key │
│    if your device is stolen.       │
│                                    │
│  Tab=next field  Enter=generate    │
│  Esc=back                          │
└────────────────────────────────────┘
```

`Tab` cycles between the three fields. Passphrases must match (both empty = no passphrase, allowed but warned).

**Live strength hint.** The line under the passphrase input updates on every keystroke once the passphrase reaches 12 characters (`keygen.MinPassphraseLength`). Below the floor the hint is hidden — typing the first 11 characters does not show a rolling "too short" annotation because that's noise. Once the floor clears, the hint reflects the tier the user would hit on submit:

| Hint | Color | Tier | Submit behavior |
|---|---|---|---|
| *(hidden — no line rendered)* | — | `HintHidden` | Length under 12 chars. |
| `✗ weak — cracked in seconds` | red | `HintBlock` | Submit rejected; user must change passphrase. Full pattern explanation appears in the error message below the form on submit. |
| `! borderline — cracked in hours` | amber | `HintWarn` | Submit shows a confirmation prompt: "Press Enter again to use it anyway, or edit to try a stronger one." Re-submit with the same passphrase proceeds. |
| `✓ strong` / `✓ very strong` | green | `HintPass` | Submit proceeds silently. |

Context-aware: the chosen display name (from the wizard's name step) is passed to zxcvbn so a passphrase containing the user's own name is penalized. The add-server dialog's generate-key path (reached via `Ctrl+g` in Settings → Add Server) reuses the same live-hint layout and additionally passes the server hostname as context, so passphrases containing `chat.example.com` or its substrings are also penalized.

**Text-only on purpose.** No colored strength bar — color comes via a single styled word (`✗` red / `!` amber / `✓` green) rather than a 5-segment bar. Keeps the aesthetic consistent with the rest of the minimal Bubble Tea UI and degrades cleanly on monochrome terminals (the icons + labels stay legible without color). The `sshkey-ctl bootstrap-admin` CLI uses a 5-segment unicode bar instead (`▰▰▰▱▱`) because it runs in arbitrary terminals where color support is unpredictable and the line-based input can't update live — see the server repo's `bootstrap-admin` docs.

### Step 5 — Back Up Your Key

**Mandatory decision point** — the user cannot skip this step without choosing.

```
┌────────────────────────────────────┐
│                                    │
│       Back Up Your Key             │
│                                    │
│  This key is your identity. If     │
│  you lose it, you lose access to   │
│  your account and all encrypted    │
│  message history. The server       │
│  cannot recover your account.      │
│                                    │
│  Your key:                         │
│  ~/.ssh/id_ed25519                 │
│                                    │
│  [e] Export copy to file           │
│  [a] I'll back it up myself        │
│      — I understand there          │
│      is no recovery                │
│                                    │
│  Esc=go back                       │
└────────────────────────────────────┘
```

`e` → Export step. `a` → Acknowledge and skip to Share step.

### Step 6 — Export Key (if chosen)

```
┌────────────────────────────────────┐
│                                    │
│          Export Key                 │
│                                    │
│  Save a backup copy to:            │
│  │~/Documents/sshkey-backup█      │
│                                    │
│  Enter=save  Esc=back              │
└────────────────────────────────────┘
```

Copies private key + `.pub` to the chosen directory.

### Step 7 — Share Public Key

```
┌────────────────────────────────────┐
│                                    │
│     Share With Your Admin          │
│                                    │
│  Your server admin needs your      │
│  public key to add you to the      │
│  server.                           │
│                                    │
│  Name: alice                       │
│  Fingerprint: SHA256:abc123...     │
│                                    │
│  Public key (includes your name):  │
│  ssh-ed25519 AAAAC3NzaC1lZDI1...  │
│                                    │
│  [c] Copy public key to clipboard  │
│                                    │
│  Send this to your admin via a     │
│  trusted channel.                  │
│                                    │
│  Enter=continue  Esc=back          │
│                                    │
│  ✓ Public key copied to clipboard  │  ← shown after pressing c
└────────────────────────────────────┘
```

`c` copies the full public key. Green confirmation appears.

### Step 8 — Connect to Server

```
┌────────────────────────────────────┐
│                                    │
│       Connect to Server            │
│                                    │
│  Server name:                      │
│  │Personal█                        │
│                                    │
│  Host:                             │
│  │chat.example.com                 │
│                                    │
│  Port:                             │
│  │2222                             │
│                                    │
│  Tab=next field  Enter=connect     │
│  Esc=back  q=quit                  │
└────────────────────────────────────┘
```

`Tab` cycles fields. Host required; server name defaults to host if empty; port defaults to 2222. On `Enter`, the wizard completes and the app connects.

---

## Settings Panel (Ctrl+, or /settings)

```
┌─ Settings ────────────────────────┐
│                                    │
│  Profile                           │
│    Display name: Alice Chen  [▶]  │
│    Status: On vacation  [▶]       │
│                                    │
│  Servers                           │
│    ● Personal (connected)  [▶]    │
│    ○ Work                  [▶]    │
│    [Add server]                    │
│                                    │
│  Account                           │
│    [Manage devices]                │
│    [Retire account]                │
│                                    │
│  [Clear history]                   │
│                                    │
│  Esc=close                         │
└────────────────────────────────────┘
```

Arrow keys navigate, `Enter` on items with `[▶]` opens edit mode. "Manage devices" opens the Device Manager. "Retire account" opens the typed confirmation dialog.

---

## Add Server Dialog (Settings → [Add server])

The add-server dialog has two modes: the **form** (name / host / port / SSH key path) and the **generate-key sub-view** reached via `Ctrl+g`. The same dialog is used when adding a second (or Nth) server post-wizard.

### Form mode

```
┌─ Add Server ───────────────────────┐
│                                    │
│  Name: │Work█                      │
│                                    │
│  Host: │work.example.com           │
│                                    │
│  Port: │2222                       │
│                                    │
│  SSH key path: │~/.ssh/id_ed25519 │
│                                    │
│  Existing Ed25519 keys (click to use):
│  ~/.ssh/id_ed25519                 │
│  ~/.ssh/work_ed25519               │
│                                    │
│  Tab=next field  Ctrl+g=generate   │
│  new key  Enter=add  Esc=cancel    │
└────────────────────────────────────┘
```

`Ctrl+g` switches to the generate sub-view. Clicking a scanned key in the list populates the `SSH key path` field directly.

### Generate-key sub-view (Ctrl+g from the form)

```
┌─ Generate New Key ─────────────────┐
│                                    │
│  Save to:                          │
│  │~/.sshkey-term/keys/id_ed25519█ │
│                                    │
│  Passphrase (recommended):         │
│  │●●●●●●●●●●●●●●                  │
│  ! borderline — cracked in hours   │  ← live strength hint (amber)
│                                    │
│  Confirm passphrase:               │
│  │●●●●●●●●●●●●●●                  │
│                                    │
│  ⚠ A passphrase protects your key │
│    if your device is stolen. Back  │
│    the key up after generating —   │
│    the server cannot recover it.   │
│                                    │
│  Tab=next field  Enter=generate    │
│  Esc=back                          │
└────────────────────────────────────┘
```

**Same live-hint treatment as the wizard's Generate Key step** (see *Step 4 — Generate Key* above for the full tier table and length-gate behavior). One context difference: in addition to the display name, the add-server dialog also passes the **hostname** from the form's `Host` field as zxcvbn context. So a passphrase containing `work.example.com` or its substrings gets penalized — users often reach for the server name when picking a passphrase, and the context catches that failure mode.

On a successful generate, the new key's fingerprint is shown in the form mode as a `✓ Key generated — back it up` notice and the `SSH key path` field is pre-populated. `Esc` from the generate sub-view returns to the form without generating.

---

## Search Overlay (Ctrl+F or /search)

```
┌─ Sidebar ────┐  ┌─ Search ──────────────────────────────────────────┐
│  ...          │  │  🔍 │migration█                                   │
│              │  │                                                    │
│              │  │  alice  10:34 AM  #general                        │
│              │  │  None so far. Monitoring grafana now               │
│              │  │  ↳ "migration"                                    │
│              │  │                                                    │
│              │  │  bob  10:33 AM  #general                          │
│              │  │  Has anyone looked at the migration logs?          │
│              │  │  ↳ "migration"                                    │
│              │  │                                                    │
│              │  │  3 results · FTS5 search                          │
└──────────────┘  └────────────────────────────────────────────────────┘
```

Results show sender, timestamp, room/group context, and a snippet with the match highlighted. Enter on a result jumps to that message. Shows "FTS5 search" or "LIKE search (slow)" indicator.

---

## Help Screen (? or /help)

```
┌─ sshkey-term ─── Help ───────────────────────────────────────────────┐
│                                                                       │
│  Keyboard Shortcuts                                                   │
│                                                                       │
│  Ctrl+K    quick switch          r    reply to message               │
│  Ctrl+N    new conversation      e    react with emoji               │
│  Ctrl+M    toggle members        p    pin/unpin (rooms)              │
│  Ctrl+P    toggle pinned         d    delete message                 │
│  Ctrl+I    info panel            c    copy message text              │
│  Ctrl+F    search                g    jump to parent                 │
│  Ctrl+,    settings              t    thread view                    │
│  Alt+↑/↓   switch room           o    open attachment                │
│  Tab       cycle focus           s    save attachment                │
│  Esc       close / back          u    unreact                        │
│  ?         this help screen                                          │
│                                                                       │
│  Slash Commands: /leave /delete /rename /upload /verify /search      │
│  /settings /help /pending /mykey /mute /unverify                     │
│                                                                       │
│  Group Admin (when you are admin of the current group):              │
│  /add /kick /promote /demote /transfer /audit /undo                  │
│  /members /admins /role /whoami /info                                │
│                                                                       │
│  Press Esc to close                                                   │
└───────────────────────────────────────────────────────────────────────┘
```

**Context-aware filtering (Phase 14).** The admin command block is only shown when the current context is a group DM AND the local user is an admin of that group. In rooms, 1:1 DMs, or groups where the user is a regular member, the admin block is hidden to reduce clutter. The toggle is driven by `help.SetContext()` on every context switch.

---

## Passphrase Prompt

Shown on startup if the selected SSH key is passphrase-protected:

```
┌────────────────────────────────────┐
│                                    │
│  SSH Key Passphrase                │
│                                    │
│  Enter passphrase for:             │
│  ~/.ssh/id_ed25519                 │
│                                    │
│  │●●●●●●●●█                       │
│                                    │
│  Enter=unlock  Esc=quit            │
└────────────────────────────────────┘
```

---

## Verify Dialog (/verify @user)

```
┌─ Verify bob ─────────────────────┐
│                                   │
│  Safety Number                    │
│                                   │
│  1234 5678 9012                   │
│  3456 7890 1234                   │
│                                   │
│  Compare this number with bob     │
│  in person or via a trusted       │
│  channel. If the numbers match,   │
│  mark as verified.                │
│                                   │
│  [v] Mark verified                │
│  [Esc] Close                      │
└───────────────────────────────────┘
```

---

## Color Scheme

Truecolor (24-bit) with automatic fallback to 256-color or ANSI 16 for older terminals. Bubble Tea's lipgloss handles detection.

Text and backgrounds use the terminal default so the app adapts to the user's scheme. The app's own identity comes from accent colors on interactive and navigational elements.

### Palette

```
Brand accent:     #7C3AED  (violet)
Success/verified: #22C55E  (green)
Warning:          #F59E0B  (amber)
Error/danger:     #EF4444  (red)
Muted/dim:        #64748B  (slate)

Text:             terminal default
Background:       terminal default
```

### Element Color Map

| Element | Color | Style |
|---|---|---|
| **Content** | | |
| Usernames | terminal default | bold |
| Message body | terminal default | normal |
| Timestamps | muted `#64748B` | normal |
| Reply references `↳ re:` | muted `#64748B` | italic |
| System messages | muted `#64748B` | italic |
| [retired] markers | muted `#64748B` | faint |
| (left) / (retired) sidebar markers | muted `#64748B` | faint |
| `★` sidebar admin marker (Phase 14) | muted `#64748B` | faint |
| `★` info-panel admin marker (Phase 14) | muted `#64748B` | faint |
| **Interactive** | | |
| Selected sidebar item | accent `#7C3AED` | bg highlight |
| Unread badge `(2)` | accent `#7C3AED` | bold |
| Reaction counts | accent `#7C3AED` | normal |
| Input cursor | accent `#7C3AED` | |
| Pinned indicator `📌` | accent `#7C3AED` | |
| **Status** | | |
| Online dot `●` | green `#22C55E` | |
| Offline dot `○` | muted `#64748B` | |
| Verified badge `✓` | green `#22C55E` | |
| **Warnings** | | |
| Unsigned indicator | amber `#F59E0B` | |
| Signature failed | red `#EF4444` | bold |
| Key changed warning | red `#EF4444` | bold |
| Replay warning | amber `#F59E0B` | |
| Status bar error `⚠` | red `#EF4444` | |
| **Structure** | | |
| Panel borders | muted `#64748B` | |
| Status bar text | muted `#64748B` | |
| Dividers | muted `#64748B` | |
| Archived/left entries | muted `#64748B` | faint |

### Principles

- Terminal default for content (text, backgrounds) -- adapts to dark and light schemes
- Violet accent for interactive/navigational elements -- the app's visual signature
- Semantic colors (green/amber/red) for trust and warnings only
- Bold and italic for emphasis, not color -- accessible on any terminal
- No theme system, no config options for colors -- the palette is the brand
