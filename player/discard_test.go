package player

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDiscard_ReadsTheAudioToItsEnd(t *testing.T) {
	audio := strings.NewReader(strings.Repeat("htgotts ", 100_000))
	if err := Discard.Play(context.Background(), audio); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if audio.Len() != 0 {
		t.Fatalf("%d bytes left unread", audio.Len())
	}
}

func TestDiscard_StopsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The audio cancels the context after its first read, like a stream
	// whose consumer gave up mid-way.
	audio := &cancelling{Reader: strings.NewReader(strings.Repeat("htgotts ", 100_000)), cancel: cancel}
	err := Discard.Play(ctx, audio)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if audio.reads != 1 {
		t.Fatalf("the audio was read %d times, want 1: no reads after the context was cancelled", audio.reads)
	}
}

func TestDiscard_FailsFastOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	audio := strings.NewReader("audio")
	if err := Discard.Play(ctx, audio); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if audio.Len() != len("audio") {
		t.Fatal("the audio was read although the context was already cancelled")
	}
}

func TestDiscard_ReportsReadErrors(t *testing.T) {
	boom := errors.New("boom")
	audio := io.MultiReader(strings.NewReader("audio"), failing{boom})
	if err := Discard.Play(context.Background(), audio); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the audio's error", err)
	}
}

// cancelling cancels a context after its first read.
type cancelling struct {
	io.Reader
	cancel context.CancelFunc
	reads  int
}

func (c *cancelling) Read(p []byte) (int, error) {
	c.reads++
	n, err := c.Reader.Read(p)
	c.cancel()
	return n, err
}

// failing is audio whose every read fails.
type failing struct{ err error }

func (f failing) Read([]byte) (int, error) { return 0, f.err }
