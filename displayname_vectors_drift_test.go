package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// findTermRepoRoot walks up from the test's working directory to the directory
// containing go.mod (the sshkey-term checkout root).
func findTermRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod")
		}
		dir = parent
	}
}

// TestDisplayNameVectorsVendorInSync is the sibling-aware drift check for the
// vendored display-name conformance vectors (displayname-validator-
// conformance.md, Option C). The term repo vendors a byte-identical copy of the
// canonical set owned by sshkey-chat. When both repos are checked out as
// siblings, this asserts the vendored copy matches the canonical byte-for-byte;
// when the sibling server checkout is absent (a standalone term clone, or CI
// that builds term alone), it SKIPS cleanly so the term repo stays
// independently buildable.
//
// It deliberately lives at the repo root, not in internal/tui: no ordinary
// package unit test should hard-depend on a sibling-checkout path. The paths are
// derived from the checkout root (go.mod), and the sibling canonical is found
// relative to it.
func TestDisplayNameVectorsVendorInSync(t *testing.T) {
	root := findTermRepoRoot(t)

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
