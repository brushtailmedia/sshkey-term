package client

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newReplayTestClient(t *testing.T) (*Client, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c := New(Config{Logger: logger})
	c.store = st
	return c, &buf
}

func TestCheckReplay_WarnsForLiveReplay(t *testing.T) {
	c, logBuf := newReplayTestClient(t)

	key := "usr_alice:dev_a:room_general"
	if err := c.store.StoreSeqMark(key, 10); err != nil {
		t.Fatalf("seed seq mark: %v", err)
	}

	c.checkReplay("usr_alice", "dev_a", "room_general", "", 1, true)
	if got := logBuf.String(); !strings.Contains(got, "possible replay detected") {
		t.Fatalf("expected replay warning log, got %q", got)
	}
}

func TestCheckReplay_SuppressesWarnForCatchupReplay(t *testing.T) {
	c, logBuf := newReplayTestClient(t)

	key := "usr_alice:dev_a:room_general"
	if err := c.store.StoreSeqMark(key, 10); err != nil {
		t.Fatalf("seed seq mark: %v", err)
	}

	c.checkReplay("usr_alice", "dev_a", "room_general", "", 1, false)
	if got := logBuf.String(); strings.Contains(got, "possible replay detected") {
		t.Fatalf("expected no replay warning log in catchup mode, got %q", got)
	}
}
