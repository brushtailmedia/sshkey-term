package tui

import "testing"

func TestValidateDisplayName_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Alice", "Alice"},
		{"  Alice  ", "Alice"},
		{"Alice Chen", "Alice Chen"},
		{"José", "José"},
		{"田中太郎", "田中太郎"},
		{"Al", "Al"},
		{"abcdefghijklmnopqrstuvwxyz123456", "abcdefghijklmnopqrstuvwxyz123456"},
	}
	for _, tc := range tests {
		got, err := ValidateDisplayName(tc.input)
		if err != nil {
			t.Errorf("ValidateDisplayName(%q) error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ValidateDisplayName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidateDisplayName_Invalid(t *testing.T) {
	tests := []struct {
		input string
		desc  string
	}{
		{"", "empty"},
		{"   ", "whitespace only"},
		{"A", "too short"},
		{"abcdefghijklmnopqrstuvwxyz1234567", "too long (33 chars)"},
		{"hello\x00world", "null byte"},
		{"hello\nworld", "newline"},
		{"hello\tworld", "tab"},
		{"test\u200Bname", "zero-width space"},
		{"test\u200Dname", "zero-width joiner"},
		{"test\u200Ename", "left-to-right mark"},
		{"test\uFEFFname", "BOM"},
		{"test\u202Aname", "bidi override"},
		{"test\u2060name", "word joiner"},
	}
	for _, tc := range tests {
		_, err := ValidateDisplayName(tc.input)
		if err == nil {
			t.Errorf("ValidateDisplayName(%q) should reject (%s)", tc.input, tc.desc)
		}
	}
}
