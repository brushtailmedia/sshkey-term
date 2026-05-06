package tui

import (
	"strings"
	"testing"
)

func TestBodyWithGutter_IndentsEveryLine(t *testing.T) {
	got := bodyWithGutter("line1\nline2\nline3")
	want := " line1\n line2\n line3"
	if got != want {
		t.Fatalf("bodyWithGutter = %q, want %q", got, want)
	}
}

func TestMessagesBuildContent_MultilineBodyKeepsGutterOnAllLines(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "", "dm_abc")
	m.messages = []DisplayMessage{
		{
			ID:     "msg_1",
			FromID: "usr_alice",
			From:   "alice",
			Body:   "first_one\nfirst_two",
			TS:     1000,
			DM:     "dm_abc",
		},
		{
			ID:     "msg_2",
			FromID: "usr_alice",
			From:   "alice",
			Body:   "second_one\nsecond_two",
			TS:     1001, // consecutive sender/time => header hidden path
			DM:     "dm_abc",
		},
	}

	content, _ := m.buildContent(80)
	out := stripANSI(content)

	if !strings.Contains(out, "\n first_one\n first_two\n") {
		t.Fatalf("first multiline body missing consistent gutter:\n%s", out)
	}
	if !strings.Contains(out, "\n second_one\n second_two\n") {
		t.Fatalf("second multiline body (no header path) missing consistent gutter:\n%s", out)
	}
}
