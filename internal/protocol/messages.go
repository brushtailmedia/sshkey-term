// Package protocol defines the sshkey-chat wire format message types (client-side).
package protocol

import "encoding/json"

// Handshake

type ServerHello struct {
	Type         string   `json:"type"`
	Protocol     string   `json:"protocol"`
	Version      int      `json:"version"`
	ServerID     string   `json:"server_id"`
	Capabilities []string `json:"capabilities"`
}

type ClientHello struct {
	Type          string   `json:"type"`
	Protocol      string   `json:"protocol"`
	Version       int      `json:"version"`
	Client        string   `json:"client"`
	ClientVersion string   `json:"client_version"`
	DeviceID      string   `json:"device_id"`
	LastSyncedAt  string   `json:"last_synced_at"`
	Capabilities  []string `json:"capabilities"`
}

type Welcome struct {
	Type               string   `json:"type"`
	User               string   `json:"user"`
	DisplayName        string   `json:"display_name"`
	Admin              bool     `json:"admin"`
	Rooms              []string `json:"rooms"`
	Groups             []string `json:"groups"`
	PendingSync        bool     `json:"pending_sync"`
	ActiveCapabilities []string `json:"active_capabilities"`
}

// Sync

type SyncBatch struct {
	Type      string         `json:"type"`
	Messages  []RawMessage   `json:"messages"`
	Reactions []RawMessage   `json:"reactions,omitempty"`
	Events    []RawMessage   `json:"events,omitempty"` // Phase 14: group admin events (join/leave/promote/demote/rename) for offline replay
	EpochKeys []SyncEpochKey `json:"epoch_keys"`
	Page      int            `json:"page"`
	HasMore   bool           `json:"has_more"`
}

type SyncEpochKey struct {
	Room       string `json:"room"`
	Epoch      int64  `json:"epoch"`
	WrappedKey string `json:"wrapped_key"`
}

type SyncComplete struct {
	Type     string `json:"type"`
	SyncedTo string `json:"synced_to"`
}

// Room messages

type Send struct {
	Type      string   `json:"type"`
	Room      string   `json:"room"`
	Epoch     int64    `json:"epoch"`
	Payload   string   `json:"payload"`
	FileIDs   []string `json:"file_ids,omitempty"`
	Signature string   `json:"signature"`
	CorrID    string   `json:"corr_id,omitempty"` // Phase 17c: client correlation tag
}

type Message struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	From      string   `json:"from"`
	Room      string   `json:"room"`
	TS        int64    `json:"ts"`
	Epoch     int64    `json:"epoch"`
	Payload   string   `json:"payload"`
	FileIDs   []string `json:"file_ids,omitempty"`
	Signature string   `json:"signature"`
	EditedAt  int64    `json:"edited_at,omitempty"` // Phase 15
	CorrID    string   `json:"corr_id,omitempty"`   // Phase 17c: originator-only ack echo
}

