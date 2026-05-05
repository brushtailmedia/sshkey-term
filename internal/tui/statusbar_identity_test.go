package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestStatusBarIdentity_UpdatesFromProfileAfterConnectFallback(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_status_identity"})
	client.SetUserIDForTesting(c, "usr_self")

	a := App{
		client:    c,
		messages:  NewMessages(),
		statusBar: NewStatusBar(),
	}

	// Initial connect path can race profile delivery and show fallback userID.
	a.statusBar.SetUser(c.DisplayName(c.UserID()), c.IsAdmin())
	a.messages.currentUser = c.DisplayName(c.UserID())
	a.messages.currentUserID = c.UserID()
	if a.statusBar.username != "usr_self" {
		t.Fatalf("precondition: initial username = %q, want fallback usr_self", a.statusBar.username)
	}

	// Simulate read-loop profile-cache hydration before the app receives
	// the corresponding profile server frame.
	client.SetProfileForTesting(c, &protocol.Profile{
		User:        "usr_self",
		DisplayName: "chicks",
	})
	raw, err := json.Marshal(protocol.Profile{
		Type:        "profile",
		User:        "usr_self",
		DisplayName: "chicks",
	})
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}

	model, _ := a.Update(ServerMsg{Type: "profile", Raw: raw})
	updated := model.(App)

	if updated.statusBar.username != "chicks" {
		t.Fatalf("status bar username = %q, want chicks", updated.statusBar.username)
	}
	if updated.messages.currentUser != "chicks" {
		t.Fatalf("messages currentUser = %q, want chicks", updated.messages.currentUser)
	}
	if updated.messages.currentUserID != "usr_self" {
		t.Fatalf("messages currentUserID = %q, want usr_self", updated.messages.currentUserID)
	}
}
