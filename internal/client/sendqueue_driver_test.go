package client

// Phase 17c Step 5 Gap 2/3/4 — send-queue driver tests.
//
// Covers:
//   - backoffForAttempts: 200ms, 400ms, 800ms, 1.6s, 3.2s, capped 5s
//   - isRetriableCategoryA / isCategoryBInvalidEpoch discrimination
//   - PendingForCategoryARetry respects the backoff window
//   - PendingForCategoryBRoomRetry filters by room
//   - Queue.SweepTimeouts + Drop integration

import (
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestBackoffForAttempts_ExponentialWithCap(t *testing.T) {
	cases := []struct {
		attempts int
		wantMin  time.Duration
		wantMax  time.Duration
	}{
		{0, 0, 250 * time.Millisecond},           // floor
		{1, 150 * time.Millisecond, 250 * time.Millisecond},
		{2, 350 * time.Millisecond, 450 * time.Millisecond},
		{3, 750 * time.Millisecond, 850 * time.Millisecond},
		{4, 1500 * time.Millisecond, 1700 * time.Millisecond},
		{5, 3000 * time.Millisecond, 3300 * time.Millisecond},
		{6, 4999 * time.Millisecond, 5001 * time.Millisecond}, // capped at 5s
		{10, 4999 * time.Millisecond, 5001 * time.Millisecond}, // still capped
	}
	for _, tc := range cases {
		got := backoffForAttempts(tc.attempts)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("backoffForAttempts(%d) = %v, want in [%v, %v]",
				tc.attempts, got, tc.wantMin, tc.wantMax)
		}
	}
}

func TestEntry_IsRetriableCategoryA(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"rate_limited", true},
		{"internal_error", true},
		{"server_busy", true},
		{"invalid_epoch", false},
		{"denied", false},
		{"message_too_large", false},
		{"", false}, // nil-error case handled by parent
	}
	for _, tc := range cases {
		e := &Entry{LastError: &protocol.Error{Code: tc.code}}
		if got := e.isRetriableCategoryA(); got != tc.want {
			t.Errorf("code=%q isRetriableCategoryA = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestEntry_IsRetriableCategoryA_NilErrorFalse(t *testing.T) {
	e := &Entry{LastError: nil}
	if e.isRetriableCategoryA() {
		t.Error("nil LastError classified as retriable")
	}
}

func TestEntry_IsCategoryBInvalidEpoch(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"invalid_epoch", true},
		{"epoch_conflict", true},
		{"stale_member_list", true},
		{"rate_limited", false},
		{"denied", false},
	}
	for _, tc := range cases {
		e := &Entry{LastError: &protocol.Error{Code: tc.code}}
		if got := e.isCategoryBInvalidEpoch(); got != tc.want {
			t.Errorf("code=%q isCategoryBInvalidEpoch = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestPendingForCategoryARetry_WaitsForBackoff(t *testing.T) {
	q := NewQueue()
	// Set up an entry that just errored: Attempts=1, LastSentAt=now.
	corrID := protocol.GenerateCorrID()
	q.EnqueueWithID(corrID, "send", protocol.Send{})
	q.MarkSending(corrID)
	q.Error(corrID, &protocol.Error{Code: "rate_limited"})

	// Immediately: backoff not elapsed, nothing eligible.
	now := time.Now()
	if got := q.PendingForCategoryARetry(now); len(got) != 0 {
		t.Errorf("immediate retry eligibility = %d entries, want 0 (backoff not yet elapsed)", len(got))
	}

	// After backoff window (Attempts=1 → ~200ms), eligibility holds.
	now = now.Add(500 * time.Millisecond)
	if got := q.PendingForCategoryARetry(now); len(got) != 1 {
		t.Errorf("post-backoff eligibility = %d, want 1", len(got))
	}
}

func TestPendingForCategoryARetry_NonCategoryAIgnored(t *testing.T) {
	q := NewQueue()
	corrID := protocol.GenerateCorrID()
	q.EnqueueWithID(corrID, "send", protocol.Send{})
	q.MarkSending(corrID)
	q.Error(corrID, &protocol.Error{Code: "invalid_epoch"}) // Category B

	// Even after a long window, Category B entries aren't picked up
	// by the A-retry path — they wait for the explicit Category B
	// state-fix trigger.
	now := time.Now().Add(10 * time.Second)
	if got := q.PendingForCategoryARetry(now); len(got) != 0 {
		t.Errorf("Category B entry eligible for A retry: %d entries", len(got))
	}
}

func TestPendingForCategoryBRoomRetry_FiltersByRoom(t *testing.T) {
	q := NewQueue()
	// Entry for room_A with invalid_epoch.
	corrA := protocol.GenerateCorrID()
	q.EnqueueWithID(corrA, "send", protocol.Send{Room: "room_a"})
	q.MarkSending(corrA)
	q.Error(corrA, &protocol.Error{Code: "invalid_epoch"})
	// Entry for room_B with invalid_epoch.
	corrB := protocol.GenerateCorrID()
	q.EnqueueWithID(corrB, "send", protocol.Send{Room: "room_b"})
	q.MarkSending(corrB)
	q.Error(corrB, &protocol.Error{Code: "invalid_epoch"})

	// Fresh epoch_key for room_a retries only the room_a entry.
	got := q.PendingForCategoryBRoomRetry("room_a", time.Now())
	if len(got) != 1 {
		t.Fatalf("room_a retry eligibility = %d, want 1", len(got))
	}
	if got[0].CorrID != corrA {
		t.Errorf("retried %q, want %q", got[0].CorrID, corrA)
	}
}

func TestPendingForCategoryBRoomRetry_CategoryAIgnored(t *testing.T) {
	q := NewQueue()
	corrID := protocol.GenerateCorrID()
	q.EnqueueWithID(corrID, "send", protocol.Send{Room: "room_x"})
	q.MarkSending(corrID)
	q.Error(corrID, &protocol.Error{Code: "rate_limited"})

	// Category A entry, not Category B — should not match the B-retry.
	if got := q.PendingForCategoryBRoomRetry("room_x", time.Now()); len(got) != 0 {
		t.Errorf("Category A entry returned by B-retry: %d entries", len(got))
	}
}

func TestRoomFromKnownPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
	}{
		{"Send", protocol.Send{Room: "room_1"}, "room_1"},
		{"Edit", protocol.Edit{Room: "room_2"}, "room_2"},
		{"React", protocol.React{Room: "room_3"}, "room_3"},
		{"Pin", protocol.Pin{Room: "room_4"}, "room_4"},
		{"Unpin", protocol.Unpin{Room: "room_5"}, "room_5"},
		{"History", protocol.History{Room: "room_6"}, "room_6"},
		{"RoomMembers", protocol.RoomMembers{Room: "room_7"}, "room_7"},
		{"UploadStart", protocol.UploadStart{Room: "room_8"}, "room_8"},
		{"SendGroup (no room)", protocol.SendGroup{Group: "group_1"}, ""},
		{"unknown type", struct{}{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := roomFromKnownPayload(tc.payload); got != tc.want {
				t.Errorf("roomFromKnownPayload = %q, want %q", got, tc.want)
			}
		})
	}
}
