# Changelog

## [Unreleased]

### Security
- **Phase 21 item 3 — edit-signature canonical-form fix + receive-path verification.** Closes a defense-in-depth gap made newly exploitable by Phase 15's `edited` / `group_edited` / `dm_edited` broadcasts: the send-path canonical forms (`SignRoom` / `SignDM`) didn't bind the signature to `msg.ID`, so a compromised server could replay a sender's past `(payload, context, epoch)` triple against a different msgID to rewrite history invisibly (pre-Phase-15 the same attack only worked via `send` — producing a visibly-new row — but Phase 15 moved the attack to `edited` where it overwrites existing rows with only a `(edited)` marker as a visual cue).
  - **New crypto functions** in `internal/crypto/crypto.go`: `SignRoomEdit(priv, msgID, payload, room, epoch) []byte`, `VerifyRoomEdit`, `SignDMEdit(priv, msgID, payload, conversation, wrappedKeys) []byte`, `VerifyDMEdit`. Canonical forms:
    - `Sign("edit_room:" || uint32_be(len(msgID)) || msgID || payload_bytes || room_utf8 || epoch_big_endian_uint64)`
    - `Sign("edit_dm:"   || uint32_be(len(msgID)) || msgID || payload_bytes || conversation_utf8 || wrapped_keys_canonical)`

    Domain-separation prefix (`"edit_room:"` / `"edit_dm:"`) prevents a valid send signature from being replayed as a valid edit signature. Length-prefixed `msgID` field prevents boundary-confusion between msgID and payload bytes. Shared helpers `buildRoomEditCanonical` / `buildDMEditCanonical` so Sign and Verify cannot drift.
  - **Edit send paths** in `internal/client/edit.go` (`EditRoomMessage` / `EditGroupMessage` / `EditDMMessage`) now call `SignRoomEdit` / `SignDMEdit` with the target `msgID` so the signature is bound to one specific message row from the moment it leaves the authoring client.
  - **Edit receive paths** in `internal/client/persist.go` (`storeEditedRoomMessage` / `storeEditedGroupMessage` / `storeEditedDMMessage`) now verify the signature BEFORE calling `UpdateMessageEdited`. Verify-or-drop contract: if verification fails OR the sender's pubkey can't be resolved, the broadcast is silently dropped and the stored row stays untouched. This is the **first client-side signature verification in the codebase** — the send receive paths remain unverified pass-through (out of scope for this fix; send-path substitution produces visibly-new rows anyway, so the stealth asymmetry doesn't apply).
  - **New helper** `Client.pubKeyForUser(userID) ed25519.PublicKey` resolves an author's pubkey via: self-check → live `c.profiles` cache → pinned-keys store fallback. Returns nil if the user is not known, which callers treat as verification failure.
  - **Breaking protocol change.** Old edit signatures (signed with `SignRoom` / `SignDM`) no longer verify on the new client. Zero users per the no-live-users policy, so no compat shim is required — post-launch the same fix would need a protocol version bit and graceful degradation for old clients.
  - **Scope decision (Option a from the plan).** Send-path weakness is accepted as-is. Option (b) — re-sign every send after the server assigns a msgID — would close the send-path too but doubles send bandwidth and complicates the send path significantly. The send-path attack requires injecting a visibly-new message row (new ID, new timestamp), which is far less stealthy than an invisible edit rewrite.
  - **~23 new tests**: 10 crypto unit tests (happy path / msgID binding / send-sig rejection / tamper detection / length-prefix ambiguity guard — per context variant) plus 13 integration tests in `internal/client/edit_verify_test.go` (happy / reused-send-sig / msgID-substitution / unknown-sender / garbage-signature × 3 contexts). Full test suite passes under `-race`.

- **Client-side defense in depth for the pre-join history gate** — `storeGroupMessage` in `internal/client/persist.go` now drops rows on `DecryptGroupMessage` failure instead of inserting an empty-body ghost. Primary defence is the server-side `joined_at` gate in `syncGroup`/`handleHistory`; this client drop catches any future path where the server regresses and sends a pre-join row. A pre-join group message has no wrapped key for the new member, so decrypt fails and the row is dropped silently. Mirrors the existing `storeReaction` "can't decrypt — don't persist garbage" pattern. See the server's `groups_admin.md` "Pre-join history gate" section for the full two-layer design.

### Added
- **Phase 17c: Error handling audit + typed error taxonomy + client send queue (client side).** Replaces the fire-and-forget send path with an in-memory send queue + retry driver, wires correlation IDs end-to-end, and mirrors the server's error-category taxonomy for client-side routing. Pairs with the server's Phase 17c entry — together they ship v0.2.0.
  - **In-memory send queue.** New `internal/client/sendqueue.go` — per-entry state machine `pending → sending → (acked | error | timeout)`. `EnqueueWithID`, `MarkSending`, `Ack`, `Error`, `SweepTimeouts`, `PendingForCategoryARetry`, `PendingForCategoryBRoomRetry`. In-memory only by construction — queue does not survive clean app close or crash. Documented trade-off matches Slack/Discord/Teams pattern; terminal apps don't get OS-killed for memory pressure, so "app close" is always a deliberate user action and typed-but-unsent becomes visible on reopen by absence-in-chat.
  - **Send-queue driver goroutine.** New `internal/client/sendqueue_driver.go` — 200ms tick. `backoffForAttempts` computes exponential backoff (200ms → 5s cap). Category A: up to 5 retry attempts, then surface. Category B: `TriggerEpochRetry` applies pushed fresh epoch/group state and re-sends.
  - **Generic corr_id dispatch.** New `internal/client/sendqueue_dispatch.go` — `dispatchCorrID` routes any incoming frame with a `corr_id` field to the right queue transition (15 corr_id-carrying verbs covered in one place). `upload_error` / `download_error` route to `Queue.Error`; `type="error"` returns early so the TUI handler gets it (needs verb lookup BEFORE queue removal for A-silent detection).
  - **Correlation-ID wiring (both sides).** New `CorrID string \`json:"corr_id,omitempty"\`` field added to 15 client-to-server verb structs in `internal/protocol/messages.go` (`send` / `send_group` / `send_dm` / `edit` / `edit_group` / `edit_dm` / `react` / `unreact` / `delete` / `pin` / `unpin` / `room_members` / `device_list` / `history` / `download`), and to 11 success response types (`Edited` / `GroupEdited` / `DMEdited` / `Reaction` / `ReactionRemoved` / `Deleted` / `HistoryResult` / `RoomMembersList` / `DeviceList` / `UploadReady` / `UploadComplete` / `DownloadStart`) so the queue can correlate echoes to queue entries. `UploadError` / `DownloadError` also gained `CorrID` + `RetryAfterMs`. Format `corr_<21-char-nanoid>`; generator reuses existing `GenerateID("corr_")` — zero new dependency.
  - **Client-side error-category mirror** — new `internal/protocol/categories.go` mirrors the server's `CategoryForCode(code)` function so the client can route errors by category without hardcoding the code list. `TestCategoryForCode_ExhaustiveOverConstants` drift-guard test parallels the server side.
  - **TUI refresh indicator** — `StatusBarModel.SetRefreshing(minDur)` / `ClearRefreshing()` in `internal/tui/statusbar.go` with a `refreshingUntil time.Time` floor. View renders "refreshing…" when `time.Now().Before(refreshingUntil)` AND no error message is set (errors take precedence — the more important signal). 200ms minimum visibility prevents flicker from fast server responses.
  - **`r` refresh keypresses** (info panel → `room_members`, device manager → `device_list`) and **`Ctrl+Shift+R`** (full reconnect handshake — sidebar refresh). Silent-fail on A-silent verbs (`room_members`, `device_list`): on `rate_limited` response, drop silently without toast — cached data stays on screen and surfacing "rate limited" for legitimate refresh intent would alarm the user about a non-problem.
  - **Quit-confirmation prompt** — new `internal/tui/quitconfirm.go` `QuitConfirmModel.ShowWithPending(serverName, pendingSend int)`. Renders a warning banner ("⚠ N message(s) still sending — quit will lose them.") below the base Disconnect prompt when the queue has unacked entries. Singular/plural noun helper (`fmtInt`) handles counts cleanly. Force-bypass via double-press Ctrl+Q within a 500ms window (`doubleQuitWindow` in `app.go`).
  - **`DeviceRevokedModel` renders the server-authored `reason` verbatim** — supports Phase 17b auto-revoke events with format `"Automatic revocation: <description> (<count> events in <window>s)"` without any client-side string-munging. Client stays dumb; server holds the signal-to-description mapping.
  - **~50 new TUI tests** covering `StatusBarModel.SetRefreshing` floor semantics (minimum visibility respected, longer window extends, shorter window does not shorten, error takes precedence, ClearRefreshing respects floor), `QuitConfirmModel.ShowWithPending` rendering (no-warning for zero pending, singular/plural nouns, large-count rendering, Hide), `fmtInt` helper, dispatch routing (plain-error-not-routed, upload_error-routed, download_error-routed). Full sshkey-term test suite passes under `-race`.

- **Phase 17b: Auto-revoke TUI adjustments (client side).** Small client-side additions layered onto the existing device-revocation handling so Phase 17b's server-initiated auto-revoke events surface cleanly.
  - **`DeviceRevokedModel` renders the server's `reason` string verbatim.** When the server auto-revokes a device, the `device_revoked` event carries `reason = "Automatic revocation: <description> (<count> events in <window>s)"`. The TUI renders the string as-is — no client-side mapping, no new code paths. Recovery instruction copy ("contact your admin to restore this device") still reads correctly in the auto-revoke case because OS-SSH admin access + `sshkey-ctl restore-device` is the same recovery regardless of whether the revoke was manual or automatic.
  - **No client-side thresholds, no client-side state.** Phase 17b enforcement is entirely server-side; the client sees only the final `device_revoked` broadcast. The reason-string contract (server owns, client renders verbatim) keeps the client dumb and makes future server-side signal additions invisible to the client.

- **Phase 17: Server hardening support (client side).** Phase 17's server work was mostly server-internal (rate-limit coverage, envelope caps, broadcast back-pressure, input validation, broadcast fan-out), but two client-visible additions landed:
  - **`retry_after_ms` field added to `protocol.Error` and `protocol.UploadError`** in `internal/protocol/messages.go` to mirror the server. Client's queue driver reads this field for Category A retry scheduling when the server populates it (16 server-side rate-limit rejection sites).
  - **Client-side protocol mirrors** for the Phase 17 + 17b server types (see Phase 17c entry above for the full corr_id / CorrID / RetryAfterMs additions to existing structs).

- **Phase 20: Server-authoritative multi-device /leave catchup + room event audit trail (client side).**
  - **Reason-specific `(left)` replaced by server-authoritative state.** Sidebar and archived transcripts now reflect the actual cause of a "gone" context (self-leave on another device / admin removal / account retirement) instead of the generic `(left)` marker. Client receives `left_rooms` / `left_groups` messages on the connect handshake carrying the server's authoritative reason code and applies them via `MarkRoomLeft` / `MarkGroupLeft`.
  - **Client-side reconciliation walk deleted.** The old "diff local active IDs against server's room_list/group_list" inference path is gone. `GetActiveRoomIDs` / `GetActiveGroupIDs` and their tests (`active_ids_test.go`) are deleted — ~40 LOC of dead code. Server is now authoritative; client no longer infers from absence.
  - **New `leave_reason` column** on `rooms` and `groups` tables. `MarkRoomLeft` / `MarkGroupLeft` take a `reason` parameter. `MarkRoomRejoined` / `MarkGroupRejoined` clear both `left_at` and `leave_reason` so re-add fully resets local state.
  - **Room event audit trail (parity with Phase 14 groups).** New local `room_events` table + `RecordRoomEvent` / `GetRoomEvents` store helpers. Client persists inbound `room_event` broadcasts AND replays `sync_batch.Events` entries typed as `room_event` (server packs group_events and room_events into the same `Events` slice; client routes by `type` field). TUI renders inline system messages in the room transcript for 5 event types: leave (with reason-specific wording), join ("alice added bob"), topic change, rename, retirement.
  - **Protocol types mirrored:** new `LeftRoomsList` / `LeftGroupsList` / `LeftRoomEntry` / `LeftGroupEntry`. `RoomEvent` struct extended with `By` and `Name` fields.
  - **Client DB privacy posture unchanged:** local DB stays SQLCipher-encrypted at rest on the client, so audit events stored client-side are encrypted even though server-side they're plaintext metadata. See the encryption-boundary section in the server's PROJECT.md for the full split.

- **Phase 16 client-side changes:**
  - **zxcvbn passphrase strength checking** integrated into the first-launch wizard (`wizard.go`) and add-server dialog (`addserver.go`) keygen flows. Passphrases at zxcvbn score 0-1 are hard-blocked; score 2 shows a warning with press-Enter-to-confirm; score 3-4 pass silently. Uses `github.com/trustelem/zxcvbn` (~30KB bundled dictionaries). New `internal/keygen/strength.go` package mirrors the server-side admin helper with a softer user-tier floor.
  - **`user_unretired` protocol handler** — when the server fires `user_unretired` (admin ran `sshkey-ctl unretire-user`), the client deletes the user from `c.retired` so the `[retired]` marker is flushed from sidebar labels, info panels, and message headers on the next render.
  - **`room_updated` protocol handler** — when the server fires `room_updated` (admin ran `sshkey-ctl update-topic` or `rename-room`), the client calls `store.UpdateRoomNameTopic` to update the local rooms table. The sidebar and info panel pick up the change on the next render. New `UpdateRoomNameTopic` store helper (UPDATE-only, leaves member count untouched).
  - **`UserUnretired` and `RoomUpdated` protocol types** added to `internal/protocol/messages.go`, mirroring the server-side types.
  - **12 new tests** covering zxcvbn tiers, user_unretired handler, room_updated handler (including members-count preservation).
- **Phase 15: message editing.** Up-arrow on an empty input enters edit mode with the user's most recent editable message pre-populated; Enter dispatches the right edit verb for the active context (`edit` / `edit_group` / `edit_dm`); Esc cancels without dispatching. The input bar shows a `✎ editing message — Esc to cancel` indicator while edit mode is active. The messages pane renders a `(edited)` marker in dim style next to the timestamp on any message with `EditedAt > 0`. Context switches (sidebar navigation, quick switch, search jump) automatically exit edit mode so a half-finished edit never dispatches to the wrong conversation. New `Client.EditRoomMessage` / `EditGroupMessage` / `EditDMMessage` methods follow the **preserve-and-replace** pattern: fetch the original decrypted message from the local store, copy `ReplyTo` / `Attachments` / `Previews` verbatim, re-extract `Mentions` from the new body, replace `Body`, regenerate `Seq` and `DeviceID`, re-encrypt with a fresh K_msg (groups/DMs) or the current epoch key (rooms), sign, and send. Local store and in-memory `MessagesModel` are updated when the `edited` / `group_edited` / `dm_edited` broadcast arrives, not on send — matches the existing wait-for-echo pattern. New store helpers: `Store.UpdateMessageEdited`, `Store.GetMessageByID`, `Store.GetUserMostRecentMessageIDInContext`. New `DisplayMessage.EditedAt` field. New TUI tests covering edit mode state transitions and the `(edited)` marker rendering. On `edit_window_expired` or `edit_not_most_recent` errors from the server, the input bar shows a friendly status bar message and exits edit mode cleanly. See server `PROTOCOL.md` "Message Editing" section for the wire format.
- **Phase 18: room topics in the terminal app** (display-only). The client has always persisted `RoomInfo.Topic` from the server's `room_list` into the local `rooms` table (since Phase 7b), but nothing ever read it — topics were invisible to users. Phase 18 wires the read path:
  - **Messages pane two-line header** — `MessagesModel.View()` now renders a permanent header at the top of the messages pane inside the rounded border: bold room display name on line 1, dim italic topic on line 2 (rooms only, omitted when empty), then a blank separator before the message stream. Before Phase 18 the header existed as dead code (the `title` variable was computed but never rendered); Phase 18 gives the header real work to do. Groups render only line 1 (group name); 1:1 DMs render only line 1 (other party's resolved display name).
  - **Info panel topic line** — `InfoPanelModel.ShowRoom()` now populates the `topic` field via `Client.DisplayRoomTopic(roomID)`. The render code at `infopanel.go:385` has had `if i.topic != "" { ... }` since v0.1.0 but was never fed data until now.
  - **`Store.GetRoomTopic(roomID) string`** — new store helper, parallel to `GetRoomName`. Empty-string semantics (not raw-ID fallback) so the render layer can use `if topic != ""` to omit the topic line cleanly.
  - **`Client.DisplayRoomTopic(roomID) string`** — new client resolver wrapping the store helper. Added to the in-memory cache accessor set alongside `DisplayRoomName`.
  - **`MessagesModel.SetRoomTopic` / `RoomTopic`** — new setter + getter on the messages model. `SetContext` clears the stored topic on every context switch; the app layer's new `applyRoomTopic()` helper calls `SetRoomTopic` after each context switch to push the current context's topic into the model.
  - **`/topic` read-only slash command** — typing `/topic` in a room context shows the current topic in the status bar ("#general — General chat, please be nice" or "#general has no topic set"). Groups/DMs surface "/topic is only available in rooms". Pure local read via `Client.DisplayRoomTopic`; no server interaction. Added to `help.go` command list and `completion.go` autocomplete.
  - **16 regression tests** across three areas:
    - **Store helper** (4 tests): `TestGetRoomTopic_ReturnsTopic`, `TestGetRoomTopic_NoRow_ReturnsEmpty`, `TestGetRoomTopic_EmptyTopic_ReturnsEmpty`, `TestGetRoomTopic_UpdatedOnSecondUpsert`.
    - **Messages pane header** (7 tests in `topic_test.go`): `TestMessagesHeader_ShowsRoomNameAndTopic`, `TestMessagesHeader_OmitsTopicLineWhenEmpty`, `TestMessagesHeader_GroupContext_NoTopicLine`, `TestMessagesHeader_DMContext_NoTopicLine`, `TestMessagesHeader_EmptyContext_ShowsFallback`, `TestMessagesHeader_SetContextClearsTopic`, `TestRoomTopic_Accessor`.
    - **Info panel + `/topic` command** (5 tests in `phase18_test.go`): `TestInfoPanel_ShowsRoomTopic`, `TestInfoPanel_OmitsTopicLineWhenEmpty`, `TestSlashTopic_RoomContext_ShowsCurrentTopic`, `TestSlashTopic_RoomContext_NoTopicSet`, `TestSlashTopic_GroupContext_ShowsNotAvailable`.
  - **`client.SetStoreForTesting(c, s)` export** — new test-only helper in `internal/client/client.go` that attaches a `*store.Store` to a `*Client` from an external package, so tests in `internal/tui` can exercise `DisplayRoomTopic` + `handleTopicCommand` without spinning up a full SSH connection. Documented "Do not call from production code."
  - **Scope: display-only.** ~~Changing a topic after room creation and broadcasting live updates are deferred to the Admin CLI audit phase.~~ **Update (Phase 16 shipped):** `sshkey-ctl update-topic` and `sshkey-ctl rename-room` now exist, and the `room_updated` protocol event broadcasts live changes to connected room members. The client-side `room_updated` handler (added in Phase 16) calls `UpdateRoomNameTopic` on receipt. Phase 18's display-only scope is now fully complemented by Phase 16's write path.

### Changed
- **Group DMs gained an in-group admin model (Phase 14)** — matches the server-side reversal of the "immutable peer DMs" decision. Group creators become the first admin; any admin can add/remove/promote/demote members. New `/add`, `/kick`, `/promote`, `/demote`, `/transfer` slash commands with confirmation dialogs. See `groups_admin.md` in the server repo for the full design.
- **Info panel per-member admin flag** now reads from the in-memory group admin set (populated by `group_list` catchup + live `group_event{promote,demote}` broadcasts) instead of the global `profile.Admin` flag (which tracks server-wide admin status, unrelated to per-group governance).
- **`/rename` now admin-gated client-side** — non-admin attempts surface a friendly "you are not an admin" message without hitting the wire. Matches the server-side admin gate landed in Phase 14.
- **Group `group_renamed` + `group_event{rename}` dual broadcast** — the client now handles both shapes during the single-repo upgrade window. Sync replay uses `group_event{rename}` via the new `SyncBatch.Events` field.
- **System message rendering for group events** — all five event types (`join`, `leave`, `promote`, `demote`, `rename`) render as system messages in the message view with specific wording ("alice added bob to the group", "alice removed bob", etc.). Honors the Phase 14 `Quiet` flag.
- Room identity switched to nanoid IDs (`room_` prefix) — display names resolved at TUI layer
- All protocol `Room` fields now carry nanoid IDs instead of display names
- `room_list` handled at client layer (persists room metadata to local DB)
- Info panel hints: active rooms and groups show both `/leave` and `/delete`; left/retired rooms show `/delete` only; obsolete "(coming in a later phase)" placeholder removed
- Read-only banner wording distinguishes self-leave ("you left this room") from admin retirement ("this room was archived by an admin")

### Added
- **Phase 14 group admin slash commands**:
  - Admin verbs: `/add @user`, `/kick @user`, `/promote @user`, `/demote @user`, `/transfer @user` (atomic promote-then-leave handoff). Each with confirmation dialog. All admin verbs pre-check the local `is_admin` flag and surface a friendly rejection before hitting the wire.
  - Status commands: `/members`, `/admins`, `/role @user`, `/whoami`, `/groupinfo`, `/audit [N]` (recent admin actions, default 10), `/undo` (revert last kick within 30 seconds).
  - Creation commands: `/groupcreate ["name"] @a @b @c` (inline group DM creation, bypasses the wizard), `/dmcreate @user` (inline 1:1 DM creation).
- **`/audit` overlay** — one-shot read-only panel showing recent admin actions for the current group, read from the local `group_events` table. Populated from both live broadcasts and offline sync replay.
- **`/members` and `/admins` overlays** — one-shot read-only panels listing group members with ★ admin markers. `/admins` pre-filters to just admins.
- **Sidebar ★ admin indicator** — groups where the local user is an admin show a muted ★ glyph before the group name. Updates live on `group_event{promote,demote}` via the `resolveIsLocalAdmin` callback.
- **Info panel admin keyboard shortcuts** — A/K/P/X on a focused member row route to the admin verb dialogs (Add / Kick / Promote / demoteX). X is used for demote because D means delete elsewhere in the app. Active only in group contexts.
- **Event coalescing** — consecutive same-admin same-verb events within 10 seconds collapse into one system message ("alice added bob, carol, and dave"). Applies to join/promote/demote/removed; never coalesces self-leave, retirement, or rename. Individual events are still persisted un-coalesced to the local `group_events` table (visible in `/audit`).
- **Client `group_events` table** — single table with `group_id` column (client is single-DB-per-server). Populated from both live `group_event` broadcasts and the new `SyncBatch.Events` replay. Feeds the `/audit` overlay.
- **Client `groups.is_admin` column** — the local user's admin flag per group, persisted so the TUI pre-check survives restart. Not folded into the `StoreGroup` upsert so promote/demote events can't clobber the members list.
- **In-memory `groupAdmins` map on `Client`** — other members' admin state, sourced from `group_list` + live `group_event{promote,demote}` + `sync_batch.Events` replay. Accessed via `GroupAdmins(groupID)` and `IsGroupAdmin(groupID, userID)`.
- **New client store helpers**: `IsLocalUserGroupAdmin`, `SetLocalUserGroupAdmin`, `RecordGroupEvent`, `GetGroupEvents`, `GetRecentGroupEvents`, plus a `FindUserByName` accessor on `Client` for resolving `@user` arguments to user IDs.
- **Confirmation dialogs** — five new dialog models (`AddConfirmModel`, `KickConfirmModel`, `PromoteConfirmModel`, `DemoteConfirmModel`, `TransferConfirmModel`) following the existing `LeaveConfirmModel` shape. Transfer carries a `TargetAlreadyAdmin` flag so the text flips to "already admin, just leave?" when promote would be a no-op.
- **`group_added_to` handler** — when an admin adds the local user to an existing group, the client inserts the group into local state immediately and surfaces a toast-style status bar notification ("alice added you to 'Project X'").
- **`/undo` 30-second kick revert** — tracks the last kick the local user performed; `/undo` within the window re-adds via `add_to_group`. Exactly one kick tracked, no stack.
- **Protocol type mirrors** — nine new message types in `sshkey-term/internal/protocol/messages.go` (`AddToGroup`, `RemoveFromGroup`, `PromoteGroupAdmin`, `DemoteGroupAdmin`, four result echoes, and `GroupAddedTo`), plus extensions to `GroupEvent` (`By`/`Name`/`Quiet`), `GroupLeft` (`By`), `GroupCreated` (`Admins`), `GroupInfo` (`Admins`), `RenameGroup` (`Quiet`), `SyncBatch` (`Events`).
- **Phase 14 deferred-items pass (post-Chunk-7)**:
  - **Desktop notification for `group_added_to`** — toast-style notification when an admin adds the local user to an existing group, consistent with room-add notifications.
  - **Stale-cache heuristic on `ErrUnknownGroup`** — when a request to a cached group fails with "unknown group", the client schedules a fresh `group_list` request on the next tick to refresh the cache, avoiding stuck state after a silent remove.
  - **Rich dialog content** — `KickConfirmModel` now shows the current member count ("After: N members will remain"); `DemoteConfirmModel` shows the pre-demote admin count and warns when the resulting count is 1. `AddConfirmModel`, `PromoteConfirmModel`, `KickConfirmModel`, `DemoteConfirmModel` all use the target's display name in the first consequence line ("Bob will receive a notification") instead of the generic pronoun.
  - **Context-aware `/help`** — `help.SetContext()` toggles the visibility of the admin command block based on whether the local user is an admin of the current group. Admins in groups see `/add`, `/kick`, `/promote`, `/demote`, `/transfer`, `/audit`, `/undo`, `/members`, `/admins`, `/role`, `/whoami`, `/groupinfo` in the help screen; everyone else sees the clean default list.
  - **Group-member-scoped `@` autocomplete** — `CompleteWithContext()` detects the leading verb and routes the completion source: `/kick`, `/promote`, `/demote`, `/transfer`, `/role` complete against **current members**; `/add` completes against **non-members**. Plain `@` outside a verb uses the default member list.
  - **Last-admin inline promote picker** — `LastAdminPickerModel` intercepts `/leave` and `/delete` when the local user is the sole admin of a group that has other members. Lists candidates, promotes the selection, then continues with the original leave/delete flow. The sole-member carve-out is respected — a solo admin with no other members leaves or deletes directly without picker interception.
- **`TestHandleRemoveFromGroup_LastMemberCleanupOnKickedSoleMember`** — regression test ensuring that kicking the last remaining member runs the same last-member cleanup as a self-leave (deletes the group conversation row and unlinks the per-group DB file).
- `rooms` table in client DB for room metadata persistence (id, name, topic, members)
- `DisplayRoomName()` resolver — reads from local DB, falls back to raw ID
- `resolveRoomName` callbacks in sidebar, messages header, quickswitch, infopanel, notifications
- **Room retirement + `/delete` for rooms (Phase 12)** — clients receive `room_retired` / `retired_rooms` and `room_deleted` / `deleted_rooms` broadcasts and catchup lists; UI flips affected rooms to read-only or removes them entirely
- `DeleteRoomConfirmModel` — confirmation dialog with distinct wording for active vs retired rooms
- Sidebar: retired rooms render with `(retired)` marker (takes priority over `(left)`); unread counts suppressed; `RemoveRoom` helper parallel to `RemoveGroup`
- Messages view: `SetRoomRetired` state + banner for the read-only admin-archived case
- `rooms.retired_at` column (no migration — empty client DBs); `MarkRoomRetired`, `IsRoomRetired`, `PurgeRoomMessages` store helpers
- `DeleteRoom` client method; `case "room_retired" / "retired_rooms" / "room_deleted" / "deleted_rooms"` in client dispatch loop

## v0.1.1 — 2026-04-07

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
