package store

// GetActiveRooms (offline-startup-browse Step 3): the offline sidebar
// enumerator must return only rooms that are neither left nor retired,
// in a stable (id) order. Four rows cover the (left_at, retired_at) grid.

import (
	"path/filepath"
	"testing"
)

func openOfflineTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGetActiveRooms_ExcludesLeftAndRetired(t *testing.T) {
	st := openOfflineTestStore(t)
	for _, id := range []string{"room_b_active", "room_a_active", "room_c_left", "room_d_retired", "room_e_both"} {
		if err := st.UpsertRoom(id, "name-"+id, "", 3); err != nil {
			t.Fatalf("UpsertRoom %s: %v", id, err)
		}
	}
	if err := st.MarkRoomLeft("room_c_left", 1700000000, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRoomRetired("room_d_retired", "name-room_d_retired (retired)", 1700000001); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRoomLeft("room_e_both", 1700000002, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRoomRetired("room_e_both", "name-room_e_both (retired)", 1700000003); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetActiveRooms()
	if err != nil {
		t.Fatalf("GetActiveRooms: %v", err)
	}
	want := []string{"room_a_active", "room_b_active"} // ORDER BY id
	if len(got) != len(want) {
		t.Fatalf("GetActiveRooms = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GetActiveRooms = %v, want %v (stable id order)", got, want)
		}
	}

	// Rejoin clears left_at → room becomes active again.
	if err := st.MarkRoomRejoined("room_c_left"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetActiveRooms()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2] != "room_c_left" {
		t.Fatalf("after rejoin GetActiveRooms = %v, want the rejoined room included", got)
	}
}

func TestGetActiveRooms_EmptyStore(t *testing.T) {
	st := openOfflineTestStore(t)
	got, err := st.GetActiveRooms()
	if err != nil {
		t.Fatalf("GetActiveRooms on empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rooms, got %v", got)
	}
}