// Edit — room message edit envelope (Phase 15 client → server).
type Edit struct {
	Type      string `json:"type"` // "edit"
	ID        string `json:"id"`
	Room      string `json:"room"`
	Epoch     int64  `json:"epoch"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	CorrID    string `json:"corr_id,omitempty"` // Phase 17c
}

// Edited — room edit broadcast (Phase 15 server → client).
type Edited struct {
	Type      string   `json:"type"` // "edited"
	ID        string   `json:"id"`
	From      string   `json:"from"`
	Room      string   `json:"room"`
	TS        int64    `json:"ts"`
	Epoch     int64    `json:"epoch"`
	Payload   string   `json:"payload"`
	FileIDs   []string `json:"file_ids,omitempty"`
	Signature string   `json:"signature"`
	EditedAt  int64    `json:"edited_at"`
	CorrID    string   `json:"corr_id,omitempty"` // Phase 17c
}

// Group DMs
//
// (Pre-Phase-11 comment about DMs not being supported has been deleted —
// 1:1 DMs landed in Phase 11 with their own type family. See `DM` below.)

type CreateGroup struct {
	Type    string   `json:"type"`
	Members []string `json:"members"`
	Name    string   `json:"name,omitempty"`
}

type GroupCreated struct {
	Type    string   `json:"type"`
	Group   string   `json:"group"`
	Members []string `json:"members"`
	Admins  []string `json:"admins,omitempty"` // Phase 14: admin user IDs (includes creator on fresh groups)
	Name    string   `json:"name,omitempty"`
}

type SendGroup struct {
	Type        string            `json:"type"`
	Group       string            `json:"group"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type GroupMessage struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	From        string            `json:"from"`
	Group       string            `json:"group"`
	TS          int64             `json:"ts"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	EditedAt    int64             `json:"edited_at,omitempty"` // Phase 15
	CorrID      string            `json:"corr_id,omitempty"`   // Phase 17c: originator-only ack echo
}

// EditGroup — group DM edit envelope (Phase 15 client → server).
type EditGroup struct {
	Type        string            `json:"type"` // "edit_group"
	ID          string            `json:"id"`
	Group       string            `json:"group"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

// GroupEdited — group DM edit broadcast (Phase 15 server → client).
type GroupEdited struct {
	Type        string            `json:"type"` // "group_edited"
	ID          string            `json:"id"`
	From        string            `json:"from"`
	Group       string            `json:"group"`
	TS          int64             `json:"ts"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	EditedAt    int64             `json:"edited_at"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type LeaveGroup struct {
	Type  string `json:"type"`
	Group string `json:"group"`
}

// DeleteGroup is the client request to remove a group DM from every
// device on the user's account. Distinct from leave_group: leave keeps
// local history (read-only); delete purges local history on every
// device. See sshkey-chat/internal/protocol/messages.go for the full
// design notes.
type DeleteGroup struct {
	Type  string `json:"type"`
	Group string `json:"group"`
}

// GroupDeleted is the canonical echo for delete_group. The server sends
// it to every connected session of the deleting user. Receiving this
// always triggers a local purge regardless of which device initiated
// the delete.
type GroupDeleted struct {
	Type  string `json:"type"`
	Group string `json:"group"`
}

// DeletedGroupsList is the offline-catchup payload sent during the
// connect handshake (BEFORE group_list) listing every group ID this
// user has previously /delete'd. Clients run the same purge path as
// group_deleted for each entry.
type DeletedGroupsList struct {
	Type   string   `json:"type"`
	Groups []string `json:"groups"`
}

// Phase 14 — in-group admin verbs
//
// The server enforces byte-identical privacy for the "unknown group /
// non-member / non-admin" triple: all three rejection shapes return an
// identical ErrUnknownGroup frame. Clients MUST NOT try to derive admin
// state from wire-level errors — always read it from the local is_admin
// flag or the GroupInfo.Admins payload. Only AFTER the caller has proven
// membership AND admin status does the server start returning distinct
// errors (already_member, already_admin, forbidden, unknown_user).
//
// The Quiet flag on AddToGroup / PromoteGroupAdmin / DemoteGroupAdmin /
// RenameGroup suppresses the inline system message on receiving clients
// while still persisting the event and updating member/admin lists.
// RemoveFromGroup deliberately does NOT have a Quiet flag — kicks are
// always loud.

// AddToGroup is the client's request for an admin to add a new member
// to an existing group. Multi-target adds are done client-side by
// sending one AddToGroup per target.
type AddToGroup struct {
	Type  string `json:"type"`            // "add_to_group"
	Group string `json:"group"`
	User  string `json:"user"`            // target to add
	Quiet bool   `json:"quiet,omitempty"` // suppress inline system message
}

// RemoveFromGroup is the client's request for an admin to remove a
// member from a group. Passing the caller's own user ID falls through
// to the self-leave path (handleLeaveGroup).
type RemoveFromGroup struct {
	Type  string `json:"type"`  // "remove_from_group"
	Group string `json:"group"`
	User  string `json:"user"`  // target to remove
}

// PromoteGroupAdmin is the client's request to promote a member to
// admin. Unilateral — any admin can promote any non-admin member.
type PromoteGroupAdmin struct {
	Type  string `json:"type"`            // "promote_group_admin"
	Group string `json:"group"`
	User  string `json:"user"`            // target to promote
	Quiet bool   `json:"quiet,omitempty"` // suppress inline system message
}

// DemoteGroupAdmin is the client's request to demote an admin (possibly
// the caller themselves) back to regular member. Rejected by the server
// if the demotion would leave the group with zero admins.
type DemoteGroupAdmin struct {
	Type  string `json:"type"`            // "demote_group_admin"
	Group string `json:"group"`
	User  string `json:"user"`            // target to demote (may equal caller for self-demote)
	Quiet bool   `json:"quiet,omitempty"` // suppress inline system message
}

// AddGroupResult echoes a successful add_to_group back to the calling admin.
type AddGroupResult struct {
	Type  string `json:"type"`  // "add_group_result"
	Group string `json:"group"`
	User  string `json:"user"`  // added user
}

// RemoveGroupResult echoes a successful remove_from_group back to the calling admin.
type RemoveGroupResult struct {
	Type  string `json:"type"`  // "remove_group_result"
	Group string `json:"group"`
	User  string `json:"user"`  // removed user
}

// PromoteAdminResult echoes a successful promote_group_admin back to the calling admin.
type PromoteAdminResult struct {
	Type  string `json:"type"`  // "promote_admin_result"
	Group string `json:"group"`
	User  string `json:"user"`  // promoted user
}

// DemoteAdminResult echoes a successful demote_group_admin back to the calling admin.
type DemoteAdminResult struct {
	Type  string `json:"type"`  // "demote_admin_result"
	Group string `json:"group"`
	User  string `json:"user"`  // demoted user
}

// GroupAddedTo is a direct notification sent to a user's sessions when
// an admin adds them to a group. Carries the full group metadata (name,
// members, admins, added_by) so the client can insert the group into
// local state without waiting for a fresh group_list catchup.
//
// The added user receives no pre-join history — their first decryptable
// message is the next group_message broadcast after the add lands.
type GroupAddedTo struct {
	Type    string   `json:"type"`    // "group_added_to"
	Group   string   `json:"group"`
	Name    string   `json:"name,omitempty"`
	Members []string `json:"members"`
	Admins  []string `json:"admins"`
	AddedBy string   `json:"added_by"` // user ID of the admin who added the recipient
}

type RenameGroup struct {
	Type  string `json:"type"`
	Group string `json:"group"`
	Name  string `json:"name"`
	Quiet bool   `json:"quiet,omitempty"` // Phase 14: suppress inline system message
}

type GroupRenamed struct {
	Type      string `json:"type"`
	Group     string `json:"group"`
	Name      string `json:"name"`
	RenamedBy string `json:"renamed_by"`
}

// GroupEvent is the generic broadcast envelope for every admin-initiated
// group mutation and self-leave. Phase 14 extended it significantly:
//
//   - Event values: "leave" (unchanged), "join", "promote", "demote", "rename"
//     (added). A group_event{leave} carries optional Reason; the other event
//     types use dedicated fields (Name for rename, By for the acting admin).
//   - By: the user ID of the admin that triggered this event. Required
//     (non-empty) for admin-initiated events (join, promote, demote, rename,
//     and leave with reason="removed"). Empty for self-leave, retirement,
//     and retirement-succession promote.
//   - Quiet: when true, clients MUST still update member/admin lists and
//     persist the event to the local group_events table, but MUST suppress
//     the inline system message in the message view. Always false for kicks
//     (leave with reason="removed") — being removed is high-consequence and
//     clients should always surface it loudly.
//   - Name: new name value for rename events.
//
// Reason values for Event="leave":
//   - ""             self-leave via /leave
//   - "removed"      admin-initiated removal via handleRemoveFromGroup; By required
//   - "retirement"   caller account was retired; By empty
//
// Reason values for Event="promote":
//   - ""                        normal admin promote; By required
//   - "retirement_succession"   auto-promote of oldest member when the last
//                               admin retires their account; By empty
type GroupEvent struct {
	Type   string `json:"type"`
	Group  string `json:"group"`
	Event  string `json:"event"`            // "leave" | "join" | "promote" | "demote" | "rename"
	User   string `json:"user"`             // target user
	By     string `json:"by,omitempty"`     // Phase 14: acting admin (required when Reason="removed" or Event in {join,promote,demote,rename})
	Reason string `json:"reason,omitempty"` // "" | "removed" | "retirement" | "retirement_succession"
	Name   string `json:"name,omitempty"`   // Phase 14: new name for Event="rename"
	Quiet  bool   `json:"quiet,omitempty"`  // Phase 14: suppress inline system message (never true for kicks)
}

// GroupLeft is the server's confirmation that a leave_group request
// succeeded. Sent only to the leaving user (across all of their active
// sessions). The leaver is not included in group_event broadcasts because
// they have already been removed from member lists.
//
// Reason distinguishes self-leave from admin-triggered removal:
//   - ""           self-leave via /leave
//   - "removed"    Phase 14: admin-initiated removal via handleRemoveFromGroup.
//                  By carries the kicking admin's user ID for rendering
//                  "You were removed from the group by alice".
//   - "retirement" the user's account was retired
//
// Phase 14 historical note: "admin" was the reason code used by the
// pre-Phase-14 CLI escape hatch. That path was deleted entirely in Phase 14.
// Treat any persisted "admin" rows as equivalent to "removed" (no new
// rows should appear with that value in v1).
type GroupLeft struct {
	Type   string `json:"type"`
	Group  string `json:"group"`
	Reason string `json:"reason,omitempty"` // "" | "removed" | "retirement"
	By     string `json:"by,omitempty"`     // Phase 14: kicking admin's user ID; required when Reason="removed", empty otherwise
}

// LeaveRoom requests that the server remove the caller from a room. Gated
// by the server's allow_self_leave_rooms config flag — when disabled, the
// server returns an "forbidden" error and does not touch state.
type LeaveRoom struct {
	Type string `json:"type"` // "leave_room"
	Room string `json:"room"`
}

// RoomLeft is the server's confirmation that a leave_room request
// succeeded. Sent to every active session of the leaving user. The leaver
// is not included in room_event broadcasts because they have already been
// removed from room_members.
type RoomLeft struct {
	Type   string `json:"type"` // "room_left"
	Room   string `json:"room"`
	Reason string `json:"reason,omitempty"` // "" | "admin" | "retirement" | "user_retired"
}

// Phase 12: Room retirement and delete

// RoomRetired is broadcast to every connected member of a room at the
// moment the room is retired. Carries the post-retirement (suffixed)
// display name so clients can update their local cache immediately.
// Also used inside the RetiredRoomsList catchup payload sent during
// the connect handshake.
type RoomRetired struct {
	Type        string `json:"type"`             // "room_retired"
	Room        string `json:"room"`
	DisplayName string `json:"display_name"`
	RetiredAt   string `json:"retired_at"`
	RetiredBy   string `json:"retired_by"`
	Reason      string `json:"reason,omitempty"`
}

// RetiredRoomsList is sent during the connect handshake (BEFORE
// room_list) to catch up devices that were offline when a room was
// retired. Carries every retired room where the user is still in
// room_members.
type RetiredRoomsList struct {
	Type  string        `json:"type"` // "retired_rooms"
	Rooms []RoomRetired `json:"rooms"`
}

// RoomUpdated is broadcast to connected members when an admin runs
// `sshkey-ctl update-topic` or `sshkey-ctl rename-room`. Phase 16
// Gap 1 — the server pushes a fresh snapshot of the affected room's
// display name and topic, and the client applies it by upserting
// its local rooms-table row.
//
// One event type covers both verbs: whichever field actually
// changed gets reflected on the next render; the unchanged field is
// overwritten with its current value (a no-op). The client doesn't
// need to know which CLI verb produced the event.
type RoomUpdated struct {
	Type        string `json:"type"`         // "room_updated"
	Room        string `json:"room"`         // room nanoid
	DisplayName string `json:"display_name"` // post-change display name
	Topic       string `json:"topic"`        // post-change topic
}

// DeleteRoom is the client-initiated request to remove a room from
// the user's view. Parallel to DeleteGroup. The server runs the leave
// flow, records a deleted_rooms sidecar row for multi-device catchup,
// and echoes RoomDeleted back to every connected session of the caller.
type DeleteRoom struct {
	Type string `json:"type"` // "delete_room"
	Room string `json:"room"`
}

// RoomDeleted is the server's confirmation that a delete_room request
// succeeded. Sent to every active session of the caller so all of
// their devices can purge local state in lockstep. Distinct from
// RoomLeft — RoomLeft keeps local history, RoomDeleted purges it.
type RoomDeleted struct {
	Type string `json:"type"` // "room_deleted"
	Room string `json:"room"`
}

// DeletedRoomsList is the offline-catchup payload sent during the
// connect handshake (BEFORE room_list and retired_rooms) listing
// every room ID this user has previously /delete'd. Clients run the
// same purge path as RoomDeleted for each entry.
type DeletedRoomsList struct {
	Type  string   `json:"type"` // "deleted_rooms"
	Rooms []string `json:"rooms"`
}

// Phase 20: server-authoritative multi-device /leave catchup.
// Mirror of the server-side types — see sshkey-chat's messages.go
// for the full design commentary. On connect, server sends
// LeftRoomsList / LeftGroupsList BEFORE RoomList / GroupList so
// clients have reason codes in hand before reconciling sidebars.

// LeftRoomEntry is one entry in a LeftRoomsList catchup message.
type LeftRoomEntry struct {
	Room        string `json:"room"`
	Reason      string `json:"reason"`                 // "" | "removed" | "user_retired"
	InitiatedBy string `json:"initiated_by,omitempty"` // admin user_id for "removed", "system" for retirement
	LeftAt      int64  `json:"left_at"`
}

// LeftRoomsList is sent on the connect handshake with the most
// recent leave per (user, room) not superseded by a re-join.
type LeftRoomsList struct {
	Type  string          `json:"type"` // "left_rooms"
	Rooms []LeftRoomEntry `json:"rooms"`
}

// LeftGroupEntry is one entry in a LeftGroupsList catchup message.
type LeftGroupEntry struct {
	Group       string `json:"group"`
	Reason      string `json:"reason"`                 // "" | "removed" | "retirement"
	InitiatedBy string `json:"initiated_by,omitempty"` // admin user_id for "removed", "system" for retirement
	LeftAt      int64  `json:"left_at"`
}

// LeftGroupsList is the group DM analogue of LeftRoomsList.
type LeftGroupsList struct {
	Type   string           `json:"type"` // "left_groups"
	Groups []LeftGroupEntry `json:"groups"`
}

// 1:1 DM messages

type CreateDM struct {
	Type  string `json:"type"`  // "create_dm"
	Other string `json:"other"` // single other user ID
}

type DMCreated struct {
	Type    string   `json:"type"`    // "dm_created"
	DM      string   `json:"dm"`
	Members []string `json:"members"` // always [user_a, user_b]
}

type SendDM struct {
	Type        string            `json:"type"` // "send_dm"
	DM          string            `json:"dm"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type DM struct {
	Type        string            `json:"type"` // "dm"
	ID          string            `json:"id"`
	From        string            `json:"from"`
	DM          string            `json:"dm"`
	TS          int64             `json:"ts"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	EditedAt    int64             `json:"edited_at,omitempty"` // Phase 15
	CorrID      string            `json:"corr_id,omitempty"`   // Phase 17c: originator-only ack echo
}

// EditDM — 1:1 DM edit envelope (Phase 15 client → server).
type EditDM struct {
	Type        string            `json:"type"` // "edit_dm"
	ID          string            `json:"id"`
	DM          string            `json:"dm"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

// DMEdited — 1:1 DM edit broadcast (Phase 15 server → client).
type DMEdited struct {
	Type        string            `json:"type"` // "dm_edited"
	ID          string            `json:"id"`
	From        string            `json:"from"`
	DM          string            `json:"dm"`
	TS          int64             `json:"ts"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	Payload     string            `json:"payload"`
	FileIDs     []string          `json:"file_ids,omitempty"`
	Signature   string            `json:"signature"`
	EditedAt    int64             `json:"edited_at"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type LeaveDM struct {
	Type string `json:"type"` // "leave_dm"
	DM   string `json:"dm"`
}

type DMLeft struct {
	Type string `json:"type"` // "dm_left"
	DM   string `json:"dm"`
}

type DMList struct {
	Type string   `json:"type"` // "dm_list"
	DMs  []DMInfo `json:"dms"`
}

type DMInfo struct {
	ID      string   `json:"id"`
	Members []string `json:"members"` // always [user_a, user_b]
	// LeftAtForCaller is the per-user history cutoff for the recipient of
	// this dm_list. 0 = the caller is an active party. >0 = the caller has
	// previously left this DM and the unix timestamp tells the client when.
	// Used by sync to propagate /delete state to other devices that were
	// offline when the leave happened.
	LeftAtForCaller int64 `json:"left_at_for_caller,omitempty"`
}

// Deletion

type Delete struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type Deleted struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	DeletedBy string `json:"deleted_by"`
	TS        int64  `json:"ts"`
	Room      string `json:"room,omitempty"`
	Group     string `json:"group,omitempty"`
	DM        string `json:"dm,omitempty"`
	CorrID    string `json:"corr_id,omitempty"` // Phase 17c
}

// Typing

type Typing struct {
	Type  string `json:"type"`
	Room  string `json:"room,omitempty"`
	Group string `json:"group,omitempty"`
	DM    string `json:"dm,omitempty"`
	User  string `json:"user,omitempty"`
}

// Read receipts

type Read struct {
	Type     string `json:"type"`
	Room     string `json:"room,omitempty"`
	Group    string `json:"group,omitempty"`
	DM       string `json:"dm,omitempty"`
	User     string `json:"user,omitempty"`
	LastRead string `json:"last_read"`
}

type Unread struct {
	Type     string `json:"type"`
	Room     string `json:"room,omitempty"`
	Group    string `json:"group,omitempty"`
	DM       string `json:"dm,omitempty"`
	Count    int    `json:"count"`
	LastRead string `json:"last_read"`
}

// Reactions

type React struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Room        string            `json:"room,omitempty"`
	Group       string            `json:"group,omitempty"`
	DM          string            `json:"dm,omitempty"`
	Epoch       int64             `json:"epoch,omitempty"`
	WrappedKeys map[string]string `json:"wrapped_keys,omitempty"`
	Payload     string            `json:"payload"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type Reaction struct {
	Type        string            `json:"type"`
	ReactionID  string            `json:"reaction_id"`
	ID          string            `json:"id"`
	Room        string            `json:"room,omitempty"`
	Group       string            `json:"group,omitempty"`
	DM          string            `json:"dm,omitempty"`
	User        string            `json:"user"`
	TS          int64             `json:"ts"`
	Epoch       int64             `json:"epoch,omitempty"`
	WrappedKeys map[string]string `json:"wrapped_keys,omitempty"`
	Payload     string            `json:"payload"`
	Signature   string            `json:"signature"`
	CorrID      string            `json:"corr_id,omitempty"` // Phase 17c
}

type Unreact struct {
	Type       string `json:"type"`
	ReactionID string `json:"reaction_id"`
	CorrID     string `json:"corr_id,omitempty"` // Phase 17c
}

type ReactionRemoved struct {
	Type       string `json:"type"`
	ReactionID string `json:"reaction_id"`
	ID         string `json:"id"`
	Room       string `json:"room,omitempty"`
	Group      string `json:"group,omitempty"`
	DM         string `json:"dm,omitempty"`
	User       string `json:"user"`
	CorrID     string `json:"corr_id,omitempty"` // Phase 17c
}

// Pins

type Pin struct {
	Type   string `json:"type"`
	Room   string `json:"room"`
	ID     string `json:"id"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type Pinned struct {
	Type     string `json:"type"`
	Room     string `json:"room"`
	ID       string `json:"id"`
	PinnedBy string `json:"pinned_by"`
	TS       int64  `json:"ts"`
}

type Unpin struct {
	Type   string `json:"type"`
	Room   string `json:"room"`
	ID     string `json:"id"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type Unpinned struct {
	Type string `json:"type"`
	Room string `json:"room"`
	ID   string `json:"id"`
}

type Pins struct {
	Type        string       `json:"type"`
	Room        string       `json:"room"`
	Messages    []string     `json:"messages"`
	MessageData []RawMessage `json:"message_data,omitempty"` // full message envelopes
}

// Profiles

type SetProfile struct {
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	AvatarID    string `json:"avatar_id,omitempty"`
}

type Profile struct {
	Type           string `json:"type"`
	User           string `json:"user"`
	DisplayName    string `json:"display_name"`
	AvatarID       string `json:"avatar_id,omitempty"`
	PubKey         string `json:"pubkey"`
	KeyFingerprint string `json:"key_fingerprint"`
	Admin          bool   `json:"admin,omitempty"`
	Retired        bool   `json:"retired,omitempty"`
	RetiredAt      string `json:"retired_at,omitempty"`
}

type SetStatus struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Presence

type Presence struct {
	Type        string `json:"type"`
	User        string `json:"user"`
	Status      string `json:"status"`
	DisplayName string `json:"display_name"`
	AvatarID    string `json:"avatar_id,omitempty"`
	StatusText  string `json:"status_text,omitempty"`
	LastSeen    string `json:"last_seen,omitempty"`
}

// Room and group DM lists

type RoomList struct {
	Type  string     `json:"type"`
	Rooms []RoomInfo `json:"rooms"`
}

type RoomInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Topic   string `json:"topic"`
	Members int    `json:"members"`
}

// RoomEvent is the protocol type for room audit events. Phase 20
// extended it with By and Name fields (and added topic/rename/retire
// event values) for the room audit trail.
type RoomEvent struct {
	Type   string `json:"type"`
	Room   string `json:"room"`
	Event  string `json:"event"`            // "join" | "leave" | "topic" | "rename" | "retire"
	User   string `json:"user"`
	By     string `json:"by,omitempty"`     // Phase 20: acting admin/operator
	Reason string `json:"reason,omitempty"` // "" | "removed" | "user_retired"
	Name   string `json:"name,omitempty"`   // Phase 20: new value for "topic" / "rename" events
}

type GroupList struct {
	Type   string      `json:"type"`
	Groups []GroupInfo `json:"groups"`
}

type GroupInfo struct {
	ID      string   `json:"id"`
	Members []string `json:"members"`
	Admins  []string `json:"admins,omitempty"` // Phase 14: admin user IDs (subset of Members); empty on pre-14 servers
	Name    string   `json:"name,omitempty"`
}

// History

type History struct {
	Type   string `json:"type"`
	Room   string `json:"room,omitempty"`
	Group  string `json:"group,omitempty"`
	DM     string `json:"dm,omitempty"`
	Before string `json:"before"`
	Limit  int    `json:"limit"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type HistoryResult struct {
	Type      string         `json:"type"`
	Room      string         `json:"room,omitempty"`
	Group     string         `json:"group,omitempty"`
	DM        string         `json:"dm,omitempty"`
	Messages  []RawMessage   `json:"messages"`
	Reactions []RawMessage   `json:"reactions,omitempty"`
	EpochKeys []SyncEpochKey `json:"epoch_keys,omitempty"`
	HasMore   bool           `json:"has_more"`
	CorrID    string         `json:"corr_id,omitempty"` // Phase 17c
}

// Epoch keys

type EpochKey struct {
	Type       string `json:"type"`
	Room       string `json:"room"`
	Epoch      int64  `json:"epoch"`
	WrappedKey string `json:"wrapped_key"`
}

type EpochTrigger struct {
	Type     string      `json:"type"`
	Room     string      `json:"room"`
	NewEpoch int64       `json:"new_epoch"`
	Members  []MemberKey `json:"members"`
}

type MemberKey struct {
	User   string `json:"user"`
	PubKey string `json:"pubkey"`
}

type EpochRotate struct {
	Type        string            `json:"type"`
	Room        string            `json:"room"`
	Epoch       int64             `json:"epoch"`
	WrappedKeys map[string]string `json:"wrapped_keys"`
	MemberHash  string            `json:"member_hash"`
}

type EpochConfirmed struct {
	Type  string `json:"type"`
	Room  string `json:"room"`
	Epoch int64  `json:"epoch"`
}

// File transfer

type UploadStart struct {
	Type        string `json:"type"`
	UploadID    string `json:"upload_id"`
	Size        int64  `json:"size"`
	ContentHash string `json:"content_hash"` // "blake2b-256:<hex>" of encrypted bytes
	Room        string `json:"room,omitempty"`
	Group       string `json:"group,omitempty"`
	DM          string `json:"dm,omitempty"`
	CorrID      string `json:"corr_id,omitempty"` // Phase 17c
}

type UploadReady struct {
	Type     string `json:"type"`
	UploadID string `json:"upload_id"`
	CorrID   string `json:"corr_id,omitempty"` // Phase 17c
}

type UploadComplete struct {
	Type     string `json:"type"`
	UploadID string `json:"upload_id"`
	FileID   string `json:"file_id"`
	CorrID   string `json:"corr_id,omitempty"` // Phase 17c
}

// UploadError is sent by the server when an upload_start is rejected
// (rate limit, size limit, etc.). Clients fail the matching pending upload.
type UploadError struct {
	Type         string `json:"type"`
	UploadID     string `json:"upload_id"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	RetryAfterMs int64  `json:"retry_after_ms,omitempty"` // Phase 17 Step 6
	CorrID       string `json:"corr_id,omitempty"`        // Phase 17c
}

// DownloadError is sent by the server when a download is rejected (file
// not found, open failure, missing download channel). Clients fail the
// matching pending download.
type DownloadError struct {
	Type    string `json:"type"`
	FileID  string `json:"file_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
	CorrID  string `json:"corr_id,omitempty"` // Phase 17c
}

type Download struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type DownloadStart struct {
	Type        string `json:"type"`
	FileID      string `json:"file_id"`
	Size        int64  `json:"size"`
	ContentHash string `json:"content_hash"`       // "blake2b-256:<hex>" of encrypted bytes
	CorrID      string `json:"corr_id,omitempty"`  // Phase 17c
}

type DownloadComplete struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
}

// Push

type PushRegister struct {
	Type     string `json:"type"`
	Platform string `json:"platform"`
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
}

type PushRegistered struct {
	Type     string `json:"type"`
	Platform string `json:"platform"`
}

// Server events

type ServerShutdown struct {
	Type        string `json:"type"`
	Message     string `json:"message"`
	ReconnectIn int    `json:"reconnect_in"`
}

// Account retirement

type RetireMe struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type UserRetired struct {
	Type string `json:"type"`
	User string `json:"user"`
	Ts   int64  `json:"ts"`
}

// UserUnretired is the inverse of UserRetired — broadcast when an
// admin runs `sshkey-ctl unretire-user` to reverse a mistaken
// retirement. The client deletes the user from c.retired so the
// [retired] marker is flushed from sidebar labels, info panels, and
// message headers. Phase 16 Gap 1.
//
// What this does NOT signal: that the user has been re-added to any
// rooms or groups. The retirement cascade removed the user from
// every shared context, and unretirement is intentionally minimal —
// it only flips the flag. Operators must manually re-add the user
// via add-to-room or in-group /add. The broadcast simply tells
// clients to stop rendering the [retired] decoration.
type UserUnretired struct {
	Type string `json:"type"`
	User string `json:"user"`
	Ts   int64  `json:"ts"`
}

type RetiredUsers struct {
	Type  string        `json:"type"`
	Users []RetiredUser `json:"users"`
}

type RetiredUser struct {
	User      string `json:"user"`
	RetiredAt string `json:"retired_at"`
}

type DeviceRevoked struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id"`
	Reason   string `json:"reason"`
}

// Device management (user-scoped)

type ListDevices struct {
	Type   string `json:"type"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type DeviceList struct {
	Type    string       `json:"type"`
	Devices []DeviceInfo `json:"devices"`
	CorrID  string       `json:"corr_id,omitempty"` // Phase 17c
}

type DeviceInfo struct {
	DeviceID     string `json:"device_id"`
	LastSyncedAt string `json:"last_synced_at,omitempty"`
	CreatedAt    string `json:"created_at"`
	Current      bool   `json:"current,omitempty"`
	Revoked      bool   `json:"revoked,omitempty"`
}

type RevokeDevice struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id"`
}

type DeviceRevokeResult struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

type AdminNotify struct {
	Type        string `json:"type"`
	Event       string `json:"event"`
	Fingerprint string `json:"fingerprint"`
	Attempts    int    `json:"attempts"`
	FirstSeen   string `json:"first_seen"`
}

// Pending keys management (admin-only)

type ListPendingKeys struct {
	Type string `json:"type"` // "list_pending_keys"
}

type PendingKeyEntry struct {
	Fingerprint string `json:"fingerprint"`
	Attempts    int    `json:"attempts"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
}

type PendingKeysList struct {
	Type string            `json:"type"` // "pending_keys_list"
	Keys []PendingKeyEntry `json:"keys"`
}

// Room membership

type RoomMembers struct {
	Type   string `json:"type"` // "room_members"
	Room   string `json:"room"`
	CorrID string `json:"corr_id,omitempty"` // Phase 17c
}

type RoomMembersList struct {
	Type    string   `json:"type"`              // "room_members_list"
	Room    string   `json:"room"`
	Members []string `json:"members"`
	CorrID  string   `json:"corr_id,omitempty"` // Phase 17c
}

// Errors

type Error struct {
	Type         string `json:"type"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	Ref          string `json:"ref,omitempty"`
	RetryAfterMs int64  `json:"retry_after_ms,omitempty"` // Phase 17: populated on rate-limit rejections; client backoff hint
	CorrID       string `json:"corr_id,omitempty"`        // Phase 17c: echoed from the inbound request
}

// Phase 15 error code constants for edit handler responses. Exported
// so app-layer code can branch on them without string-literal churn
// when the server mirrors for new codes. Only the two surfaced codes
// are exported — the byte-identical "not_authorized" / "deleted" codes
// are collapsed into ErrUnknownX responses on the wire and are never
// observed directly by the client.
const (
	ErrEditNotMostRecent = "edit_not_most_recent"
	ErrEditWindowExpired = "edit_window_expired"
)

// Decrypted payload (client-side only — this is what's inside the encrypted payload field)

type DecryptedPayload struct {
	Body        string       `json:"body"`
	Seq         int64        `json:"seq"`
	DeviceID    string       `json:"device_id"`
	Mentions    []string     `json:"mentions,omitempty"`
	ReplyTo     string       `json:"reply_to,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Previews    []Preview    `json:"previews,omitempty"`
}

type Attachment struct {
	FileID      string `json:"file_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	ThumbnailID string `json:"thumbnail_id,omitempty"`
	FileEpoch   int64  `json:"file_epoch,omitempty"` // rooms: which epoch key decrypts this file
	FileKey     string `json:"file_key,omitempty"`   // DMs: base64 per-file symmetric key K_file
}

type Preview struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageID     string `json:"image_id,omitempty"`
}

// Decrypted reaction payload

type DecryptedReaction struct {
	Emoji    string `json:"emoji"`
	Target   string `json:"target"`
	Seq      int64  `json:"seq"`
	DeviceID string `json:"device_id"`
}

// RawMessage is a JSON object that hasn't been decoded into a specific type yet.
type RawMessage = json.RawMessage
