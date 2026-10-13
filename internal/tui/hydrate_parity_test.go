package tui

// Offline-browse Step 3 (R4 parity): hydrateOfflineUI must derive from the
// local store exactly what the live room_list / group_list / dm_list
// handlers derive from equivalent frames — active + left + retired rooms
// with their markers, active + archived groups in a stable order, visible
// DMs only — and wire the resolvers and initial context the same way.

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newOfflineHydrateApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	client.SetUserIDForTesting(c, "usr_self")
	a := &App{
		client:      c,
		statusBar:   NewStatusBar(),
		sidebar:     NewSidebar(),
		messages:    NewMessages(),
		input:       NewInput(),
		memberPanel: NewMemberPanel(),
		search:      NewSearch(),
		newConv:     NewNewConv(),
		width:       100,
		height:      40,
	}
	return a, st
}

func TestHydrateOfflineUI_ParityWithLiveHandlers(t *testing.T) {
	a, st := newOfflineHydrateApp(t)
	a.cfg.DataDir = t.TempDir() // the files dir derives from this (inline-image cache)

	// Rooms: 2 active, 1 left, 1 retired. Ids chosen so each ORDER BY id
	// segment of the merge is unambiguous.
	for _, id := range []string{"room_a", "room_b", "room_c_left", "room_d_retired"} {
		if err := st.UpsertRoom(id, "name-"+id, "", 2); err != nil {
			t.Fatalf("UpsertRoom %s: %v", id, err)
		}
	}
	if err := st.MarkRoomLeft("room_c_left", 1700000000, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRoomRetired("room_d_retired", "name-room_d_retired (retired)", 1700000001); err != nil {
		t.Fatal(err)
	}
	// Groups: 1 active (named), 1 archived (unnamed → member-list label).
	if err := st.StoreGroup("grp_b", "Beta", "usr_self,usr_bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.StoreGroup("grp_a", "", "usr_self,usr_carol"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkGroupLeft("grp_a", 1700000002, ""); err != nil {
		t.Fatal(err)
	}
	// DMs: 1 visible, 1 hidden tombstone (MarkDMLeft sets hidden = 1).
	if err := st.StoreDM("dm_visible", "usr_self", "usr_bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.StoreDM("dm_hidden", "usr_self", "usr_carol"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDMLeft("dm_hidden", 1700000003); err != nil {
		t.Fatal(err)
	}

	a.hydrateOfflineUI()

	// Rooms: active (by id), then archived, then retired — the room_list
	// handler's merge order — with the same markers.
	wantRooms := []string{"room_a", "room_b", "room_c_left", "room_d_retired"}
	if len(a.sidebar.rooms) != len(wantRooms) {
		t.Fatalf("sidebar rooms = %v, want %v", a.sidebar.rooms, wantRooms)
	}
	for i := range wantRooms {
		if a.sidebar.rooms[i] != wantRooms[i] {
			t.Fatalf("sidebar rooms = %v, want %v", a.sidebar.rooms, wantRooms)
		}
	}
	if !a.sidebar.leftRooms["room_c_left"] {
		t.Error("left room must carry the left marker")
	}
	if a.sidebar.leftRooms["room_a"] || a.sidebar.leftRooms["room_d_retired"] {
		t.Error("non-left rooms must not carry the left marker")
	}
	if !a.sidebar.retiredRooms["room_d_retired"] {
		t.Error("retired room must carry the retired marker")
	}
	if a.sidebar.retiredRooms["room_a"] || a.sidebar.retiredRooms["room_c_left"] {
		t.Error("non-retired rooms must not carry the retired marker")
	}

	// Groups: union of active + archived, sorted by id (map source).
	if len(a.sidebar.groups) != 2 || a.sidebar.groups[0].ID != "grp_a" || a.sidebar.groups[1].ID != "grp_b" {
		t.Fatalf("sidebar groups = %+v, want grp_a, grp_b in id order", a.sidebar.groups)
	}
	if a.sidebar.groups[1].Name != "Beta" {
		t.Errorf("named group lost its name: %+v", a.sidebar.groups[1])
	}
	if m := a.sidebar.groups[0].Members; len(m) != 2 || m[0] != "usr_self" || m[1] != "usr_carol" {
		t.Errorf("group members = %v, want [usr_self usr_carol]", m)
	}
	if !a.sidebar.leftGroups["grp_a"] {
		t.Error("archived group must carry the left marker")
	}
	if a.sidebar.leftGroups["grp_b"] {
		t.Error("active group must not carry the left marker")
	}

	// DMs: hidden tombstones excluded, exactly like the dm_list filter.
	if len(a.sidebar.dms) != 1 || a.sidebar.dms[0].ID != "dm_visible" {
		t.Fatalf("sidebar dms = %+v, want only dm_visible", a.sidebar.dms)
	}
	if m := a.sidebar.dms[0].Members; len(m) != 2 || m[0] != "usr_self" || m[1] != "usr_bob" {
		t.Errorf("dm members = %v, want [usr_self usr_bob]", m)
	}
	if a.sidebar.selfUserID != "usr_self" {
		t.Errorf("sidebar selfUserID = %q, want usr_self", a.sidebar.selfUserID)
	}

	// Initial context + identity + resolvers, mirroring connectedWithClient.
	if a.messages.room != "room_a" {
		t.Errorf("initial context room = %q, want first active room", a.messages.room)
	}
	if a.messages.currentUserID != "usr_self" {
		t.Errorf("identity not synced: currentUserID = %q", a.messages.currentUserID)
	}
	if a.messages.resolveName == nil || a.sidebar.resolveRoomName == nil || a.search.resolveName == nil {
		t.Error("client resolvers must be wired by hydrateOfflineUI")
	}
	// Cached inline images / previews render from <filesDir>/<fileID>; the
	// live connect handler sets this and the offline landing must too.
	if want := config.FilesDir(a.cfg.DataDir); a.messages.filesDir != want {
		t.Errorf("messages.filesDir = %q, want %q (cached images read as missing without it)", a.messages.filesDir, want)
	}
}

func TestHydrateOfflineUI_NoStoreIsSafe(t *testing.T) {
	a := &App{
		client:    client.New(client.Config{}),
		statusBar: NewStatusBar(),
		sidebar:   NewSidebar(),
		messages:  NewMessages(),
	}
	a.hydrateOfflineUI() // must not panic (R13/R20 nil tolerance)
	if a.statusBar.errorMsg != "No local history for this server yet." {
		t.Fatalf("status = %q, want the no-local-history message", a.statusBar.errorMsg)
	}
	if len(a.sidebar.rooms) != 0 {
		t.Fatalf("sidebar must stay empty without a store, got %v", a.sidebar.rooms)
	}
}

func TestHydrateOfflineUI_NilClientIsNoop(t *testing.T) {
	a := &App{statusBar: NewStatusBar(), sidebar: NewSidebar(), messages: NewMessages()}
	a.hydrateOfflineUI()
	if a.statusBar.errorMsg != "" {
		t.Fatalf("nil client must be a silent no-op, got status %q", a.statusBar.errorMsg)
	}
}
