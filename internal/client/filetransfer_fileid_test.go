package client

import (
	"testing"
)

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
		"",                                // malformed: empty ID has no required prefix or suffix
		".",                               // malformed: dot path is not a file ID
		"..",                              // malformed: dot path is not a file ID
		"../etc/passwd",                   // malformed: path separators or traversal are not permitted in a file ID
		"../../../../etc/cron.d/evil",     // malformed: path separators or traversal are not permitted in a file ID
		"/etc/passwd",                     // malformed: absolute filesystem path is not a file ID
		"/absolute/path",                  // malformed: absolute filesystem path is not a file ID
		"sub/dir",                         // malformed: path separators or traversal are not permitted in a file ID
		"foo/",                            // malformed: trailing separator
		"dir\\win",                        // malformed: backslash (cross-platform defense-in-depth)
		"..\\..\\windows",                 // malformed: windows-style traversal
		"a/../../b",                       // malformed: path separators or traversal are not permitted in a file ID
		"file_\x00evil",                   // malformed: embedded NUL
		"file_abc123",                     // malformed: short
		"safename",                        // malformed: no prefix
		"a-b_c.d",                         // malformed: dotted, no prefix
		"file_V1StGXR8_Z5jdHi6B-myT.part", // malformed: a derived-file name
	}
	for _, id := range invalid {
		if validFileID(id) {
			t.Errorf("validFileID(%q) = true, want false", id)
		}
	}
}
