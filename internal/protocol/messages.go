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
	Conversations      []string `json:"conversations"`
	PendingSync        bool     `json:"pending_sync"`
	ActiveCapabilities []string `json:"active_capabilities"`
}

// Sync

type SyncBatch struct {
	Type      string         `json:"type"`
	Messages  []RawMessage   `json:"messages"`
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
}

// DMs

type CreateDM struct {
	Type    string   `json:"type"`
	Members []string `json:"members"`
	Name    string   `json:"name,omitempty"`
}

type DMCreated struct {
	Type         string   `json:"type"`
	Conversation string   `json:"conversation"`
	Members      []string `json:"members"`
	Name         string   `json:"name,omitempty"`
}

type SendDM struct {
	Type         string            `json:"type"`
	Conversation string            `json:"conversation"`
	WrappedKeys  map[string]string `json:"wrapped_keys"`
	Payload      string            `json:"payload"`
	FileIDs      []string          `json:"file_ids,omitempty"`
	Signature    string            `json:"signature"`
}

type DM struct {
	Type         string            `json:"type"`
	ID           string            `json:"id"`
	From         string            `json:"from"`
	Conversation string            `json:"conversation"`
	TS           int64             `json:"ts"`
	WrappedKeys  map[string]string `json:"wrapped_keys"`
	Payload      string            `json:"payload"`
	FileIDs      []string          `json:"file_ids,omitempty"`
	Signature    string            `json:"signature"`
}

type LeaveConversation struct {
	Type         string `json:"type"`
	Conversation string `json:"conversation"`
}

type RenameConversation struct {
	Type         string `json:"type"`
	Conversation string `json:"conversation"`
	Name         string `json:"name"`
}

type ConversationRenamed struct {
	Type         string `json:"type"`
	Conversation string `json:"conversation"`
	Name         string `json:"name"`
	RenamedBy    string `json:"renamed_by"`
}

type ConversationEvent struct {
	Type         string `json:"type"`
	Conversation string `json:"conversation"`
	Event        string `json:"event"`
	User         string `json:"user"`
	Reason       string `json:"reason,omitempty"` // "retirement" when leave was caused by account retirement
}

// Deletion

type Delete struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Deleted struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	DeletedBy    string `json:"deleted_by"`
	TS           int64  `json:"ts"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
}

// Typing

type Typing struct {
	Type         string `json:"type"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	User         string `json:"user,omitempty"`
}

// Read receipts

type Read struct {
	Type         string `json:"type"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	User         string `json:"user,omitempty"`
	LastRead     string `json:"last_read"`
}

type Unread struct {
	Type         string `json:"type"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	Count        int    `json:"count"`
	LastRead     string `json:"last_read"`
}

// Reactions

type React struct {
	Type         string            `json:"type"`
	ID           string            `json:"id"`
	Room         string            `json:"room,omitempty"`
	Conversation string            `json:"conversation,omitempty"`
	Epoch        int64             `json:"epoch,omitempty"`
	WrappedKeys  map[string]string `json:"wrapped_keys,omitempty"`
	Payload      string            `json:"payload"`
	Signature    string            `json:"signature"`
}

type Reaction struct {
	Type         string            `json:"type"`
	ReactionID   string            `json:"reaction_id"`
	ID           string            `json:"id"`
	Room         string            `json:"room,omitempty"`
	Conversation string            `json:"conversation,omitempty"`
	User         string            `json:"user"`
	TS           int64             `json:"ts"`
	Epoch        int64             `json:"epoch,omitempty"`
	WrappedKeys  map[string]string `json:"wrapped_keys,omitempty"`
	Payload      string            `json:"payload"`
	Signature    string            `json:"signature"`
}

type Unreact struct {
	Type       string `json:"type"`
	ReactionID string `json:"reaction_id"`
}

type ReactionRemoved struct {
	Type         string `json:"type"`
	ReactionID   string `json:"reaction_id"`
	ID           string `json:"id"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	User         string `json:"user"`
}

// Pins

type Pin struct {
	Type string `json:"type"`
	Room string `json:"room"`
	ID   string `json:"id"`
}

type Pinned struct {
	Type     string `json:"type"`
	Room     string `json:"room"`
	ID       string `json:"id"`
	PinnedBy string `json:"pinned_by"`
	TS       int64  `json:"ts"`
}

type Unpin struct {
	Type string `json:"type"`
	Room string `json:"room"`
	ID   string `json:"id"`
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

// Room/conversation lists

type RoomList struct {
	Type  string     `json:"type"`
	Rooms []RoomInfo `json:"rooms"`
}

type RoomInfo struct {
	Name    string `json:"name"`
	Topic   string `json:"topic"`
	Members int    `json:"members"`
}

type RoomEvent struct {
	Type  string `json:"type"`
	Room  string `json:"room"`
	Event string `json:"event"`
	User  string `json:"user"`
}

type ConversationList struct {
	Type          string             `json:"type"`
	Conversations []ConversationInfo `json:"conversations"`
}

type ConversationInfo struct {
	ID      string   `json:"id"`
	Members []string `json:"members"`
	Name    string   `json:"name,omitempty"`
}

// History

type History struct {
	Type         string `json:"type"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	Before       string `json:"before"`
	Limit        int    `json:"limit"`
}

type HistoryResult struct {
	Type         string         `json:"type"`
	Room         string         `json:"room,omitempty"`
	Conversation string         `json:"conversation,omitempty"`
	Messages     []RawMessage   `json:"messages"`
	EpochKeys    []SyncEpochKey `json:"epoch_keys,omitempty"`
	HasMore      bool           `json:"has_more"`
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
	Type         string `json:"type"`
	UploadID     string `json:"upload_id"`
	Size         int64  `json:"size"`
	Room         string `json:"room,omitempty"`
	Conversation string `json:"conversation,omitempty"`
}

type UploadReady struct {
	Type     string `json:"type"`
	UploadID string `json:"upload_id"`
}

type UploadComplete struct {
	Type     string `json:"type"`
	UploadID string `json:"upload_id"`
	FileID   string `json:"file_id"`
}

// UploadError is sent by the server when an upload_start is rejected
// (rate limit, size limit, etc.). Clients fail the matching pending upload.
type UploadError struct {
	Type     string `json:"type"`
	UploadID string `json:"upload_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// DownloadError is sent by the server when a download is rejected (file
// not found, open failure, missing download channel). Clients fail the
// matching pending download.
type DownloadError struct {
	Type    string `json:"type"`
	FileID  string `json:"file_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Download struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
}

type DownloadStart struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
	Size   int64  `json:"size"`
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
	Type string `json:"type"`
}

type DeviceList struct {
	Type    string       `json:"type"`
	Devices []DeviceInfo `json:"devices"`
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

// Errors

type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Ref     string `json:"ref,omitempty"`
}

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
