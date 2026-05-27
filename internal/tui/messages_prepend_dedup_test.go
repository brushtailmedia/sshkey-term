package tui

import "testing"

// Regression tests for the duplicate-prepend bug: the local-short-page history
// fallback prepends local rows and then asks the server with the original
// cursor, so the server's history_result overlaps rows already present.
// PrependMessages must de-duplicate by message ID (PrependMessages is the only
// prepend path) and advance the cursor by the post-dedup count.

func msgIDs(m MessagesModel) []string {
	ids := make([]string, len(m.messages))
	for i, mm := range m.messages {
		ids[i] = mm.ID
	}
	return ids
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPrependMessages_DedupesOverlapWithExisting models the real flow: a local
// prepend adds older rows, then the server history_result overlaps them and adds
// one genuinely-older row. The overlap must not render twice, and the cursor must
// stay on the same logical message.
func TestPrependMessages_DedupesOverlapWithExisting(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{{ID: "m3"}, {ID: "m4"}}
	m.cursor = 1 // on m4

	// Local-short-page prepend: older m1, m2.
	m.PrependMessages([]DisplayMessage{{ID: "m1"}, {ID: "m2"}}, true)
	// Server history_result: overlaps m1, m2 (already prepended) and adds m0.
	m.PrependMessages([]DisplayMessage{{ID: "m0"}, {ID: "m1"}, {ID: "m2"}}, false)

	want := []string{"m0", "m1", "m2", "m3", "m4"}
	if got := msgIDs(m); !eqStrings(got, want) {
		t.Errorf("duplicate/missing rows after overlapping prepend:\n got  %v\n want %v", got, want)
	}
	if m.cursor < 0 || m.cursor >= len(m.messages) || m.messages[m.cursor].ID != "m4" {
		t.Errorf("cursor drifted off m4: cursor=%d ids=%v", m.cursor, msgIDs(m))
	}
}

// TestPrependMessages_DedupesWithinBatch covers a batch that itself repeats an ID.
func TestPrependMessages_DedupesWithinBatch(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{{ID: "m5"}}

	m.PrependMessages([]DisplayMessage{{ID: "m1"}, {ID: "m1"}, {ID: "m2"}}, false)

	want := []string{"m1", "m2", "m5"}
	if got := msgIDs(m); !eqStrings(got, want) {
		t.Errorf("within-batch duplicate not collapsed:\n got  %v\n want %v", got, want)
	}
}

// TestPrependMessages_KeepsEmptyIDRows verifies empty-ID rows (e.g. system rows)
// are never collapsed by the dedupe.
func TestPrependMessages_KeepsEmptyIDRows(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{{ID: "m3"}}

	m.PrependMessages([]DisplayMessage{{ID: ""}, {ID: ""}, {ID: "m1"}}, false)

	if len(m.messages) != 4 {
		t.Fatalf("empty-ID rows were collapsed: ids=%v (want 4 rows)", msgIDs(m))
	}
	if m.messages[3].ID != "m3" {
		t.Errorf("existing row not preserved at tail: ids=%v", msgIDs(m))
	}
}
