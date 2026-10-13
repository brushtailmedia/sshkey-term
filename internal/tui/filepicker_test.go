package tui

// upload-file-picker Step 4: pure widget tests for FilePickerModel over
// t.TempDir() trees — tree state, filter scope, the scroll invariant, view
// sizing, symlink classification, the stat cap, and the leak defense.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mkPickerTree builds:
//
//	root/
//	  Documents/
//	    invoices/
//	      invoice-2026-08.pdf (182000 B)
//	    notes.md (1 B)
//	  Downloads/            (empty)
//	  photo.jpg (2300 B)
//	  .hidden (1 B)         (excluded, G1)
func mkPickerTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"Documents/invoices", "Downloads"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, size := range map[string]int{
		"photo.jpg":                              2300,
		"Documents/notes.md":                     1,
		"Documents/invoices/invoice-2026-08.pdf": 182000,
		".hidden":                                1,
	} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func openPickerAt(t *testing.T, root string) FilePickerModel {
	t.Helper()
	var m FilePickerModel
	m.SetViewport(80, 30)
	if err := m.Show(FilePickerRequest{Room: "room_x", Title: "Upload to #t", Gen: 7}, root); err != nil {
		t.Fatalf("Show: %v", err)
	}
	return m
}

func rowNames(m FilePickerModel) []string {
	out := make([]string, 0, len(m.rows))
	for _, r := range m.rows {
		switch r.kind {
		case fpRowParent:
			out = append(out, "../")
		case fpRowPlaceholder:
			out = append(out, r.label)
		case fpRowDir:
			out = append(out, r.entry.name+"/")
		default:
			out = append(out, r.entry.name)
		}
	}
	return out
}

