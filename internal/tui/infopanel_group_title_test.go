package tui

import (
	"strings"
	"testing"
)

func TestInfoPanel_ShowGroupUsesDisplayNameInHeader(t *testing.T) {
	a, st := newEditAppHarness(t)
	if err := st.StoreGroup("group_1", "Project Alpha", "usr_alice,usr_bob"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}

	i := InfoPanelModel{}
	i.ShowGroup("group_1", a.client, nil)

	view := i.View(80)
	if !strings.Contains(view, "Project Alpha — info") {
		t.Fatalf("group info header should use display name, got:\n%s", view)
	}
	if strings.Contains(view, "group_1 — info") {
		t.Fatalf("group info header should not show raw group ID when display name exists, got:\n%s", view)
	}
}
