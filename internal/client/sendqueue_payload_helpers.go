package client

// Phase 17c Step 5 Gap 4 — type-switch helper for extracting the Room
// field from a queue entry's Payload. Used by Category B retry to
// match Pending entries to the room whose epoch_key was refreshed.
//
// Kept separate so the driver can stay focused on scheduling without
// type switches; extending to cover new verbs that carry a Room field
// only needs a case added here.

import (
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// roomFromKnownPayload returns the Room field for payload types that
// carry one, or "" if the type doesn't have a relevant room binding.
func roomFromKnownPayload(payload any) string {
	switch p := payload.(type) {
	case protocol.Send:
		return p.Room
	case protocol.Edit:
		return p.Room
	case protocol.React:
		return p.Room
	case protocol.Pin:
		return p.Room
	case protocol.Unpin:
		return p.Room
	case protocol.History:
		return p.Room
	case protocol.RoomMembers:
		return p.Room
	case protocol.UploadStart:
		return p.Room
	}
	return ""
}
