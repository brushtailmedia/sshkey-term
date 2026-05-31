package client

import "testing"

// TestValidDownloadFileID guards the audit-F12 fix: the download cache write
// uses a server-relayed fileID as the local filename, so validFileID
// must reject anything that isn't a safe single path component (otherwise a
// malicious server could path-traverse / absolute-path-write decrypted bytes
// outside filesDir). Legitimate server fileIDs ("file_" + nanoid over
// [A-Za-z0-9_-]) must still pass.
func TestValidFileID(t *testing.T) {
	valid := []string{
		"file_V1StGXR8_Z5jdHi6B-myT", // server format: file_ + 21-char nanoid (incl. _ and -)
		"file_abc123",
		"safename",
		"a-b_c.d", // a dot INSIDE a name is fine — only "." / ".." are rejected
	}
	for _, id := range valid {
		if !validFileID(id) {
			t.Errorf("validFileID(%q) = false, want true (legitimate id)", id)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"../etc/passwd",
		"../../../../etc/cron.d/evil",
		"/etc/passwd",
		"/absolute/path",
		"sub/dir",
		"foo/",            // trailing separator
		"dir\\win",        // backslash (cross-platform defense-in-depth)
		"..\\..\\windows", // windows-style traversal
		"a/../../b",
		"file_\x00evil", // embedded NUL
	}
	for _, id := range invalid {
		if validFileID(id) {
			t.Errorf("validFileID(%q) = true, want false (path-traversal risk)", id)
		}
	}
}
