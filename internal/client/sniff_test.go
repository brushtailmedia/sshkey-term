package client

import "testing"

func TestSniffMimeType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPEG", "image/jpeg"},
		{"image.png", "image/png"},
		{"animated.gif", "image/gif"},
		{"modern.webp", "image/webp"},
		{"video.mp4", "video/mp4"},
		{"video.MOV", "video/quicktime"},
		{"clip.webm", "video/webm"},
		{"doc.pdf", "application/pdf"},
		{"archive.zip", "application/zip"},
		{"README.md", "text/plain"},
		{"notes.txt", "text/plain"},
		{"data.json", "application/json"},
		{"page.html", "text/html"},
		{"track.mp3", "audio/mpeg"},
		{"clip.wav", "audio/wav"},
		{"clip.ogg", "audio/ogg"},
		{"unknown.xyz", "application/octet-stream"},
		{"noext", "application/octet-stream"},
		{"/full/path/to/file.png", "image/png"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := sniffMimeType(tc.path)
			if got != tc.want {
				t.Errorf("sniffMimeType(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
