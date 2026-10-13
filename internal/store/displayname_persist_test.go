package store

// pinned_keys.display_name (offline-startup-browse Step 3 / D1): the
// migration must be idempotent across re-opens, and the setter/getter must
// round-trip while excluding rows with no persisted name.

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func pinnedKeysHasColumn(t *testing.T, st *Store, name string) bool {
	t.Helper()
	rows, err := st.db.Query(`PRAGMA table_info(pinned_keys)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if colName == name {
			return true
		}
	}
	return false
}

func TestPinnedKeysDisplayNameMigration_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	st, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if !pinnedKeysHasColumn(t, st, "display_name") {
		t.Fatal("display_name column missing after first open")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Re-opening the same file must not fail on a duplicate ALTER.
	st2, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("second open (migration must be idempotent): %v", err)
	}
	t.Cleanup(func() { st2.Close() })
	if !pinnedKeysHasColumn(t, st2, "display_name") {
		t.Fatal("display_name column missing after second open")
	}
}

func TestSetDisplayName_RoundTripExcludesEmpty(t *testing.T) {
	st := openOfflineTestStore(t)
	for _, u := range []string{"usr_alice", "usr_bob", "usr_carol"} {
		if err := st.PinKey(u, "SHA256:"+u, "ssh-ed25519 AAAA"+u); err != nil {
			t.Fatalf("PinKey %s: %v", u, err)
		}
	}
	if err := st.SetDisplayName("usr_alice", "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDisplayName("usr_bob", "Bob"); err != nil {
		t.Fatal(err)
	}
	// usr_carol keeps the '' default and must be excluded.

	names, err := st.GetAllDisplayNames()
	if err != nil {
		t.Fatalf("GetAllDisplayNames: %v", err)
	}
	if len(names) != 2 || names["usr_alice"] != "Alice" || names["usr_bob"] != "Bob" {
		t.Fatalf("GetAllDisplayNames = %v, want alice+bob only", names)
	}
	if _, ok := names["usr_carol"]; ok {
		t.Fatal("empty display names must be excluded")
	}

	// Overwrite keeps a single row per user.
	if err := st.SetDisplayName("usr_alice", "Alice Chen"); err != nil {
		t.Fatal(err)
	}
	names, _ = st.GetAllDisplayNames()
	if names["usr_alice"] != "Alice Chen" || len(names) != 2 {
		t.Fatalf("after rename GetAllDisplayNames = %v", names)
	}
}
