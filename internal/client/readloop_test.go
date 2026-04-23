package client

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

type errReader struct {
	err error
}

func (r errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestReadLoop_EOFCallsOnErrorWhenNotLocallyClosed(t *testing.T) {
	var gotErr error
	c := New(Config{
		OnError: func(err error) {
			gotErr = err
		},
	})
	c.dec = protocol.NewDecoder(bytes.NewBuffer(nil))

	c.readLoop()

	if !errors.Is(gotErr, io.EOF) {
		t.Fatalf("OnError = %v, want io.EOF", gotErr)
	}
}

func TestReadLoop_LocalCloseSuppressesOnError(t *testing.T) {
	called := false
	c := New(Config{
		OnError: func(error) {
			called = true
		},
	})
	c.dec = protocol.NewDecoder(bytes.NewBuffer(nil))
	close(c.done)

	c.readLoop()

	if called {
		t.Fatal("OnError fired during local close path")
	}
}

func TestReadLoop_PropagatesNonEOFDecodeError(t *testing.T) {
	want := errors.New("decode boom")
	var gotErr error
	c := New(Config{
		OnError: func(err error) {
			gotErr = err
		},
	})
	c.dec = protocol.NewDecoder(errReader{err: want})

	c.readLoop()

	if !errors.Is(gotErr, want) {
		t.Fatalf("OnError = %v, want %v", gotErr, want)
	}
}
