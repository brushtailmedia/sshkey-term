package tui

// q closes everything that is not a text field (q-closes-everywhere.md).
// Every overlay has a role: closes (q is rewritten as Esc and takes the
// surface's own close path), types (q is a character), own (the surface
// binds q itself), pane. The table must name every IsVisible field and must
// agree with the App's own lists; the behaviour test drives q through the
// real key path per role.

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

type qRole int

const (
	qCloses qRole = iota
	qTypes
	qOwn
	qPane
)

var qRoles = map[string]qRole{
	// with a preset in overlayOpeners
	"threadPanel":      qOwn,
	"emojiPicker":      qTypes,
	"contextMenu":      qOwn,
	"picker":           qTypes, // with a filter (as the preset has) q types; without one the picker's own q closes it
	"quitConfirm":      qOwn,   // q cancels — owner decision 2026-10-26, over the design's G2
	"deleteMsgConfirm": qOwn,
	"help":             qOwn,
	"settings":         qTypes,
	"search":           qTypes,
	"quickSwitch":      qTypes,
	"newConv":          qTypes,
	"infoPanel":        qCloses,
	"pendingPanel":     qCloses,
	"keyWarning":       qCloses,
	"leaveConfirm":     qCloses,
	"memberMenu":       qCloses,
	"verify":           qTypes,
	"addServer":        qTypes,
	"deviceMgr":        qOwn,
	"memberPanel":      qPane,
	// allow-listed (no preset yet)
	"connectFailed":      qOwn, // q quits there
	"passphrase":         qTypes,
	"retireConfirm":      qTypes,
	"leaveRoomConfirm":   qCloses,
	"deleteDMConfirm":    qCloses,
	"deleteGroupConfirm": qCloses,
	"deleteRoomConfirm":  qCloses,
	"addConfirm":         qCloses,
	"kickConfirm":        qCloses,
	"promoteConfirm":     qCloses,
	"demoteConfirm":      qCloses,
	"transferConfirm":    qCloses,
	"unverifyConfirm":    qCloses,
	"saveAttachment":     qTypes,
	"auditOverlay":       qOwn,
	"membersOverlay":     qOwn,
	"lastAdminPicker":    qOwn,
	"deviceRevoked":      qOwn,
	"newDeviceAlert":     qOwn,
	"roomAttestation":    qOwn,
	"statusPicker":       qCloses,
	"filePicker":         qTypes,
}

// Every App field with IsVisible has a role, and no role names a field
// that does not exist.
func TestQ_EveryVisibleFieldHasARole(t *testing.T) {
	typ := reflect.TypeFor[App]()
	var missing []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if _, ok := reflect.PointerTo(f.Type).MethodByName("IsVisible"); !ok {
			continue
		}
		if _, ok := qRoles[f.Name]; !ok {
			missing = append(missing, f.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("overlays without a q role: %s", strings.Join(missing, ", "))
	}
	for name := range qRoles {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("q role names a field App does not have: %s", name)
		}
	}
}

// The role table agrees with the App's own lists: with a preset open, the
// App says "q closes" exactly for the closes role, and "typing surface"
// exactly for the types role.
func TestQ_RoleTableAgreesWithTheApp(t *testing.T) {
	for _, op := range overlayOpeners {
		a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()))
		op.open(t, &a)
		role := qRoles[op.field]
		if got := a.qClosesVisibleSurface(); got != (role == qCloses) {
			t.Errorf("%s: App says q closes = %v, role table says %v", op.field, got, role == qCloses)
		}
		if got := a.anyQTypingSurfaceVisible(); got != (role == qTypes) {
			t.Errorf("%s: App says typing surface = %v, role table says %v", op.field, got, role == qTypes)
		}
	}
}

// q through the real key path, per role: closes and own → gone; types →
// still up; pane → focus on the input with the panel kept.
func TestQ_ClosesOrTypesPerRole(t *testing.T) {
	for _, op := range overlayOpeners {
		t.Run(op.field, func(t *testing.T) {
			a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages))
			op.open(t, &a)
			if op.field == "memberPanel" {
				a.setFocus(FocusMembers)
			}
			got := pressFocusKey(t, a, "q")
			switch qRoles[op.field] {
			case qCloses, qOwn:
				if op.visible(&got) {
					t.Fatalf("%s must close on q", op.field)
				}
			case qTypes:
				if !op.visible(&got) {
					t.Fatalf("%s has a text field: q must type, not close", op.field)
				}
			case qPane:
				if !op.visible(&got) || got.focus != FocusInput {
					t.Fatalf("%s: q from the panel must focus the input and keep the panel (panel %v, focus %v)", op.field, op.visible(&got), got.focus)
				}
			}
		})
	}
}

