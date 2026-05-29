package store

import "testing"

// S3 (history-state-model.md): term loaders and scroll-back page by server_order,
// NOT local rowid. This is the load-bearing case the migration fixes: remote
// history backfill inserts older messages AFTER newer local rows, so rowid order
// diverges from chronology. A rowid-based loader would then mis-order the pane
// and load the wrong "latest" window after a restart.

func msgIDs(msgs []StoredMessage) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

func eqIDs(a, b []string) bool {
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

func TestGetRoomMessages_OrdersByServerOrderNotRowid(t *testing.T) {
	s := openTestStore(t)

	// Insert a NEWER message (higher server_order) first, then an OLDER one
	// (lower server_order) — so local rowid order is the REVERSE of server_order,
	// exactly as remote backfill would produce.
	if _, err := s.InsertMessage(StoredMessage{ServerOrder: 5, ID: "newer", Sender: "a", Body: "newer", TS: 500, Room: "general"}); err != nil {
		t.Fatalf("insert newer: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{ServerOrder: 2, ID: "older", Sender: "a", Body: "older", TS: 200, Room: "general"}); err != nil {
		t.Fatalf("insert older: %v", err)
	}

	// Oldest-first BY server_order: [older, newer] — even though "newer" has the
	// lower rowid (it was inserted first).
	msgs, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := msgIDs(msgs); !eqIDs(got, []string{"older", "newer"}) {
		t.Fatalf("GetRoomMessages order = %v, want [older newer] (by server_order, not rowid)", got)
	}
}

func TestGetMessagesBefore_OrdersByServerOrderNotRowid(t *testing.T) {
	s := openTestStore(t)

	// server_order 1..4, but inserted in rowid order c,a,d,b (backfill churn).
	insert := func(id string, so int64) {
		t.Helper()
		if _, err := s.InsertMessage(StoredMessage{ServerOrder: so, ID: id, Sender: "a", Body: id, TS: so * 100, Room: "general"}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("c", 3)
	insert("a", 1)
	insert("d", 4)
	insert("b", 2)

	// Before "d" (server_order 4), generous limit: oldest-first [a b c] by
	// server_order — not by rowid (which would be [c a b]).
	got, err := s.GetMessagesBefore("general", "", "", "d", 10)
	if err != nil {
		t.Fatalf("before d: %v", err)
	}
	if ids := msgIDs(got); !eqIDs(ids, []string{"a", "b", "c"}) {
		t.Fatalf("before d = %v, want [a b c] (by server_order)", ids)
	}

	// Adjacent page (limit 2) before "d": the two rows immediately below the
	// cursor, oldest-first [b c] (server_order 2,3).
	got, err = s.GetMessagesBefore("general", "", "", "d", 2)
	if err != nil {
		t.Fatalf("before d limit2: %v", err)
	}
	if ids := msgIDs(got); !eqIDs(ids, []string{"b", "c"}) {
		t.Fatalf("before d limit2 = %v, want [b c] (adjacent page, oldest-first)", ids)
	}

	// Unknown cursor resolves to no rows (server-fallback contract).
	got, err = s.GetMessagesBefore("general", "", "", "nonexistent", 10)
	if err != nil {
		t.Fatalf("before missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("before missing cursor should be empty, got %d", len(got))
	}
}

// S5: global search stays timestamp-primary (cross-conversation recency) but uses
// server_order as a deterministic tie-breaker for same-second messages within a
// conversation. Without the tie-breaker, two messages sharing a unix-second ts
// would order arbitrarily.
func TestSearchMessages_ServerOrderTieBreaker(t *testing.T) {
	s := openTestStore(t)
	// Same room. m_lo and m_hi share ts=100 (differ only by server_order);
	// m_newer has a later ts. All match "needle".
	if _, err := s.InsertMessage(StoredMessage{ServerOrder: 1, ID: "m_lo", Sender: "a", Body: "needle one", TS: 100, Room: "general"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMessage(StoredMessage{ServerOrder: 2, ID: "m_hi", Sender: "a", Body: "needle two", TS: 100, Room: "general"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMessage(StoredMessage{ServerOrder: 3, ID: "m_newer", Sender: "a", Body: "needle three", TS: 200, Room: "general"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.SearchMessages("needle", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// ts DESC primary (m_newer first); server_order DESC tie-breaker among the
	// ts=100 pair (m_hi before m_lo).
	if got := msgIDs(res); !eqIDs(got, []string{"m_newer", "m_hi", "m_lo"}) {
		t.Fatalf("search order = %v, want [m_newer m_hi m_lo] (ts DESC, then server_order DESC)", got)
	}
	// server_order survives into the search results (scanned on both FTS + LIKE paths).
	for _, m := range res {
		if m.ServerOrder == 0 {
			t.Errorf("search result %q has zero server_order (not scanned)", m.ID)
		}
	}
}
