package tui

import (
	"strings"
	"testing"
)

// Trailing prose punctuation is not part of a URL; a paren the URL opened
// is. Duplicates collapse; order is kept.
func TestExtractLinks(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{"plain text", nil},
		{"see https://example.com/a", []string{"https://example.com/a"}},
		{"(see https://example.com/a/b).", []string{"https://example.com/a/b"}},
		{"really? https://example.com/q?x=1!", []string{"https://example.com/q?x=1"}},
		{"it's https://en.wikipedia.org/wiki/Foo_(bar), honest", []string{"https://en.wikipedia.org/wiki/Foo_(bar)"}},
		{"\"https://example.com/quoted\"", []string{"https://example.com/quoted"}},
		{"two: http://a.example/1 and https://b.example/2.", []string{"http://a.example/1", "https://b.example/2"}},
		{"twice https://same.example/x https://same.example/x", []string{"https://same.example/x"}},
	} {
		got := extractLinks(tc.body)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("extractLinks(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// The highlighter underlines exactly the trimmed span: the trailing
// punctuation stays plain text after the link.
func TestHighlightLinks_TrimsTrailingPunctuation(t *testing.T) {
	out := highlightLinks("(see https://example.com/a).")
	if !strings.Contains(out, "\x1b]8;;https://example.com/a\x1b\\") {
		t.Errorf("OSC 8 target must be the trimmed URL:\n%q", out)
	}
	if !strings.HasSuffix(out, ").") {
		t.Errorf("the punctuation must follow the link as plain text:\n%q", out)
	}
}
