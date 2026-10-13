package tui

// upload-file-picker Step 3: the router forwards the bare `/upload` form
// (no more silent no-op), `/u` is a full alias canonicalized to the literal
// "/upload" at the router so no later gate can tell them apart, and the
// completion entry lives ONLY in the general list — never in the read-only
// room allow-list (upload is a mutation).

import (
	"strings"
	"testing"
)

func routeUpload(t *testing.T, text string) *SlashCommandMsg {
	t.Helper()
	i := NewInput()
	if !i.handleCommand(text, nil, "room_x", "", "") {
		t.Fatalf("%q must be a recognized verb", text)
	}
	if i.pendingCmd == nil {
		t.Fatalf("%q must forward a SlashCommandMsg (bare forms included)", text)
	}
	return i.pendingCmd
}

func TestRouter_BareUploadForwards(t *testing.T) {
	sc := routeUpload(t, "/upload")
	if sc.Command != "/upload" || sc.Arg != "" || sc.Room != "room_x" {
		t.Fatalf("bare /upload forwarded %+v, want Command=/upload Arg='' Room=room_x", sc)
	}
}

func TestRouter_AliasCanonicalizes(t *testing.T) {
	for _, typed := range []string{"/u", "/U"} {
		sc := routeUpload(t, typed)
		if sc.Command != "/upload" || sc.Arg != "" {
			t.Fatalf("%q forwarded %+v, want the canonical /upload with no arg", typed, sc)
		}
	}
	sc := routeUpload(t, "/u ~/x y")
	if sc.Command != "/upload" || sc.Arg != "~/x y" {
		t.Fatalf("/u with an arg forwarded %+v, want Command=/upload Arg verbatim", sc)
	}
	// App must never see the alias.
	if strings.Contains(sc.Command, "/u ") || sc.Command == "/u" {
		t.Fatal("the alias leaked past the router")
	}
}

func TestCompletion_AliasInGeneralListOnly(t *testing.T) {
	general := completeCommands("/u")
	if general == nil {
		t.Fatal("completeCommands(\"/u\") must offer candidates")
	}
	var haveU, haveUpload bool
	for _, it := range general.items {
		switch it.Text {
		case "/u":
			haveU = true
		case "/upload ":
			haveUpload = true
		}
	}
	if !haveU || !haveUpload {
		t.Fatalf("general completion for /u must list both /u and /upload, got %+v", general.items)
	}

	for _, typed := range []string{"/u", "/upload"} {
		ro := completeReadOnlyRoomCommands(typed, len(typed))
		if ro == nil {
			continue
		}
		for _, it := range ro.items {
			if strings.HasPrefix(it.Text, "/u") {
				t.Fatalf("read-only room completion must never offer %q (upload is a mutation)", it.Text)
			}
		}
	}
}
