package protocol

// Phase 17c Step 5 — error taxonomy classification (client-side mirror
// of sshkey-chat/internal/protocol/categories.go).
//
// Four client-facing categories:
//
//   A-default : Retriable transient; auto-retry w/ exponential backoff,
//               surface to user on budget exhaustion.
//   A-silent  : Retriable transient; SILENT drop on client. Used for
//               rate_limited on refresh verbs (room_members,
//               device_list).
//   B         : Retriable after state fix; server pushes fresh state
//               (epoch_key, group_list) alongside the error. Client
//               applies pushed state and resends.
//   C         : Permanent user-action required; surface to UI, do NOT
//               auto-retry.
//   D         : Privacy-identical rejection; surface as generic
//               "operation rejected", no retry.
//
// The client send queue uses CategoryForCode to route each error
// response to the correct retry/surface policy.

type ErrorCategory int

const (
	CategoryUnknown ErrorCategory = iota
	CategoryADefault
	CategoryASilent
	CategoryB
	CategoryC
	CategoryD
)

func (c ErrorCategory) String() string {
	switch c {
	case CategoryADefault:
		return "A-default"
	case CategoryASilent:
		return "A-silent"
	case CategoryB:
		return "B"
	case CategoryC:
		return "C"
	case CategoryD:
		return "D"
	default:
		return "unknown"
	}
}

// CategoryForCode returns the client-response category for a given
// wire error code. Returns CategoryUnknown for codes this client
// doesn't recognize — callers should treat that as A-default
// (retry-and-surface) since that's the safest conservative UX when a
// newer server responds with a code this client version doesn't know.
//
// Refresh verbs (room_members, device_list) should override
// CategoryADefault → CategoryASilent at the call site by verb context;
// this function can't see the verb.
func CategoryForCode(code string) ErrorCategory {
	switch code {
	// A — retriable transient
	case "rate_limited", "internal_error", "server_busy":
		return CategoryADefault

	// B — retriable after state fix
	case "invalid_epoch", "epoch_conflict", "stale_member_list":
		return CategoryB

	// C — permanent user-action
	case "message_too_large", "upload_too_large",
		"edit_window_expired", "edit_not_most_recent",
		"invalid_wrapped_keys",
		"user_retired", "room_retired",
		"forbidden", "not_authorized",
		"already_member", "already_admin",
		"device_limit_exceeded",
		"too_many_members", "username_taken", "invalid_profile",
		"invalid_upload_id", "invalid_content_hash", "missing_hash",
		"invalid_context", "invalid_file_id", "invalid_message",
		"edit_not_authorized", "edit_deleted_message",
		"malformed", "invalid_id", "payload_too_large", "unknown_verb":
		return CategoryC

	// D — privacy-identical rejection
	case "denied",
		"unknown_room", "unknown_group", "unknown_dm", "unknown_user",
		"unknown_file", "not_found":
		return CategoryD
	}
	return CategoryUnknown
}
