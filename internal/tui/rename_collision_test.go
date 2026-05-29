package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Model-level tests for the Settings display-name rename collision fix
// (rename-collision-ux.md, Option B). No live server: drive the ProfileUpdateMsg
// submit (updateInner) and the self-`profile` / `error` server-message handlers
// (handleServerMessage) directly.

const renameSelf = "usr_self"

func newRenameApp(t *testing.T) App {
	t.Helper()
	c := client.New(client.Config{DeviceID: "dev_test"})
	client.SetUserIDForTesting(c, renameSelf)
	// A real encoder over a throwaway buffer so the submit's Enc().Encode
	// doesn't panic without a live connection.
	client.SetEncoderForTesting(c, protocol.NewEncoder(&bytes.Buffer{}))
	// Seed the confirmed self profile so DisplayName(self) is "OldName".
	client.SetProfileForTesting(c, &protocol.Profile{User: renameSelf, DisplayName: "OldName"})

	a := App{
		client:    c,
		messages:  NewMessages(),
		input:     NewInput(),
		statusBar: NewStatusBar(),
		settings:  NewSettings(),
		focus:     FocusInput,
	}
	cfg := &config.Config{Servers: []config.ServerConfig{{Name: "Test", Host: "h", Port: 2222}}}
	a.settings.Show(cfg, "", "OldName", 0)
	return a
}

func profileMsg(user, name string) ServerMsg {
	raw, _ := json.Marshal(protocol.Profile{Type: "profile", User: user, DisplayName: name})
	return ServerMsg{Type: "profile", Raw: raw}
}

func errorMsg(code, message string) ServerMsg {
	raw, _ := json.Marshal(protocol.Error{Type: "error", Code: code, Message: message})
	return ServerMsg{Type: "error", Raw: raw}
}

func correlatedErrorMsg(code, message, corrID string) ServerMsg {
	raw, _ := json.Marshal(protocol.Error{Type: "error", Code: code, Message: message, CorrID: corrID})
	return ServerMsg{Type: "error", Raw: raw}
}

func settingsActionIndex(t *testing.T, s SettingsModel, action string) int {
	t.Helper()
	for i, item := range s.items {
		if item.action == action {
			return i
		}
	}
	t.Fatalf("settings action %q not found", action)
	return -1
}

// Submit: ProfileUpdateMsg sets the in-flight marker + a tentative "Saving…"
// notice, and does NOT optimistically rewrite the displayed name to the new
// value (the root-cause false success). A second submit while in flight is a
// no-op.
func TestRename_SubmitSetsMarkerNoOptimisticSuccess(t *testing.T) {
	a := newRenameApp(t)

	model, _ := a.updateInner(ProfileUpdateMsg{DisplayName: "NewName"})
	na := model.(App)

	if !na.renameInFlight || na.renameAttempted != "NewName" {
		t.Fatalf("marker not set: inFlight=%v attempted=%q", na.renameInFlight, na.renameAttempted)
	}
	if na.settings.noticeIsError {
		t.Error("Saving notice should not be error-styled")
	}
	if !contains(na.settings.notice, "Saving") {
		t.Errorf("notice = %q, want a tentative Saving… message", na.settings.notice)
	}
	if na.settings.displayName != "OldName" {
		t.Errorf("displayed name = %q, want unchanged OldName (no optimistic rewrite)", na.settings.displayName)
	}
	if !na.settings.displayNameRenamePending {
		t.Error("settings should mirror pending rename state so display-name edit is disabled")
	}

	// Second submit while in flight: ignored (marker unchanged, no panic).
	model2, _ := na.updateInner(ProfileUpdateMsg{DisplayName: "Another"})
	na2 := model2.(App)
	if na2.renameAttempted != "NewName" {
		t.Errorf("second submit changed the attempted name to %q — should be ignored while in flight", na2.renameAttempted)
	}

	na2.settings.cursor = settingsActionIndex(t, na2.settings, "edit_name")
	settingsAfterEnter, cmd := na2.settings.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("display-name row should not emit a command while rename is pending")
	}
	if settingsAfterEnter.editing {
		t.Fatal("display-name row should not enter edit mode while rename is pending")
	}
}

