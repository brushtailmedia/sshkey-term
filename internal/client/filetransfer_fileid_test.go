package client

import "testing"

// TestValidFileID guards the audit-F12 fix: the download cache write uses
// a server-relayed fileID as the local filename, so validFileID must reject
// anything that is not exactly the shape the server mints ("file_" + 21
// nanoid characters over [A-Za-z0-9_-]) — otherwise a malicious server
// could path-traverse / absolute-path-write decrypted bytes outside
// filesDir, or (attachments v2 PLAN step 3b) mint a dotted id that names a
// neighbour's `.part` / `.lthumb` / thumbnail. The canonical shape test
// lives in config (`TestValidFileID` there); this one pins that the client
// wrapper applies the same rule.
func TestValidFileID(t *testing.T) {
	valid := []string{
		"file_V1StGXR8_Z5jdHi6B-myT", // server format: file_ + 21-char nanoid (incl. _ and -)
		"file_AAAAAAAAAAAAAAAAAAAAA",
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
		"file_\x00evil",                   // embedded NUL
		"file_abc123",                     // short
		"safename",                        // no prefix
		"a-b_c.d",                         // dotted, no prefix
		"file_V1StGXR8_Z5jdHi6B-myT.part", // a derived-file name
	}
	for _, id := range invalid {
		if validFileID(id) {
			t.Errorf("validFileID(%q) = true, want false", id)
		}
	}
}
