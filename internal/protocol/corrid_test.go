package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateCorrID_FormatAndValidation(t *testing.T) {
	id := GenerateCorrID()
	if !strings.HasPrefix(id, "corr_") {
		t.Fatalf("corr_id %q missing prefix", id)
	}
	if len(id) != len("corr_")+21 {
		t.Fatalf("corr_id length = %d, want %d", len(id), len("corr_")+21)
	}
	if err := ValidateCorrID(id); err != nil {
		t.Fatalf("generated corr_id should validate, got %v", err)
	}
}

func TestGenerateCorrID_NonDeterministic(t *testing.T) {
	a := GenerateCorrID()
	b := GenerateCorrID()
	if a == b {
		t.Fatalf("two generated corr_ids matched: %q", a)
	}
}

func TestValidateCorrID_EmptyIsValid(t *testing.T) {
	if err := ValidateCorrID(""); err != nil {
		t.Fatalf("empty corr_id should be valid, got %v", err)
	}
}

func TestValidateCorrID_InvalidShapes(t *testing.T) {
	cases := []struct {
		name string
		id   string
		sent error
	}{
		{name: "wrong_prefix", id: "usr__0123456789ABCDEFGHIJK", sent: ErrInvalidCorrIDPrefix},
		{name: "too_short", id: "corr_abc", sent: ErrInvalidCorrIDLength},
		{name: "too_long", id: "corr_0123456789ABCDEFGHIJK_extra", sent: ErrInvalidCorrIDLength},
		{name: "bad_alphabet_space", id: "corr_01234567890123456789 ", sent: ErrInvalidCorrIDAlphabet},
		{name: "bad_alphabet_bang", id: "corr_!123456789012345678AB", sent: ErrInvalidCorrIDAlphabet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCorrID(tc.id)
			if err == nil {
				t.Fatalf("invalid corr_id %q was accepted", tc.id)
			}
			if !errors.Is(err, tc.sent) {
				t.Fatalf("ValidateCorrID(%q) = %v, want sentinel %v", tc.id, err, tc.sent)
			}
		})
	}
}
