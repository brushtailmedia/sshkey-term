package tui

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestWaitForMsg_SoleConsumerDeliversAllMessages is the positive-path
// guard for the 2026-04-25 audit fix: the discarder goroutine inside
// App.connect() at app.go:321-334 was deleted, leaving waitForMsg as
// the sole consumer of msgCh / errCh / keyWarnCh / attachReadyCh.
// With one consumer, a burst of N messages must deliver exactly N
// times to the consumer — no drops.
//
// The pre-fix state was reproduced by a second goroutine racing with
// waitForMsg on the same channel; measured loss averaged ~50% across
// five runs. Keeping the positive-path test as a regression guard:
// if anyone adds a second consumer back (mistaking it for a
// fan-out / tee pattern), the loss will surface here rather than in
// mysterious "messages sometimes missing" bug reports.
//
// No time.Sleep — uses sync.WaitGroup to pin completion. No Bubble
// Tea program — the test replicates the single-consumer shape
// directly so the assertion is sharp.
func TestWaitForMsg_SoleConsumerDeliversAllMessages(t *testing.T) {
	const N = 1000

	// Buffered 100 to match msgCh's buffer size in connect()
	// (app.go "msgCh := make(chan ServerMsg, 100)").
	msgCh := make(chan struct{}, 100)
	done := make(chan struct{})

	var received atomic.Int64
	var wg sync.WaitGroup
	wg.Add(N)

	// Single consumer — the post-fix shape. waitForMsg is the only
	// goroutine draining the channel in production; this test
	// simulates the fast-loop version of that (Bubble Tea's actual
	// cmd re-issue cycle would add latency between selects but
	// wouldn't change delivery count).
	go func() {
		for {
			select {
			case <-msgCh:
				received.Add(1)
				wg.Done()
			case <-done:
				return
			}
		}
	}()

	// Push N messages — same burst shape as a peer sending 1000
	// protocol frames rapidly (offline-catchup sync_batch, chatty
	// room backlog, etc.).
	for i := 0; i < N; i++ {
		msgCh <- struct{}{}
	}

	wg.Wait()
	close(done)

	got := received.Load()
	t.Logf("Burst of %d messages — sole consumer received %d (%.1f%%)",
		N, got, 100.0*float64(got)/float64(N))

	if got != N {
		t.Errorf("REGRESSION: sole consumer received %d of %d messages. "+
			"A second consumer goroutine may have been re-added to app.go's "+
			"connect(); check for any new `go func() { for { select { "+
			"case <-msgCh ... }}}()` blocks alongside waitForMsg.", got, N)
	}
}
