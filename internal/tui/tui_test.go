package tui

import (
	"testing"
)

func TestMessageGrouping(t *testing.T) {
	now := int64(1700000000)
	msgs := []DisplayMessage{
		{ID: "1", From: "alice", Body: "first", TS: now},
		{ID: "2", From: "alice", Body: "second", TS: now + 60},
		{ID: "3", From: "alice", Body: "third", TS: now + 400},
		{ID: "4", From: "bob", Body: "different", TS: now + 401},
		{ID: "5", From: "bob", Body: "also bob", TS: now + 410},
	}

	prevSender := ""
	prevTS := int64(0)
	headers := 0
	for _, m := range msgs {
		showHeader := true
		if m.From == prevSender && m.TS-prevTS < 300 {
			showHeader = false
		}
		if showHeader {
			headers++
		}
		prevSender = m.From
		prevTS = m.TS
	}

	if headers != 3 {
		t.Errorf("expected 3 headers, got %d", headers)
	}
}

func TestMouseLayoutHitTest(t *testing.T) {
	layout := Layout{
		SidebarX0: 0, SidebarX1: 22,
		SidebarY0: 0, SidebarY1: 30,
		SidebarWidth: 20,
		MessagesX0: 22, MessagesX1: 80,
		MessagesY0: 0, MessagesY1: 25,
		MessagesWidth: 56,
		InputX0: 22, InputX1: 80,
		InputY0: 25, InputY1: 30,
		MemberX0: 81, MemberX1: 100,
		MemberY0: 0, MemberY1: 30,
		MemberWidth: 18,
		StatusY: 31,
		Height:  32,
	}

	tests := []struct {
		x, y   int
		expect string
	}{
		{5, 5, "sidebar"},
		{40, 10, "messages"},
		{40, 27, "input"},
		{90, 10, "members"},
		{50, 31, "status"},
		{200, 200, ""},
	}

	for _, tc := range tests {
		got := layout.HitTest(tc.x, tc.y)
		if got != tc.expect {
			t.Errorf("HitTest(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.expect)
		}
	}

	if layout.SidebarItemAt(5) < 0 {
		t.Error("SidebarItemAt(5) should be valid")
	}
	if layout.MessageItemAt(10) < 0 {
		t.Error("MessageItemAt(10) should be valid")
	}
	if layout.MemberItemAt(5) < 0 {
		t.Error("MemberItemAt(5) should be valid")
	}
}

func TestEmojiSearch(t *testing.T) {
	results := SearchEmoji("thumb", 5)
	if len(results) == 0 {
		t.Fatal("no results for 'thumb'")
	}
	found := false
	for _, e := range results {
		if e.Emoji == "👍" {
			found = true
		}
	}
	if !found {
		t.Error("👍 not found")
	}

	// Alias search
	results = SearchEmoji("lol", 5)
	found = false
	for _, e := range results {
		if e.Emoji == "😂" {
			found = true
		}
	}
	if !found {
		t.Error("😂 not found via 'lol'")
	}

	quick := QuickReactions()
	if len(quick) != 8 {
		t.Errorf("quick reactions = %d", len(quick))
	}
}

func TestLinkDetection(t *testing.T) {
	urls := ExtractURLs("check https://example.com and http://test.org/path?q=1")
	if len(urls) != 2 {
		t.Errorf("found %d URLs, want 2", len(urls))
	}

	urls = ExtractURLs("plain text")
	if len(urls) != 0 {
		t.Errorf("found %d URLs in plain text", len(urls))
	}
}

func TestBellConfig(t *testing.T) {
	bell := BellConfig{
		Mode:      "mentions",
		MuteRooms: map[string]bool{"noisy": true},
	}
	muted := map[string]bool{"muted-room": true}

	if !bell.ShouldBell("general", "", "bob", "alice", true, muted) {
		t.Error("should bell for mention")
	}
	if bell.ShouldBell("general", "", "bob", "alice", false, muted) {
		t.Error("should not bell for non-mention")
	}
	if bell.ShouldBell("general", "", "alice", "alice", true, muted) {
		t.Error("should not bell for own message")
	}
	if bell.ShouldBell("noisy", "", "bob", "alice", true, muted) {
		t.Error("should not bell for config-muted room")
	}
	if bell.ShouldBell("muted-room", "", "bob", "alice", true, muted) {
		t.Error("should not bell for app-muted room")
	}

	off := BellConfig{Mode: "off"}
	if off.ShouldBell("general", "", "bob", "alice", true, nil) {
		t.Error("should not bell when off")
	}

	all := BellConfig{Mode: "all", MuteRooms: map[string]bool{}}
	if !all.ShouldBell("general", "", "bob", "alice", false, map[string]bool{}) {
		t.Error("should bell for all mode")
	}

	dms := BellConfig{Mode: "dms", MuteRooms: map[string]bool{}}
	if !dms.ShouldBell("", "conv_1", "bob", "alice", false, map[string]bool{}) {
		t.Error("should bell for DM in dms mode")
	}
	if dms.ShouldBell("general", "", "bob", "alice", false, map[string]bool{}) {
		t.Error("should not bell for room in dms mode")
	}
}

func TestTabCompletion(t *testing.T) {
	members := []MemberEntry{
		{Username: "usr_alice", DisplayName: "alice"},
		{Username: "usr_bob", DisplayName: "bob"},
		{Username: "usr_carol", DisplayName: "carol"},
	}

	comp := Complete("hey @bo", 7, members)
	if comp == nil || len(comp.items) == 0 {
		t.Fatal("no completion for @bo")
	}
	if comp.items[0].Display != "@bob" {
		t.Errorf("first = %q, want @bob", comp.items[0].Display)
	}

	comp = Complete("/re", 3, nil)
	if comp == nil {
		t.Fatal("no completion for /re")
	}
	foundReply := false
	for _, item := range comp.items {
		if item.Display == "/reply" {
			foundReply = true
		}
	}
	if !foundReply {
		t.Error("/reply not found")
	}

	comp = Complete("@", 1, members)
	if comp != nil {
		t.Error("should not complete single @")
	}
}
