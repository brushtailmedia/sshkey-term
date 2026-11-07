package config

import (
	"os"
	"testing"
)

// TestLoad_ImagesDefaults: a config without an [images] section, or with
// non-positive values, gets the decode budget and memory-limit defaults;
// explicit values survive; a percentage over 100 is clamped. Runs for a
// missing file and for a written one, since both paths apply defaults.
func TestLoad_ImagesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load (no file): %v", err)
	}
	if cfg.Images.MaxDecodeBytes != DefaultMaxDecodeBytes || cfg.Images.MemoryLimitPercent != DefaultMemoryLimitPercent {
		t.Fatalf("no file: images = %+v, want defaults", cfg.Images)
	}

	cases := []struct {
		name            string
		toml            string
		wantBytes       int64
		wantPercentMemo int
	}{
		{"section absent", "[device]\nid = \"dev_x\"\n", DefaultMaxDecodeBytes, DefaultMemoryLimitPercent},
		{"zeros", "[images]\nmax_decode_bytes = 0\nmemory_limit_percent = 0\n", DefaultMaxDecodeBytes, DefaultMemoryLimitPercent},
		{"negatives", "[images]\nmax_decode_bytes = -1\nmemory_limit_percent = -7\n", DefaultMaxDecodeBytes, DefaultMemoryLimitPercent},
		{"explicit", "[images]\nmax_decode_bytes = 1048576\nmemory_limit_percent = 40\n", 1 << 20, 40},
		{"clamped", "[images]\nmemory_limit_percent = 300\n", DefaultMaxDecodeBytes, 100},
	}
	for _, c := range cases {
		d := t.TempDir()
		if err := os.WriteFile(ConfigFilePath(d), []byte(c.toml), 0o600); err != nil {
			t.Fatalf("%s: write: %v", c.name, err)
		}
		got, err := Load(d)
		if err != nil {
			t.Fatalf("%s: load: %v", c.name, err)
		}
		if got.Images.MaxDecodeBytes != c.wantBytes || got.Images.MemoryLimitPercent != c.wantPercentMemo {
			t.Errorf("%s: images = %+v, want {%d %d}", c.name, got.Images, c.wantBytes, c.wantPercentMemo)
		}
	}
}
