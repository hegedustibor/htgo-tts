package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hajimehoshi/go-mp3"
)

func TestPlay_RejectsNonMP3WithoutOpeningTheSoundCard(t *testing.T) {
	before := openCard
	err := New().Play(context.Background(), strings.NewReader("this is text, not an MP3 file"))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	if openCard != before {
		t.Fatal("the sound card was opened for audio that cannot be played")
	}
}

func TestPlay_FailsFastOnACancelledContext(t *testing.T) {
	before := openCard
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New().Play(ctx, bytes.NewReader(silentMP3(10, rate24000)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if openCard != before {
		t.Fatal("the sound card was opened although the context was already cancelled")
	}
}

// The audio-tagged tests play silentMP3's output; this test keeps that
// fixture honest without a sound card.
func TestSilentMP3_DecodesAtTheRequestedRate(t *testing.T) {
	for _, r := range []rate{rate24000, rate22050} {
		const frames = 42
		decoder, err := mp3.NewDecoder(bytes.NewReader(silentMP3(frames, r)))
		if err != nil {
			t.Fatalf("%d Hz: NewDecoder: %v", r.hz, err)
		}
		if decoder.SampleRate() != r.hz {
			t.Fatalf("%d Hz: SampleRate() = %d", r.hz, decoder.SampleRate())
		}
		n, err := io.Copy(io.Discard, decoder)
		if err != nil {
			t.Fatalf("%d Hz: decode: %v", r.hz, err)
		}
		// 576 samples per frame, two 16-bit channels.
		if want := int64(frames * 576 * 4); n != want {
			t.Fatalf("%d Hz: decoded %d bytes, want %d", r.hz, n, want)
		}
	}
}

// rate is an MPEG-2 sample rate: its index in the frame header and its value.
type rate struct {
	index byte
	hz    int
}

var (
	rate24000 = rate{index: 1, hz: 24000} // what Google Translate and OpenAI produce
	rate22050 = rate{index: 0, hz: 22050}
)

// silentMP3 returns an MP3 of the given number of MPEG-2 Layer III frames,
// 576 samples each, at 8 kbit/s mono. Every frame has all-zero side info and
// main data, which decodes to silence, so the audio tests make no noise.
func silentMP3(frames int, r rate) []byte {
	frameLen := 72 * 8000 / r.hz // an MPEG-2 Layer III frame is 72 * bitrate / sample rate bytes
	out := make([]byte, 0, frames*frameLen)
	for i := 0; i < frames; i++ {
		frame := make([]byte, frameLen)
		frame[0] = 0xFF              // sync
		frame[1] = 0xF3              // sync, MPEG-2, Layer III, no CRC
		frame[2] = 1<<4 | r.index<<2 // 8 kbit/s, sample rate, no padding
		frame[3] = 0xC0              // mono
		out = append(out, frame...)
	}
	return out
}
