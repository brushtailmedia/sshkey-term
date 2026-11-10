package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/testutil"
)

func TestMarkDeleted_ClearsAttachmentsFromDisplay(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{
			ID: "msg_1", FromID: "usr_a", From: "Alice", Body: "see attached", TS: 1000,
			Attachments: []DisplayAttachment{
				{FileID: testutil.FileID("abc"), Name: "doc.pdf"},
				{FileID: testutil.FileID("def"), Name: "photo.jpg"},
			},
		},
	}

	m.MarkDeleted("msg_1", "usr_a")

	if m.messages[0].Attachments != nil {
		t.Error("attachments should be nil after mark deleted")
	}
	if !m.messages[0].Deleted {
		t.Error("should be marked deleted")
	}
}