// Success: a self-`profile` event whose name matches the attempt confirms —
// panel shows the new name + a success (non-error) notice; marker cleared.
func TestRename_ProfileEventConfirms(t *testing.T) {
	a := newRenameApp(t)
	a.renameInFlight = true
	a.renameAttempted = "NewName"

	a.handleServerMessage(profileMsg(renameSelf, "NewName"))

	if a.renameInFlight || a.renameAttempted != "" {
		t.Errorf("marker not cleared on confirm: inFlight=%v attempted=%q", a.renameInFlight, a.renameAttempted)
	}
	if a.settings.noticeIsError {
		t.Error("confirm notice should be success-styled, not error")
	}
	if !contains(a.settings.notice, "updated") {
		t.Errorf("notice = %q, want a success 'updated' message", a.settings.notice)
	}
	if a.settings.displayName != "NewName" {
		t.Errorf("displayed name = %q, want NewName after confirm", a.settings.displayName)
	}
	if a.settings.displayNameRenamePending {
		t.Error("settings pending mirror should clear on confirm")
	}
}

// A profile event that is NOT this rename (different user, or a different name —
// e.g. a second device renamed) must NOT confirm/clear the marker.
func TestRename_NonMatchingProfileDoesNotConfirm(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user, dn string
	}{
		{"different user", "usr_other", "NewName"},
		{"different name (second device)", renameSelf, "SomethingElse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newRenameApp(t)
			a.renameInFlight = true
			a.renameAttempted = "NewName"

			a.handleServerMessage(profileMsg(tc.user, tc.dn))

			if !a.renameInFlight || a.renameAttempted != "NewName" {
				t.Errorf("marker wrongly cleared by a non-matching profile event")
			}
		})
	}
}

// Failure: an empty-correlation server error while the marker is set surfaces
// the failure in-panel (error-styled), re-enters edit mode pre-filled with the
// rejected name, leaves the displayed name at the old confirmed value, and
// clears the marker. Covers every expected code, including rate_limited
// (pre-validation) and internal_error (the server write-hardening), per the
// "clear on empty-correlation error" rule.
func TestRename_ErrorWhilePendingShowsInPanelAndReEdits(t *testing.T) {
	for _, code := range []string{"username_taken", "invalid_profile", "rate_limited", "internal_error"} {
		t.Run(code, func(t *testing.T) {
			a := newRenameApp(t)
			a.renameInFlight = true
			a.renameAttempted = "Taken"

			a.handleServerMessage(errorMsg(code, "Display name is already in use"))

			if a.renameInFlight || a.renameAttempted != "" {
				t.Errorf("marker not cleared on %s: inFlight=%v", code, a.renameInFlight)
			}
			if !a.settings.noticeIsError {
				t.Errorf("%s: notice should be error-styled (NOT the success/check style)", code)
			}
			if !contains(a.settings.notice, "Name change failed") {
				t.Errorf("%s: notice = %q, want a failure message", code, a.settings.notice)
			}
			if a.statusBar.errorMsg != "" {
				t.Errorf("%s: visible Settings failure should not also write hidden status bar, got %q",
					code, a.statusBar.errorMsg)
			}
			if !a.settings.editing || a.settings.editInput.Value() != "Taken" {
				t.Errorf("%s: edit mode not re-entered with rejected name (editing=%v value=%q)",
					code, a.settings.editing, a.settings.editInput.Value())
			}
			if a.settings.displayName != "OldName" {
				t.Errorf("%s: displayed name = %q, want unchanged OldName (nothing to revert)", code, a.settings.displayName)
			}
			if a.settings.displayNameRenamePending {
				t.Errorf("%s: settings pending mirror should clear before retry edit opens", code)
			}
		})
	}
}

