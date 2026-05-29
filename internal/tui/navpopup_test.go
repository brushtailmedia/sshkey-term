package tui

import (
	"strings"
	"testing"
)

// TestNavBindings_AllDispatched is the parity guard: every key the which-key
// popup advertises must actually be dispatched by handleNavModeKey. The "1-9"
// entry is a display range, so it expands to the digits 1..9.
func TestNavBindings_AllDispatched(t *testing.T) {
	var keys []string
	for _, nb := range navBindings {
		if nb.keys == "1-9" {
			for d := '1'; d <= '9'; d++ {
				keys = append(keys, string(d))
			}
			continue
		}
		keys = append(keys, nb.keys)
	}

	for _, k := range keys {
		t.Run("key "+k, func(t *testing.T) {
			a := newNavModeAppHarness(t)
			a.messages.SetContext("", "", "dm_parity") // give `i` a context to open
			_, handled := a.handleNavModeKey(navRune(rune(k[0])))
			if !handled {
				t.Fatalf("navBindings key %q is not dispatched by handleNavModeKey", k)
			}
		})
	}
}

// TestNavPopup_NotShownInModalContext locks decision 6: Ctrl+g still enters nav
// mode while a modal (Add Server) is open — for the server-nav escape keys — but
// the popup never reveals there.
func TestNavPopup_NotShownInModalContext(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.addServer.Show()
	a.navModePopupDelay = 0 // would reveal instantly if allowed
	model, _ := a.Update(navCtrlG())
	updated := model.(App)
	if !updated.navMode {
		t.Fatal("Ctrl+g should still enter nav mode with a modal up (escape-hatch nav)")
	}
	if updated.navPopupVisible {
		t.Fatal("popup must not reveal while a modal (Add Server) is visible")
	}
}

// TestNavPopup_RevealSuppressedIfModalOpens covers the modal-opens-during-delay
// race: the reveal handler re-checks !anyModalVisible() when it fires.
func TestNavPopup_RevealSuppressedIfModalOpens(t *testing.T) {
	a := newNavModeAppHarness(t)
	a = updateNavApp(t, a, navCtrlG())
	gen := a.navModeTickGen
	a.addServer.Show() // a modal opened during the reveal delay
	a = updateNavApp(t, a, navPopupRevealMsg{Gen: gen})
	if a.navPopupVisible {
		t.Fatal("reveal must be suppressed when a modal opened during the delay")
	}
}

// TestNavPopup_MouseClickDismisses locks the click-outside-to-close behavior.
func TestNavPopup_MouseClickDismisses(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.navModePopupDelay = 0
	a = updateNavApp(t, a, navCtrlG()) // popup visible (delay 0, no modal)
	if !a.navPopupVisible {
		t.Fatal("precondition: popup should be visible after Ctrl+g with zero delay")
	}
	model, _ := a.handleMouseClick(0, 0)
	updated := model.(App)
	if updated.navMode || updated.navPopupVisible {
		t.Fatal("a click should dismiss nav mode and the popup")
	}
}

// TestNavPopup_RenderContainsBindings checks the popup lists every binding's
// description, the group titles, the Ctrl+g header, and the cancel footer.
func TestNavPopup_RenderContainsBindings(t *testing.T) {
	out := renderNavPopup()
	for _, nb := range navBindings {
		if !strings.Contains(out, nb.desc) {
			t.Errorf("popup render missing description %q", nb.desc)
		}
	}
	for _, title := range navGroupTitles {
		if !strings.Contains(out, title) {
			t.Errorf("popup render missing group title %q", title)
		}
	}
	if !strings.Contains(out, "Ctrl+g") {
		t.Error("popup render missing the Ctrl+g header")
	}
	if !strings.Contains(out, "cancel") {
		t.Error("popup render missing the cancel footer")
	}
}
