package tui

// Phase 1 regression tests for the sidebar group-presence-dot
// self-leak fix. See presence-dot-self-leak-fix.md.
//
// The bug: groupPresenceDot() excludes self from the presence
// aggregation by filtering m == s.selfUserID, but selfUserID was
// only ever set by the dm_list handler. Between connect and
// dm_list (and forever, if dm_list never arrived or the group was
// solo-self) selfUserID was "" so the filter matched no real
// member and self's own online/status leaked into the group dot.
//
// Phase 1 sets selfUserID at connect time (connectedWithClient
// handler) via the new SidebarModel.SetSelfUserID setter, so the
// self-filter is reliably populated before the first render.
// Phase 1 deliberately does NOT change groupPresenceDot's
// rank-based body (that is Phase 2, gated on a product decision) —
// TestGroupPresenceDot_OtherMemberStillRanked pins that.

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// TestGroupPresenceDot_SelfFilteredWhenSelfUserIDSet pins the bug's
// core symptom: with selfUserID populated, a solo-self group must
// NOT show self's status color — it must render the offline dot,
// because there is no OTHER online member. self.status is set to
// Away to prove self's status does not leak even when non-default.
func TestGroupPresenceDot_SelfFilteredWhenSelfUserIDSet(t *testing.T) {
	s := NewSidebar()
	s.SetSelfUserID("usr_self")
	s.online["usr_self"] = true
	s.status["usr_self"] = StatusAway // self's status must not leak

	dot := s.groupPresenceDot([]string{"usr_self"})
	if dot != offlineDot {
		t.Errorf("solo-self group must show offline dot (self excluded), got %q want %q", dot, offlineDot)
	}
}

// TestGroupPresenceDot_OtherMemberStillRanked is a Phase 1
// regression guard: Phase 1 must NOT change rank semantics. An
// away OTHER member still yields awayDot under the unchanged
// rank-based body. This test is intentionally expected to be
// rewritten in Phase 2 (which removes rank in favor of binary
// online/offline); its presence here documents that Phase 1
// preserves rank behavior.
func TestGroupPresenceDot_OtherMemberStillRanked(t *testing.T) {
	s := NewSidebar()
	s.SetSelfUserID("usr_self")
	s.online["usr_self"] = true
	s.online["usr_other"] = true
	s.status["usr_other"] = StatusAway

	dot := s.groupPresenceDot([]string{"usr_self", "usr_other"})
	if dot != awayDot {
		t.Errorf("Phase 1 must preserve rank semantics: away other-member should yield awayDot, got %q want %q", dot, awayDot)
	}
}

// TestApp_ConnectedWithClient_PopulatesSidebarSelfUserID drives the
// real connectedWithClient handler and asserts the connect-time
// wiring populates sidebar.selfUserID — the actual fix. Pre-fix,
// only the dm_list handler set this; this test would fail (empty
// selfUserID) before the Phase 1 change.
//
// Option 1 (full-handler drive) per the fix plan: the dry-run
// audit confirmed every handler call is safe on a fresh
// client.New() + minimally-constructed App — syncLocalIdentityUI
// and updateTitle are internally guarded, the heavy room-context
// block is gated behind len(Rooms())>0 (Rooms() is nil on a fresh
// client so it is skipped), App.cfg is a value field so
// a.cfg.DataDir=="" is safe, and the waitForMsg Cmd is constructed
// but discarded (never run). No fallback needed.
func TestApp_ConnectedWithClient_PopulatesSidebarSelfUserID(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_presence_dot"})
	client.SetUserIDForTesting(c, "usr_alice")

	// Construct only the models the handler actually exercises.
	// sidebar/messages/search need their New* constructors (nil
	// maps / embedded bubbles otherwise); pinnedBar/statusBar are
	// zero-value-safe (pinnedBar has no constructor by design — see
	// app.go:417 a.pinnedBar = PinnedBarModel{}). newConv/infoPanel
	// are only field-assigned by the handler, safe as zero values.
	a := App{
		client:    c,
		sidebar:   NewSidebar(),
		messages:  NewMessages(),
		search:    NewSearch(),
		pinnedBar: PinnedBarModel{}, // no NewPinnedBar(); zero-value literal per app.go:417
		statusBar: NewStatusBar(),
	}

	msg := connectedWithClient{
		client:           c,
		msgCh:            make(chan ServerMsg, 1),
		errCh:            make(chan error, 1),
		keyWarnCh:        make(chan KeyChangeEvent, 1),
		attachReadyCh:    make(chan AttachmentReadyEvent, 1),
		uploadResultCh:   make(chan UploadResultEvent, 1),
		downloadResultCh: make(chan DownloadResultEvent, 1),
		saveResultCh:     make(chan SaveResultEvent, 1),
		roomUpdatedCh:    make(chan RoomUpdatedEvent, 1),
	}
	model, _ := a.Update(msg)
	updated := model.(App)

	if updated.sidebar.selfUserID != "usr_alice" {
		t.Errorf("after connectedWithClient, sidebar.selfUserID = %q, want %q (pre-fix only dm_list set this, leaving a startup leak window)", updated.sidebar.selfUserID, "usr_alice")
	}
}

// TestPresenceDot_MemberInfoContractUnchanged is a drift guard:
// Phase 1's blast-radius lock forbids any change to the
// package-level PresenceDot (used by memberpanel.go:240 and
// infopanel.go:655/738). These assertions pin its contract.
func TestPresenceDot_MemberInfoContractUnchanged(t *testing.T) {
	if got := PresenceDot(true, StatusAway); got != awayDot {
		t.Fatalf("away dot changed: got %q want %q", got, awayDot)
	}
	if got := PresenceDot(true, StatusBusy); got != busyDot {
		t.Fatalf("busy dot changed: got %q want %q", got, busyDot)
	}
	if got := PresenceDot(false, StatusAvailable); got != offlineDot {
		t.Fatalf("offline dot changed: got %q want %q", got, offlineDot)
	}
	if got := PresenceDot(true, StatusAvailable); got != onlineDot {
		t.Fatalf("online/available dot changed: got %q want %q", got, onlineDot)
	}
}