// The confirms and the status picker have no preset yet: open each directly
// and check q cancels it.
func TestQ_CancelsEveryConfirmDialog(t *testing.T) {
	cases := []struct {
		name    string
		open    func(a *App)
		visible func(a *App) bool
	}{
		{"leaveRoomConfirm", func(a *App) { a.leaveRoomConfirm.Show("r", "general") }, func(a *App) bool { return a.leaveRoomConfirm.IsVisible() }},
		{"deleteDMConfirm", func(a *App) { a.deleteDMConfirm.Show("dm", "Bob") }, func(a *App) bool { return a.deleteDMConfirm.IsVisible() }},
		{"deleteGroupConfirm", func(a *App) { a.deleteGroupConfirm.Show("g", "crew") }, func(a *App) bool { return a.deleteGroupConfirm.IsVisible() }},
		{"deleteRoomConfirm", func(a *App) { a.deleteRoomConfirm.Show("r", "general", false) }, func(a *App) bool { return a.deleteRoomConfirm.IsVisible() }},
		{"addConfirm", func(a *App) { a.addConfirm.Show("g", "crew", "usr_b", "Bob") }, func(a *App) bool { return a.addConfirm.IsVisible() }},
		{"kickConfirm", func(a *App) { a.kickConfirm.Show("g", "crew", "usr_b", "Bob", 3) }, func(a *App) bool { return a.kickConfirm.IsVisible() }},
		{"promoteConfirm", func(a *App) { a.promoteConfirm.Show("g", "crew", "usr_b", "Bob") }, func(a *App) bool { return a.promoteConfirm.IsVisible() }},
		{"demoteConfirm", func(a *App) { a.demoteConfirm.Show("g", "crew", "usr_b", "Bob", 2, false) }, func(a *App) bool { return a.demoteConfirm.IsVisible() }},
		{"transferConfirm", func(a *App) { a.transferConfirm.Show("g", "crew", "usr_b", "Bob", false) }, func(a *App) bool { return a.transferConfirm.IsVisible() }},
		{"unverifyConfirm", func(a *App) { a.unverifyConfirm.Show("usr_b", "Bob") }, func(a *App) bool { return a.unverifyConfirm.IsVisible() }},
		{"statusPicker", func(a *App) { a.statusPicker.Show("online") }, func(a *App) bool { return a.statusPicker.IsVisible() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t, withContext("general", "", ""))
			tc.open(&a)
			if !tc.visible(&a) || !a.qClosesVisibleSurface() {
				t.Fatalf("precondition: %s open and classified as closing on q", tc.name)
			}
			got := pressFocusKey(t, a, "q")
			if tc.visible(&got) {
				t.Fatalf("q must cancel %s", tc.name)
			}
		})
	}
}

// From a pane q is a pure "back to the input": the draft and a reply
// context survive, which Esc would have cancelled.
func TestQ_FromAPaneReturnsToTheInputWithoutCancelling(t *testing.T) {
	for _, from := range []Focus{FocusMessages, FocusSidebar} {
		a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(from))
		a.input.textInput.SetValue("half")
		a.input.SetReplyContext("mid", "Bob: a reply", "general", "", "")
		got := pressFocusKey(t, a, "q")
		if got.focus != FocusInput || got.input.Value() != "half" || !got.input.IsReplying() {
			t.Fatalf("from %v: focus %v, draft %q, replying %v; want the input with everything kept", from, got.focus, got.input.Value(), got.input.IsReplying())
		}
	}
	// In the input, q is a letter.
	a := newTestApp(t, withContext("general", "", ""))
	got := pressFocusKey(t, a, "q")
	if got.focus != FocusInput || got.input.Value() != "q" {
		t.Fatalf("in the input q must type: focus %v, draft %q", got.focus, got.input.Value())
	}
}

func TestQ_CollapsesThePinnedBarAndCancelsNavMode(t *testing.T) {
	// The expanded bar takes the key before focus routing, so q collapses
	// it from the input too, without typing.
	for _, from := range []Focus{FocusMessages, FocusInput} {
		a := newTestApp(t, withContext("general", "", ""), withFocus(from))
		a.pinnedBar.expanded = true
		got := pressFocusKey(t, a, "q")
		if got.pinnedBar.expanded || got.input.Value() != "" {
			t.Fatalf("from %v: q must collapse the expanded pinned bar and type nothing: expanded %v, draft %q", from, got.pinnedBar.expanded, got.input.Value())
		}
	}

	b := newTestApp(t, withContext("general", "", ""))
	b.navMode = true
	got := pressFocusKey(t, b, "q")
	if got.navMode || got.input.Value() != "" {
		t.Fatalf("q must cancel nav mode and type nothing: nav %v, draft %q", got.navMode, got.input.Value())
	}
}
