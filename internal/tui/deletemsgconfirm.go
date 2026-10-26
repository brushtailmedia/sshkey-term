package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// DeleteMsgConfirmedMsg is the dialog's answer: the user pressed y on the
// message it quoted. The App sends the delete.
type DeleteMsgConfirmedMsg struct {
	Msg DisplayMessage
}

// DeleteMsgConfirmModel asks before a message is deleted: `d` is one letter
// away from every other pane key and a delete is permanent (signed, no
// undo). Built on the quit modal's shape and its rule — only y confirms and
// Enter does nothing, so a reflexive Enter cannot finish what a stray d
// began; n, Esc and q cancel and change nothing (focus-consistency.md
// clause 3). Both the pane's d and the context menu's Delete open it.
type DeleteMsgConfirmModel struct {
	visible bool
	msg     DisplayMessage
	asAdmin bool // another user's message, deleted by room admin right (rooms only; the server has no admin override in groups or DMs)
}

func (d *DeleteMsgConfirmModel) Show(msg DisplayMessage, asAdmin bool) {
	d.visible = true
	d.msg = msg
	d.asAdmin = asAdmin
}

func (d *DeleteMsgConfirmModel) Hide() {
	d.visible = false
}

func (d *DeleteMsgConfirmModel) IsVisible() bool {
	return d.visible
}

func (d DeleteMsgConfirmModel) Update(msg tea.KeyMsg) (DeleteMsgConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y":
		target := d.msg
		d.Hide()
		return d, func() tea.Msg { return DeleteMsgConfirmedMsg{Msg: target} }
	case "n", "esc", "q":
		d.Hide()
	}
	return d, nil
}

// deletePreviewWidth caps the quoted message in the dialog.
const deletePreviewWidth = 60

// View renders the compact prompt: the quoted message, the admin line when
// it is someone else's, and the quit modal's footer shape.
func (d DeleteMsgConfirmModel) View() string {
	if !d.visible {
		return ""
	}
	lines := []string{"  " + deletePreview(d.msg)}
	if d.asAdmin {
		lines = append(lines, "", errorStyle.Render("  Another user's message — deleted by room admin right."))
	}
	return renderConfirmBox(" Delete message?", lines, "  [y] Delete  [n] Cancel")
}

// deletePreview is "Sender: first line of the body", cut to the preview
// width, or the attachment's name for a file-only message — so the dialog
// shows what is about to go.
func deletePreview(m DisplayMessage) string {
	who := m.From
	if who == "" {
		who = m.FromID
	}
	body := strings.TrimSpace(m.Body)
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = strings.TrimSpace(body[:nl])
	}
	if body == "" && len(m.Attachments) > 0 {
		glyph := filePlaceholderGlyph
		if m.Attachments[0].IsImage {
			glyph = imagePlaceholderGlyph
		}
		body = glyph + " " + m.Attachments[0].Name
	}
	return ansi.Truncate(who+": "+body, deletePreviewWidth, "…")
}