// set_profile has no corr_id today, so only empty-correlation errors can be
// attributed to the pending rename. A correlated error belongs to another verb
// and must not clear the rename marker or show "Name change failed".
func TestRename_CorrelatedErrorWhilePendingDoesNotClearRename(t *testing.T) {
	a := newRenameApp(t)
	a.renameInFlight = true
	a.renameAttempted = "NewName"
	a.settings.SetDisplayNameRenamePending(true)

	a.handleServerMessage(correlatedErrorMsg("rate_limited", "slow down", "corr_ABCDEFGHIJKLMNOPQRSTU"))

	if !a.renameInFlight || a.renameAttempted != "NewName" {
		t.Fatalf("correlated error cleared rename marker: inFlight=%v attempted=%q", a.renameInFlight, a.renameAttempted)
	}
	if a.settings.editing {
		t.Fatal("correlated error should not reopen display-name edit mode")
	}
	if a.settings.noticeIsError {
		t.Fatal("correlated error should not render a rename failure notice")
	}
	if a.statusBar.errorMsg != "slow down" {
		t.Fatalf("correlated error should follow normal error path, got status error %q", a.statusBar.errorMsg)
	}
}

// No marker: an error with no rename in flight must NOT hijack the Settings
// panel — it falls back to the existing status-bar path (guards against avatar
// invalid_profile and any future set_profile caller).
func TestRename_ErrorNoMarkerDoesNotTouchSettings(t *testing.T) {
	a := newRenameApp(t)
	// renameInFlight is false (no rename submitted).

	a.handleServerMessage(errorMsg("username_taken", "Display name is already in use"))

	if a.renameInFlight {
		t.Error("an error with no rename in flight should not set the marker")
	}
	if a.settings.noticeIsError || a.settings.editing {
		t.Error("an error with no rename in flight should not write an error notice or re-enter edit mode")
	}
}

// stuckWriter always fails — used to force a local Enc().Encode error.
type stuckWriter struct{}

func (stuckWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// A local encode failure on submit must NOT set the in-flight marker (which would
// stick Settings on "Saving…" with no result coming) and must surface a visible
// failure. set-profile-corr-id.md dry-run finding: the send path previously
// discarded the Enc().Encode error and set the marker unconditionally.
func TestRename_EncodeFailureDoesNotSetMarker(t *testing.T) {
	a := newRenameApp(t)
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(stuckWriter{}))

	model, _ := a.updateInner(ProfileUpdateMsg{DisplayName: "NewName"})
	na := model.(App)

	if na.renameInFlight || na.renameAttempted != "" {
		t.Fatalf("encode failure must not set the pending marker: inFlight=%v attempted=%q", na.renameInFlight, na.renameAttempted)
	}
	if !na.settings.noticeIsError || !contains(na.settings.notice, "Name change failed") {
		t.Errorf("encode failure should surface an error notice in the visible Settings panel: noticeIsError=%v notice=%q",
			na.settings.noticeIsError, na.settings.notice)
	}
	if na.settings.displayNameRenamePending {
		t.Error("encode failure must not leave the Settings pending mirror set")
	}
}

// A current-server disconnect (ErrMsg) while a rename is pending must clear the
// marker so Settings doesn't stick on "Saving…": the rename's outcome is unknown
// and no self-`profile`/error will arrive on the dead connection. The reconnect
// welcome self-`profile` re-renders the authoritative name.
func TestRename_DisconnectClearsPending(t *testing.T) {
	a := newRenameApp(t)
	a.connected = true // a live connection that then drops -> reconnect branch
	a.renameInFlight = true
	a.renameAttempted = "NewName"
	a.settings.SetDisplayNameRenamePending(true)

	model, _ := a.updateInner(ErrMsg{Err: errors.New("connection lost"), gen: a.connGen})
	na := model.(App)

	if na.renameInFlight || na.renameAttempted != "" {
		t.Fatalf("disconnect must clear the pending rename: inFlight=%v attempted=%q", na.renameInFlight, na.renameAttempted)
	}
	if na.settings.displayNameRenamePending {
		t.Error("disconnect must clear the Settings pending mirror so it doesn't stick on Saving…")
	}
}

// A disconnect with NO rename pending must not touch rename state (guard against
// the clear firing spuriously).
func TestRename_DisconnectNoPendingIsNoop(t *testing.T) {
	a := newRenameApp(t)
	a.connected = true
	// renameInFlight is false.

	model, _ := a.updateInner(ErrMsg{Err: errors.New("connection lost"), gen: a.connGen})
	na := model.(App)

	if na.renameInFlight {
		t.Error("disconnect with no pending rename must not set the marker")
	}
}
