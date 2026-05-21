package tui

// Regression test for the rename-event double-render bug (2026-05-20):
// the server emits BOTH a legacy `group_renamed` message AND the newer
// `group_event{Event:"rename"}` for the same rename action (see the
// "follow-up cleanup" comment in sshkey-chat session.go). Pre-fix the
// term-side rendered an inline system message from BOTH handlers,
// producing a visible duplicate in the chat — one quoted, one
// unquoted — for every rename. Fix: drop the inline message in the
// legacy `group_renamed` handler; the canonical inline message lives
// on the `group_event{rename}` path (which also honors `m.Quiet`).
// The sidebar update in the legacy handler stays — the group_event
// path renders the message but doesn't touch sidebar.RenameGroup.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newRenameDedupApp(t *testing.T) *App {
	t.Helper()
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	client.SetUserIDForTesting(c, "usr_self")
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_admin", DisplayName: "Admin"})
	a := &App{client: c, statusBar: NewStatusBar()}
	a.messages = NewMessages()
	a.messages.SetContext("", "g_x", "")
	a.sidebar = NewSidebar()
	a.sidebar.SetGroups([]protocol.GroupInfo{
		{ID: "g_x", Name: "OldName", Members: []string{"usr_self", "usr_admin"}},
	})
	return a
}

func countSystemMessagesContaining(a *App, needle string) int {
	n := 0
	for _, m := range a.messages.messages {
		if strings.Contains(m.SystemText, needle) {
			n++
		}
	}
	return n
}

func TestGroupRename_NoDuplicateSystemMessage(t *testing.T) {
	a := newRenameDedupApp(t)

	// Server-emitted pair for the same rename action.
	geRaw, _ := json.Marshal(protocol.GroupEvent{
		Type: "group_event", Group: "g_x", Event: "rename",
		User: "usr_admin", By: "usr_admin", Name: "hello",
	})
	grRaw, _ := json.Marshal(protocol.GroupRenamed{
		Type: "group_renamed", Group: "g_x", Name: "hello", RenamedBy: "usr_admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: geRaw})
	a.handleServerMessage(ServerMsg{Type: "group_renamed", Raw: grRaw})

	got := countSystemMessagesContaining(a, "renamed the group")
	if got != 1 {
		// Dump for diagnosis on failure.
		texts := make([]string, 0, len(a.messages.messages))
		for _, m := range a.messages.messages {
			if m.SystemText != "" {
				texts = append(texts, m.SystemText)
			}
		}
		t.Fatalf("got %d 'renamed the group' system messages, want exactly 1\nrendered system messages:\n  %s",
			got, strings.Join(texts, "\n  "))
	}
}

func TestGroupRename_LegacyEventStillUpdatesSidebar(t *testing.T) {
	a := newRenameDedupApp(t)
	// Drive ONLY the legacy event — pre-fix it both rendered an
	// inline message AND updated the sidebar; the fix kept the
	// sidebar update. Drift-guard: future cleanup that removes the
	// whole legacy handler must move sidebar.RenameGroup somewhere
	// the new event handler reaches, or older servers that emit
	// only the legacy event will leave the sidebar name stale.
	grRaw, _ := json.Marshal(protocol.GroupRenamed{
		Type: "group_renamed", Group: "g_x", Name: "NewName", RenamedBy: "usr_admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_renamed", Raw: grRaw})

	var got string
	for _, g := range a.sidebar.groups {
		if g.ID == "g_x" {
			got = g.Name
			break
		}
	}
	if got != "NewName" {
		t.Fatalf("sidebar group name = %q, want NewName (legacy handler must still update sidebar)", got)
	}
}

func TestGroupRename_QuietGroupEventStillNoDuplicate(t *testing.T) {
	// Quiet on the group_event suppresses the inline message; the
	// legacy `group_renamed` would have rendered one anyway pre-fix
	// (it never checked Quiet — `protocol.GroupRenamed` has no
	// Quiet field). Post-fix, neither path renders. This locks the
	// Quiet contract end-to-end for renames.
	a := newRenameDedupApp(t)

	geRaw, _ := json.Marshal(protocol.GroupEvent{
		Type: "group_event", Group: "g_x", Event: "rename",
		User: "usr_admin", By: "usr_admin", Name: "hello", Quiet: true,
	})
	grRaw, _ := json.Marshal(protocol.GroupRenamed{
		Type: "group_renamed", Group: "g_x", Name: "hello", RenamedBy: "usr_admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: geRaw})
	a.handleServerMessage(ServerMsg{Type: "group_renamed", Raw: grRaw})

	if got := countSystemMessagesContaining(a, "renamed the group"); got != 0 {
		t.Fatalf("Quiet rename produced %d system messages, want 0", got)
	}
}
