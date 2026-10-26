package tui

import "strings"

// renderConfirmBox is the compact yes/no dialog the quit modal set the shape
// of: a header, body lines, a blank line, the "[y] …  [n] Cancel" footer, in
// the rounded purple box, auto-sized and spliced at the centre of the screen
// by the App (overlayCentered). The message-delete confirm renders through
// it too, so the two cannot drift; the wide confirms keep their paragraphs.
func renderConfirmBox(title string, lines []string, footer string) string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\n")
	b.WriteString(footer)
	return dialogStyle.Render(b.String())
}