func cursorTo(t *testing.T, m *FilePickerModel, name string) {
	t.Helper()
	for i, n := range rowNames(*m) {
		if n == name {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
	t.Fatalf("row %q not found in %v", name, rowNames(*m))
}

func press(m FilePickerModel, k tea.KeyType) (FilePickerModel, tea.Cmd) {
	return m.Update(key(k))
}

func assertRows(t *testing.T, m FilePickerModel, want ...string) {
	t.Helper()
	got := rowNames(m)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestFilePicker_ShowListsRootDirsFirstSorted(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	assertRows(t, m, "../", "Documents/", "Downloads/", "photo.jpg")
	if m.rows[3].entry.size != 2300 {
		t.Fatalf("photo.jpg size = %d, want 2300", m.rows[3].entry.size)
	}
	for _, n := range rowNames(m) {
		if n == ".hidden" {
			t.Fatal("dotfiles must be excluded (G1)")
		}
	}
}

func TestFilePicker_EnterOnDirTogglesAndOnFileEmits(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter)
	assertRows(t, m, "../", "Documents/", "invoices/", "notes.md", "Downloads/", "photo.jpg")
	if m.rows[2].depth != 1 || m.rows[3].depth != 1 {
		t.Fatal("expanded children must be indented one level")
	}
	m, _ = press(m, tea.KeyEnter)
	assertRows(t, m, "../", "Documents/", "Downloads/", "photo.jpg")

	cursorTo(t, &m, "photo.jpg")
	m, cmd := press(m, tea.KeyEnter)
	if m.IsVisible() {
		t.Fatal("Enter on a file must hide the picker (hide-then-emit)")
	}
	if cmd == nil {
		t.Fatal("Enter on a file must emit a cmd")
	}
	sel, ok := cmd().(FilePickerSelectedMsg)
	if !ok {
		t.Fatalf("cmd yielded %T, want FilePickerSelectedMsg", cmd())
	}
	if sel.Request.Room != "room_x" || sel.Request.Gen != 7 || filepath.Base(sel.Path) != "photo.jpg" {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestFilePicker_RightExpandsLeftCollapsesThenJumps(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyRight)
	if !m.expanded[m.rows[1].entry.path] {
		t.Fatal("→ must expand the folder under the cursor")
	}
	cursorTo(t, &m, "notes.md")
	m, _ = press(m, tea.KeyLeft)
	if rowNames(m)[m.cursor] != "Documents/" {
		t.Fatalf("← on a child must jump to its parent row, cursor on %q", rowNames(m)[m.cursor])
	}
	m, _ = press(m, tea.KeyLeft)
	assertRows(t, m, "../", "Documents/", "Downloads/", "photo.jpg")
	cursorTo(t, &m, "photo.jpg")
	before := m.cursor
	m, _ = press(m, tea.KeyLeft)
	if m.cursor != before {
		t.Fatal("← on a top-level file must be a no-op")
	}
}

func TestFilePicker_ParentRowRerootsPreselectsAndPreservesSubtree(t *testing.T) {
	root := mkPickerTree(t)
	m := openPickerAt(t, root)
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter) // expand
	cursorTo(t, &m, "../")
	m, _ = press(m, tea.KeyEnter) // re-root up
	if m.root != filepath.Dir(root) {
		t.Fatalf("root = %q, want parent %q", m.root, filepath.Dir(root))
	}
	if r := m.rows[m.cursor]; r.kind != fpRowDir || r.entry.path != root {
		t.Fatalf("cursor must land on the folder just exited, got %+v", r)
	}
	if !m.expanded[root] {
		t.Fatal("the old root must stay expanded so its subtree nests intact")
	}
	// The previously expanded Documents/ is still open, one level deeper.
	found := false
	for _, r := range m.rows {
		if r.kind == fpRowFile && r.entry.name == "notes.md" && r.depth == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expansion subtree lost on re-root: %v", rowNames(m))
	}
}

func TestFilePicker_NoParentRowAtFilesystemRoot(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	m.root = string(os.PathSeparator)
	m.children[m.root] = nil
	m.rebuild()
	if len(m.rows) == 0 || m.rows[0].kind == fpRowParent {
		t.Fatalf("no ../ row at the filesystem root, rows = %v", rowNames(m))
	}
}

func TestFilePicker_ExpandReadsOnceAndCaches(t *testing.T) {
	root := mkPickerTree(t)
	m := openPickerAt(t, root)
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter)
	if err := os.Remove(filepath.Join(root, "Documents", "notes.md")); err != nil {
		t.Fatal(err)
	}
	m, _ = press(m, tea.KeyEnter) // collapse
	m, _ = press(m, tea.KeyEnter) // expand again: cached listing, no re-read
	assertRows(t, m, "../", "Documents/", "invoices/", "notes.md", "Downloads/", "photo.jpg")

	m.Hide()
	if m.LastRoot() != root {
		t.Fatal("Hide must keep the root for session memory (G2)")
	}
	if err := m.Show(FilePickerRequest{}, root); err != nil {
		t.Fatal(err)
	}
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter)
	assertRows(t, m, "../", "Documents/", "invoices/", "Downloads/", "photo.jpg")
}

func TestFilePicker_FilterLoadedRowsOnlyKeepsAncestors(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter) // expand Documents only
	m, _ = m.Update(runes("inv"))
	assertRows(t, m, "Documents/", "invoices/")

	m, _ = press(m, tea.KeyBackspace)
	m, _ = press(m, tea.KeyBackspace)
	m, _ = press(m, tea.KeyBackspace)
	cursorTo(t, &m, "invoices/")
	m, _ = press(m, tea.KeyRight) // expand invoices
	m, _ = m.Update(runes("2026"))
	assertRows(t, m, "Documents/", "invoices/", "invoice-2026-08.pdf")

	// Collapse invoices: its contents are no longer loaded-visible.
	m.setFilter("")
	cursorTo(t, &m, "invoices/")
	m, _ = press(m, tea.KeyLeft)
	m, _ = m.Update(runes("2026"))
	if len(m.rows) != 0 {
		t.Fatalf("a collapsed folder's contents must not match, rows = %v", rowNames(m))
	}
}

func TestFilePicker_BackspaceNeverAscends(t *testing.T) {
	root := mkPickerTree(t)
	m := openPickerAt(t, root)
	m, _ = m.Update(runes("x"))
	m, _ = press(m, tea.KeyBackspace)
	if m.filter != "" || m.root != root {
		t.Fatalf("filter=%q root=%q after clearing", m.filter, m.root)
	}
	m, _ = press(m, tea.KeyBackspace)
	if m.root != root {
		t.Fatal("Backspace on an empty filter must NEVER re-root")
	}
}

