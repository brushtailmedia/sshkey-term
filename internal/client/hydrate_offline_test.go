package client

// Offline-browse Step 3: an OpenLocal (no Connect) must hydrate the
// persisted display names (D1) and the self user id, so DisplayName() and
// UserID() work offline exactly as they do after a welcome frame.

import "testing"

func TestOpenLocal_HydratesDisplayNamesAndSelfID(t *testing.T) {
	keyPath := writeTestKeyFile(t)
	dataDir := t.TempDir()

	// Session 1: pretend an online connect pinned Bob with a display name
	// and recorded our own id (what StoreProfile + Connect now persist).
	first := New(Config{KeyPath: keyPath, DataDir: dataDir})
	if err := first.OpenLocal(); err != nil {
		t.Fatalf("first OpenLocal: %v", err)
	}
	if err := first.store.PinKey("usr_bob", "SHA256:bob", "ssh-ed25519 AAAAbob"); err != nil {
		t.Fatal(err)
	}
	if err := first.store.SetDisplayName("usr_bob", "Bob"); err != nil {
		t.Fatal(err)
	}
	if err := first.store.SetState("self_user_id", "usr_self"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Session 2: offline open with no profile frames and no welcome.
	second := New(Config{KeyPath: keyPath, DataDir: dataDir})
	t.Cleanup(func() {
		if second.store != nil {
			second.store.Close()
		}
	})
	if second.DisplayName("usr_bob") != "usr_bob" {
		t.Fatal("precondition: before OpenLocal the name must fall back to the raw id")
	}
	if err := second.OpenLocal(); err != nil {
		t.Fatalf("second OpenLocal: %v", err)
	}
	if got := second.DisplayName("usr_bob"); got != "Bob" {
		t.Fatalf("DisplayName offline = %q, want persisted %q", got, "Bob")
	}
	if got := second.UserID(); got != "usr_self" {
		t.Fatalf("UserID offline = %q, want persisted %q", got, "usr_self")
	}
	// Seeds are partial by design: never an authority signal.
	if p := second.profiles["usr_bob"]; p == nil || p.Admin || p.Retired {
		t.Fatalf("seeded profile must exist with Admin/Retired unset, got %+v", p)
	}
}

func TestOpenLocal_SelfIDGuardKeepsWelcomeValue(t *testing.T) {
	keyPath := writeTestKeyFile(t)
	dataDir := t.TempDir()

	first := New(Config{KeyPath: keyPath, DataDir: dataDir})
	if err := first.OpenLocal(); err != nil {
		t.Fatal(err)
	}
	if err := first.store.SetState("self_user_id", "usr_stale"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Online path: welcome set userID BEFORE the store opened — the
	// persisted value must not overwrite it.
	second := New(Config{KeyPath: keyPath, DataDir: dataDir})
	t.Cleanup(func() {
		if second.store != nil {
			second.store.Close()
		}
	})
	second.userID = "usr_from_welcome"
	if err := second.OpenLocal(); err != nil {
		t.Fatal(err)
	}
	if got := second.UserID(); got != "usr_from_welcome" {
		t.Fatalf("hydrate overwrote a welcome-set userID: got %q", got)
	}
}
