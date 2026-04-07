package tui

import "testing"

func TestMarkDeleted_ClearsAttachmentsFromDisplay(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{
			ID: "msg_1", FromID: "usr_a", From: "Alice", Body: "see attached", TS: 1000,
			Attachments: []DisplayAttachment{
				{FileID: "file_abc", Name: "doc.pdf"},
				{FileID: "file_def", Name: "photo.jpg"},
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