func TestFilePicker_SpaceAppendsToFilter(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	m, _ = m.Update(runes("a"))
	m, _ = press(m, tea.KeySpace)
	if m.filter != "a " {
		t.Fatalf("filter = %q, want %q", m.filter, "a ")
	}
}

func TestFilePicker_PlaceholderRowsEmptyAndDenied(t *testing.T) {
	root := mkPickerTree(t)
	m := openPickerAt(t, root)
	cursorTo(t, &m, "Downloads/")
	m, _ = press(m, tea.KeyEnter)
	assertRows(t, m, "../", "Documents/", "Downloads/", "(empty)", "photo.jpg")
	cursorTo(t, &m, "(empty)")
	before := rowNames(m)
	for _, k := range []tea.KeyType{tea.KeyEnter, tea.KeyRight, tea.KeyLeft} {
		m, _ = press(m, k)
		if strings.Join(rowNames(m), "|") != strings.Join(before, "|") || !m.IsVisible() {
			t.Fatalf("key %v on a placeholder must be inert", k)
		}
	}

	if os.Geteuid() == 0 {
		t.Skip("permission-denied placeholder cannot be exercised as root")
	}
	denied := filepath.Join(root, "Documents", "invoices")
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o755) })
	m = openPickerAt(t, root)
	cursorTo(t, &m, "Documents/")
	m, _ = press(m, tea.KeyEnter)
	cursorTo(t, &m, "invoices/")
	m, _ = press(m, tea.KeyEnter)
	names := rowNames(m)
	if names[3] != "(permission denied)" {
		t.Fatalf("expanding an unreadable folder must show (permission denied) under it, rows = %v", names)
	}
	if names[2] != "invoices/" {
		t.Fatal("the unreadable folder's own row must stay")
	}
}

