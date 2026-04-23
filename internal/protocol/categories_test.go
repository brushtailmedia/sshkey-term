package protocol

import (
	"strings"
	"testing"
)

func TestCategoryForCode_KnownCodes(t *testing.T) {
	cases := []struct {
		code     string
		want     ErrorCategory
		wantName string
	}{
		// A-default
		{"rate_limited", CategoryADefault, "A-default"},
		{"internal_error", CategoryADefault, "A-default"},
		{"server_busy", CategoryADefault, "A-default"},

		// B
		{"invalid_epoch", CategoryB, "B"},
		{"epoch_conflict", CategoryB, "B"},
		{"stale_member_list", CategoryB, "B"},

		// C (sampled + exported constants)
		{"message_too_large", CategoryC, "C"},
		{"upload_too_large", CategoryC, "C"},
		{ErrEditWindowExpired, CategoryC, "C"},
		{ErrEditNotMostRecent, CategoryC, "C"},
		{"invalid_wrapped_keys", CategoryC, "C"},
		{"user_retired", CategoryC, "C"},
		{"room_retired", CategoryC, "C"},
		{"forbidden", CategoryC, "C"},
		{"not_authorized", CategoryC, "C"},
		{"already_member", CategoryC, "C"},
		{"already_admin", CategoryC, "C"},
		{"device_limit_exceeded", CategoryC, "C"},
		{"unknown_verb", CategoryC, "C"},
		{"invalid_message", CategoryC, "C"},
		{ErrDailyQuotaExceeded, CategoryC, "C"},

		// D
		{"denied", CategoryD, "D"},
		{"unknown_room", CategoryD, "D"},
		{"unknown_group", CategoryD, "D"},
		{"unknown_dm", CategoryD, "D"},
		{"unknown_user", CategoryD, "D"},
		{"unknown_file", CategoryD, "D"},
		{"not_found", CategoryD, "D"},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			got := CategoryForCode(tc.code)
			if got != tc.want {
				t.Errorf("CategoryForCode(%q) = %v, want %v", tc.code, got, tc.want)
			}
			if got.String() != tc.wantName {
				t.Errorf("CategoryForCode(%q).String() = %q, want %q", tc.code, got.String(), tc.wantName)
			}
		})
	}
}

func TestCategoryForCode_ExhaustiveList(t *testing.T) {
	// Keep this list in lockstep with categories.go. Any new wire code
	// added there must be listed here.
	codes := []string{
		// A
		"rate_limited", "internal_error", "server_busy",
		// B
		"invalid_epoch", "epoch_conflict", "stale_member_list",
		// C
		"message_too_large", "upload_too_large",
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
		"malformed", "invalid_id", "payload_too_large", "unknown_verb",
		"daily_quota_exceeded",
		// D
		"denied",
		"unknown_room", "unknown_group", "unknown_dm", "unknown_user",
		"unknown_file", "not_found",
	}

	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			if got := CategoryForCode(code); got == CategoryUnknown {
				t.Errorf("CategoryForCode(%q) = CategoryUnknown; expected explicit mapping", code)
			}
		})
	}
}

func TestCategoryForCode_UnknownReturnsUnknown(t *testing.T) {
	got := CategoryForCode("totally_unknown_wire_code")
	if got != CategoryUnknown {
		t.Errorf("unknown code category = %v, want %v", got, CategoryUnknown)
	}
	if got.String() != "unknown" {
		t.Errorf("CategoryUnknown.String() = %q, want %q", got.String(), "unknown")
	}
}

func TestCategoryString_AllCategoriesHaveNames(t *testing.T) {
	for _, c := range []ErrorCategory{
		CategoryADefault, CategoryASilent, CategoryB, CategoryC, CategoryD,
	} {
		name := c.String()
		if name == "" || strings.Contains(name, "unknown") {
			t.Errorf("category %d has empty/unknown name %q", c, name)
		}
	}
}
