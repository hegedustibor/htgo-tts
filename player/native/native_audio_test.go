//go:build audio

package native

// These tests open the sound card, so the CI only compiles them. Run them
// with
//
//	go test -tags audio ./player/native
//
// They play generated silence and make no noise. To hear real speech in
// TestPlay_TenTimesInARow, set HTGOTTS_TEST_MP3 to the path of a 24 kHz MP3,
// the rate the Google Translate and OpenAI engines produce and the rate the
// other tests here open the sound card at.
//
// With oto v3.3.3 on macOS run them without -race: the race detector reports
// a data race inside oto's own sound card initialization (gone in later oto
// versions), not in this package.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPlay_TenTimesInARow(t *testing.T) {
	audio := silentMP3(framesFor(0.5, rate24000), rate24000)
	if path := os.Getenv("HTGOTTS_TEST_MP3"); path != "" {
		var err error
		if audio, err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	for i := 1; i <= 10; i++ {
		if err := p.Play(context.Background(), bytes.NewReader(audio)); err != nil {
			t.Fatalf("play %d of 10: %v", i, err)
		}
	}
}

func TestPlay_StopsWhenTheContextIsDone(t *testing.T) {
	audio := silentMP3(framesFor(3, rate24000), rate24000)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := New().Play(ctx, bytes.NewReader(audio))
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Play returned after %v, want within a second", elapsed)
	}
}

func TestPlay_RejectsAnotherSampleRate(t *testing.T) {
	// Open the sound card at 24 kHz, in case this test runs alone.
	if err := New().Play(context.Background(), bytes.NewReader(silentMP3(2, rate24000))); err != nil {
		t.Fatalf("24 kHz: %v", err)
	}
	err := New().Play(context.Background(), bytes.NewReader(silentMP3(2, rate22050)))
	if !errors.Is(err, ErrSampleRate) {
		t.Fatalf("22.05 kHz after 24 kHz: err = %v, want ErrSampleRate", err)
	}
	for _, hz := range []string{"24000", "22050"} {
		if !strings.Contains(err.Error(), hz) {
			t.Fatalf("err = %q, want it to name %s Hz", err, hz)
		}
	}
}

// framesFor returns how many frames last the given number of seconds at r.
func framesFor(seconds float64, r rate) int {
	return int(seconds*float64(r.hz)/576) + 1
}