func TestFilePicker_ScrollInvariant(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(root, "zdir", "sub"+string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var m FilePickerModel
	m.SetViewport(80, filePickerChromeRows+4) // 4 body rows
	if err := m.Show(FilePickerRequest{}, root); err != nil {
		t.Fatal(err)
	}
	if m.bodyRows != 4 {
		t.Fatalf("bodyRows = %d, want 4", m.bodyRows)
	}
	check := func(when string) {
		t.Helper()
		if m.cursor < m.scroll || m.cursor >= m.scroll+m.bodyRows {
			t.Fatalf("%s: cursor %d outside window [%d,%d)", when, m.cursor, m.scroll, m.scroll+m.bodyRows)
		}
		if m.scroll < 0 || (len(m.rows) > m.bodyRows && m.scroll > len(m.rows)-m.bodyRows) {
			t.Fatalf("%s: scroll %d leaves blank rows (len %d)", when, m.scroll, len(m.rows))
		}
	}
	m, _ = press(m, tea.KeyEnd)
	check("End")
	if m.cursor != len(m.rows)-1 {
		t.Fatal("End must move to the last row")
	}
	m, _ = press(m, tea.KeyHome)
	check("Home")
	if m.scroll != 0 {
		t.Fatal("Home must scroll to the top")
	}

	// Expanding a folder keeps the cursor index (and window) stationary.
	// (Directories sort first, so zdir/ sits near the top of the list.)
	cursorTo(t, &m, "zdir/")
	cur, scr := m.cursor, m.scroll
	m, _ = press(m, tea.KeyRight)
	check("expand")
	if m.cursor != cur || m.scroll != scr {
		t.Fatalf("expand must keep the cursor stationary: cursor %d→%d scroll %d→%d", cur, m.cursor, scr, m.scroll)
	}
	// Scroll to the very end (past the expansion), then collapse zdir from
	// a deep scroll position: the window must re-clamp, never blank rows.
	m, _ = press(m, tea.KeyEnd)
	check("End past expansion")
	cursorTo(t, &m, "zdir/")
	m, _ = press(m, tea.KeyLeft)
	check("collapse re-clamp")
	// Parent-jump from a child deep in the list snaps the window to it.
	m, _ = press(m, tea.KeyRight)
	cursorTo(t, &m, "sube.txt")
	m, _ = press(m, tea.KeyLeft)
	check("parent jump")
	if rowNames(m)[m.cursor] != "zdir/" {
		t.Fatalf("← from a child must land on the parent, cursor on %q", rowNames(m)[m.cursor])
	}
	// Shrinking the viewport re-clamps too.
	m, _ = press(m, tea.KeyEnd)
	m.SetViewport(80, filePickerChromeRows+filePickerMinBodyRows)
	check("after shrink")
}

func TestFilePicker_ViewFitsViewport(t *testing.T) {
	root := mkPickerTree(t)
	for _, dims := range [][2]int{{50, 20}, {100, 40}, {60, 25}} {
		var m FilePickerModel
		m.SetViewport(dims[0], dims[1])
		if err := m.Show(FilePickerRequest{Title: "Upload to #general"}, root); err != nil {
			t.Fatal(err)
		}
		cursorTo(t, &m, "Documents/")
		m, _ = press(m, tea.KeyEnter)
		out := m.View()
		if h := lipgloss.Height(out); h > dims[1] {
			t.Fatalf("%dx%d: rendered %d lines > viewport height (filePickerChromeRows is wrong)", dims[0], dims[1], h)
		}
		if w := lipgloss.Width(out); w > dims[0] {
			t.Fatalf("%dx%d: rendered width %d > viewport width", dims[0], dims[1], w)
		}
	}
}

func TestFilePicker_SymlinkDirClassifiedAsDir(t *testing.T) {
	root := mkPickerTree(t)
	if err := os.Symlink(filepath.Join(root, "Documents"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	m := openPickerAt(t, root)
	cursorTo(t, &m, "link/")
	if m.rows[m.cursor].kind != fpRowDir {
		t.Fatal("a symlink to a directory must classify as a directory (G5)")
	}
	m, _ = press(m, tea.KeyRight)
	found := false
	for _, n := range rowNames(m) {
		if n == "notes.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expanding the symlinked dir must list its contents, rows = %v", rowNames(m))
	}
}

func TestFilePicker_StatCapSkipsSizes(t *testing.T) {
	orig := filePickerStatCap
	filePickerStatCap = 1
	t.Cleanup(func() { filePickerStatCap = orig })
	m := openPickerAt(t, mkPickerTree(t))
	cursorTo(t, &m, "photo.jpg")
	if m.rows[m.cursor].entry.size != -1 {
		t.Fatal("above the stat cap, sizes must be unknown (-1)")
	}
	if strings.Contains(m.View(), "(2.") {
		t.Fatal("no size suffix may render above the stat cap")
	}
}

func TestFilePicker_LeakedMouseRunesDropped(t *testing.T) {
	m := openPickerAt(t, mkPickerTree(t))
	// A complete SGR mouse report leaked as runes, in one packed chunk…
	m, _ = m.Update(runes("[<64;10;5M"))
	if m.filter != "" {
		t.Fatalf("packed leaked mouse sequence reached the filter: %q", m.filter)
	}
	// …and split across keystrokes.
	for _, chunk := range []string{"[", "<", "65;1", "0;5", "m"} {
		m, _ = m.Update(runes(chunk))
	}
	if m.filter != "" {
		t.Fatalf("chunked leaked mouse sequence reached the filter: %q", m.filter)
	}
	// Ordinary typing still works afterwards.
	m, _ = m.Update(runes("ph"))
	if m.filter != "ph" {
		t.Fatalf("filter = %q, want %q", m.filter, "ph")
	}
}

func TestFilePicker_TruncatesLongNamesKeepingSize(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("ü", 120) + ".bin"
	if err := os.WriteFile(filepath.Join(root, long), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var m FilePickerModel
	m.SetViewport(filePickerMinContentWidth+8, 30)
	if err := m.Show(FilePickerRequest{}, root); err != nil {
		t.Fatal(err)
	}
	cursorTo(t, &m, long)
	row := m.renderRow(m.rows[m.cursor], m.contentWidth-4)
	if lipgloss.Width(row) > m.contentWidth-4 {
		t.Fatalf("row width %d exceeds text width %d", lipgloss.Width(row), m.contentWidth-4)
	}
	if !strings.Contains(row, "…") || !strings.HasSuffix(row, "(1 B)") {
		t.Fatalf("row must truncate with … and keep the size suffix, got %q", row)
	}
}
