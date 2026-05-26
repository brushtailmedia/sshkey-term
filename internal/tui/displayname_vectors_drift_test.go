package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDisplayNameVectorsVendorInSync is the sibling-aware drift check for the
// vendored display-name conformance vectors (displayname-validator-
// conformance.md, Option C). The term repo vendors a byte-identical copy of the
// canonical set owned by sshkey-chat. When both repos are checked out as
// siblings, this asserts the vendored copy matches the canonical byte-for-byte;
// when the sibling server checkout is absent (a standalone term clone, or CI
// that builds term alone), it SKIPS cleanly so the term repo stays
// independently buildable.
//
// It lives in package tui — alongside the loader test
// (displayname_vectors_test.go) whose testdata it backstops — following the same
// in-package guard-test convention as request_room_members_guard_test.go and
// internal/config/path_drift_test.go. The repo root is discovered via the shared
// repoRootForGuardTest go.mod walk, so the sibling canonical resolves relative to
// the checkout root regardless of the package's test working directory.
func TestDisplayNameVectorsVendorInSync(t *testing.T) {
	root := repoRootForGuardTest(t)

	vendored := filepath.Join(root, "internal", "tui", "testdata", "displayname_vectors.json")
	canonical := filepath.Join(root, "..", "sshkey-chat", "internal", "config", "testdata", "displayname_vectors.json")

	canon, err := os.ReadFile(canonical)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("sibling sshkey-chat checkout not present (%s) — vendored-copy drift check skipped", canonical)
		}
		t.Fatalf("read canonical vectors: %v", err)
	}
	vend, err := os.ReadFile(vendored)
	if err != nil {
		t.Fatalf("read vendored vectors: %v", err)
	}
	if !bytes.Equal(canon, vend) {
		t.Errorf("vendored display-name vectors are STALE vs the canonical sshkey-chat copy — re-vendor.\n  canonical: %s\n  vendored:  %s\n  fix: cp %q %q", canonical, vendored, canonical, vendored)
	}
}
