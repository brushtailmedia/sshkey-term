package tui

import "testing"

func TestContainsMention(t *testing.T) {
	tests := []struct {
		body   string
		target string
		want   bool
	}{
		// Basic matches
		{"hey @alice check this", "@alice", true},
		{"@alice hello", "@alice", true},
		{"hello @alice", "@alice", true},
		{"@alice", "@alice", true},

		// Punctuation after mention
		{"hello @alice, how are you?", "@alice", true},
		{"talk to @alice.", "@alice", true},
		{"@alice!", "@alice", true},
		{"@alice?", "@alice", true},

		// Word boundary: @ must NOT be mid-word
		{"redalice", "@alice", false},
		{"hello@alice", "@alice", false},
		{"email@alice.com", "@alice", false},
		{"foo@alice bar", "@alice", false},

		// Trailing boundary: mention must not be a prefix of a longer word
		{"@alicexyz hello", "@alice", false},
		{"@alice123 hello", "@alice", false},

		// Multiple occurrences — one valid, one not
		{"hello@alice @alice world", "@alice", true},

		// Newlines
		{"line1\n@alice line2", "@alice", true},

		// No match
		{"hello world", "@alice", false},
		{"@bob hello", "@alice", false},
	}

	for _, tc := range tests {
		got := containsMention(tc.body, tc.target)
		if got != tc.want {
			t.Errorf("containsMention(%q, %q) = %v, want %v", tc.body, tc.target, got, tc.want)
		}
	}
}

func TestExtractMentions_WordBoundary(t *testing.T) {
	input := NewInput()
	input.SetMembers([]MemberEntry{
		{Username: "usr_alice123", DisplayName: "Alice"},
		{Username: "usr_bob456", DisplayName: "Bob"},
	})

	// Should match: @Alice at word boundary
	mentions := input.ExtractMentions("hey @Alice check this")
	if len(mentions) != 1 || mentions[0] != "usr_alice123" {
		t.Errorf("expected [usr_alice123], got %v", mentions)
	}

	// Should NOT match: @Alice mid-word
	mentions = input.ExtractMentions("redalice@Alice hello")
	if len(mentions) != 0 {
		t.Errorf("mid-word @Alice should not match, got %v", mentions)
	}

	// Should NOT match: @Alice as prefix of longer name
	mentions = input.ExtractMentions("@AliceXYZ hello")
	if len(mentions) != 0 {
		t.Errorf("prefix @AliceXYZ should not match @Alice, got %v", mentions)
	}

	// Multiple valid mentions
	mentions = input.ExtractMentions("@Alice and @Bob agree")
	if len(mentions) != 2 {
		t.Errorf("expected 2 mentions, got %v", mentions)
	}
}
