package tui

// V8 — read-only room input allow-list normalization. The gate permits only
// /delete, /search, /help, /? in a retired/left room, matching the verb after
// trimming whitespace and lowercasing, ignoring trailing args.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

func TestCommandAllowedInReadOnlyRoom(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Allowed verbs.
		{"/delete", true},
		{"/search", true},
		{"/help", true},
		{"/?", true},
		// Normalization: leading/trailing whitespace, case, trailing args.
		{"  /delete", true},
		{"/DELETE", true},
		{"/Delete", true},
		{"/delete confirm", true},
		{"/search needle", true},
		{"\t/help\t", true},
		// Not allowed: other slash commands.
		{"/leave", false},
		{"/setstatus away", false},
		{"/whois @alice", false},
		{"/mute", false},
		// Not allowed: near-misses.
		{"/deletex", false},
		{"/foo", false},
		{"/", false},
		// Not allowed: normal (non-slash) text.
		{"hello world", false},
		{"delete", false},
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		if got := commandAllowedInReadOnlyRoom(tc.in); got != tc.want {
			t.Errorf("commandAllowedInReadOnlyRoom(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestCompleteReadOnlyRoomCommands verifies the typing-time hint is built
// directly from the allow-list (audit fix #1): a bare "/" lists all four
// commands (not just whatever survived completeCommands' 5-item cap), "/d"
// narrows to /delete, and @-mention / non-command input is suppressed.
func TestCompleteReadOnlyRoomCommands(t *testing.T) {
	displays := func(m *CompletionModel) map[string]bool {
		out := map[string]bool{}
		if m == nil {
			return out
		}
		for _, it := range m.Items() {
			out[it.Display] = true
		}
		return out
	}

	// Bare "/" → all four allow-list commands.
	all := completeReadOnlyRoomCommands("/", len("/"))
	got := displays(all)
	for _, want := range []string{"/delete", "/search", "/help", "/?"} {
		if !got[want] {
			t.Errorf("typing / should list %q (got %v)", want, got)
		}
	}
	if all != nil && len(all.Items()) != 4 {
		t.Errorf("typing / should list exactly 4 commands, got %d", len(all.Items()))
	}

	// "/d" narrows to /delete only.
	narrowed := completeReadOnlyRoomCommands("/d", len("/d"))
	if narrowed == nil || len(narrowed.Items()) != 1 || narrowed.Items()[0].Display != "/delete" {
		t.Fatalf("typing /d should narrow to exactly /delete, got %+v", narrowed)
	}

	// "/help" matches /help (and not /?).
	help := displays(completeReadOnlyRoomCommands("/help", len("/help")))
	if !help["/help"] || help["/?"] {
		t.Errorf("typing /help should match only /help, got %v", help)
	}

	// @-mention is suppressed — no member completion in read-only rooms.
	if completeReadOnlyRoomCommands("@al", len("@al")) != nil {
		t.Error("@-mention should be suppressed in a read-only room")
	}

	// Non-command text → nil.
	if completeReadOnlyRoomCommands("hello", len("hello")) != nil {
		t.Error("normal text should not complete in a read-only room")
	}

	// A slash not in first-token position → nil (commands are first-token only).
	if completeReadOnlyRoomCommands("hi /de", len("hi /de")) != nil {
		t.Error("a non-leading slash token should not complete")
	}
}

// TestHandleCommand_VerbNormalization verifies audit fix #2: handleCommand
// lowercases the verb before routing (so /DELETE matches case "/delete") and
// preserves the original-case arg string.
func TestHandleCommand_VerbNormalization(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/DELETE Confirm-Case", nil, "rm_a", "", "")
	if i.pendingCmd == nil {
		t.Fatal("/DELETE should route to a pending command, got nil (would have been a silent no-op pre-fix)")
	}
	if i.pendingCmd.Command != "/delete" {
		t.Fatalf("verb should normalize to /delete, got %q", i.pendingCmd.Command)
	}
	if i.pendingCmd.Arg != "Confirm-Case" {
		t.Fatalf("arg should be preserved verbatim (not lowercased), got %q", i.pendingCmd.Arg)
	}
}

// TestHandleSlashCommand_ReadOnlyRoomDispatchGate verifies audit fix #3: the
// allow-list gate is enforced at the dispatch layer, so a non-allow-listed
// command reaching handleSlashCommand via any emitter is rejected in a
// read-only room. (The predicate commandAllowedInReadOnlyRoom is unit-tested
// separately above.)
func TestHandleSlashCommand_ReadOnlyRoomDispatchGate(t *testing.T) {
	a := App{
		messages:  NewMessages(),
		statusBar: NewStatusBar(),
	}
	a.messages.SetContext("rm_a", "", "")
	a.messages.SetRoomRetired(true)

	a.handleSlashCommand(&SlashCommandMsg{Command: "/leave", Room: "rm_a"})

	const want = `"/delete", "/search", and "/help" are available`
	if a.statusBar.errorMsg != want {
		t.Fatalf("blocked command at dispatch should set %q, got %q", want, a.statusBar.errorMsg)
	}
}

// TestInfoPanelReadOnly_MuteRefreshInert verifies audit fix #5: in a read-only
// room the m (mute) and r (refresh) keys are inert — no MuteToggleMsg /
// RefreshRequestMsg is emitted and mute state does not change.
func TestInfoPanelReadOnly_MuteRefreshInert(t *testing.T) {
	for _, key := range []string{"m", "r"} {
		i := InfoPanelModel{visible: true, room: "rm_a", retired: true}
		mutedBefore := i.muted
		_, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil {
			t.Errorf("read-only room: key %q should be inert (no command), got a command", key)
		}
		if i.muted != mutedBefore {
			t.Errorf("read-only room: key %q must not change mute state", key)
		}
	}
}

// TestMemberPanelCacheMiss_Notice verifies audit fix #7: an ACTIVE room with
// no member-cache entry shows the bug-signal notice rather than an empty list.
func TestMemberPanelCacheMiss_Notice(t *testing.T) {
	c := client.New(client.Config{})
	// No store row for "rm_active" → IsRoomRetired/IsRoomLeft are false
	// (active), and RoomMembers returns ok=false (cache miss).
	var m MemberPanelModel
	m.Refresh("rm_active", "", "", c, map[string]bool{}, map[string]string{})
	if m.noticeMessage != "(members unavailable — press r to refresh)" {
		t.Fatalf("active-room cache miss should set the notice, got %q", m.noticeMessage)
	}
	if len(m.members) != 0 {
		t.Fatalf("cache miss should render no member rows, got %d", len(m.members))
	}
	if m.readOnly {
		t.Fatal("cache-miss room is active, not read-only")
	}
}

// TestMemberPanelRefreshKey verifies `r` is wired into member-panel focus:
// active rooms (incl. the cache-miss notice) emit a room_members refresh;
// read-only rooms are inert (matching the info panel).
func TestMemberPanelRefreshKey(t *testing.T) {
	rKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}

	// Active member panel → r emits RefreshRequestMsg{Kind: room_members}.
	active := MemberPanelModel{focused: true}
	_, cmd := active.Update(rKey)
	if cmd == nil {
		t.Fatal("active: r should emit a refresh request")
	}
	if msg, ok := cmd().(RefreshRequestMsg); !ok || msg.Kind != "room_members" {
		t.Fatalf("r should emit RefreshRequestMsg{Kind: room_members}, got %#v", cmd())
	}

	// Active-room cache miss → r still works (the notice advertises it).
	miss := MemberPanelModel{focused: true, noticeMessage: "(members unavailable — press r to refresh)"}
	if _, cmd := miss.Update(rKey); cmd == nil {
		t.Fatal("cache-miss: r should emit a refresh request")
	}

	// Read-only room → r is inert.
	ro := MemberPanelModel{focused: true, readOnly: true, noticeMessage: "room retired"}
	if _, cmd := ro.Update(rKey); cmd != nil {
		t.Fatal("read-only room: r should be inert")
	}

	// Not focused → r does nothing.
	unfocused := MemberPanelModel{}
	if _, cmd := unfocused.Update(rKey); cmd != nil {
		t.Fatal("unfocused member panel: r should do nothing")
	}
}
