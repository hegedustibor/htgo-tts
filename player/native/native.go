// Package native plays MP3 speech through the sound card in-process, with the
// oto library and the go-mp3 decoder, so no external player is needed.
//
// It has requirements that the player package does not:
//
//   - Building needs no cgo on any platform: oto loads the sound library at
//     run time through purego. On Linux that library is ALSA, so the machine
//     that runs the program needs the libasound shared library (the
//     libasound2 package on Debian and Ubuntu; the -dev headers are not
//     needed). macOS and Windows need nothing extra.
//   - Only MP3 is played; other formats fail with [ErrUnsupportedFormat].
//     The Google Translate and OpenAI engines produce MP3.
//   - The sound card is opened once per process, at the sample rate of the
//     first MP3 played, because oto supports a single context per process.
//     A later MP3 with another sample rate fails with [ErrSampleRate]. Google
//     Translate and OpenAI both produce 24 kHz MP3, so mixing them is fine.
//
// Playing many MP3s in a row in one process works; every Play reuses the open
// sound card.
package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
)

var (
	// ErrUnsupportedFormat is returned when the audio is not MP3.
	ErrUnsupportedFormat = errors.New("htgotts: native player plays MP3 only")

	// ErrSampleRate is returned when the audio's sample rate differs from
	// the one the sound card was opened with by the first Play in this
	// process. The error message names both rates.
	ErrSampleRate = errors.New("htgotts: native player: sample rate differs from the first MP3 played")
)

// Player plays MP3 audio through the sound card. It holds no state of its
// own, as the sound card is shared by every Player in the process, so the
// zero value is ready to use.
type Player struct{}

// New returns a Player.
func New() *Player { return &Player{} }

// pollInterval is how often Play checks whether playback has ended.
const pollInterval = 10 * time.Millisecond

// Play decodes the MP3 audio and plays it, returning when playback ends.
// When ctx is done first, playback stops and Play returns an error wrapping
// ctx.Err(). Audio that is not MP3 fails with an error wrapping
// [ErrUnsupportedFormat] before the sound card is touched; an MP3 whose
// sample rate differs from the first one played fails with [ErrSampleRate].
func (p *Player) Play(ctx context.Context, audio io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("htgotts: native player: %w", err)
	}
	decoder, err := mp3.NewDecoder(audio)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	card, err := soundCard(decoder.SampleRate())
	if err != nil {
		return err
	}

	player := card.NewPlayer(decoder)
	player.Play()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for player.IsPlaying() {
		select {
		case <-ctx.Done():
			player.Pause()
			return fmt.Errorf("htgotts: native player: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	// Err reports an error that stopped the decoding mid-way. Since oto 3.4
	// a player needs no Close; it is released with the garbage collector.
	if err := player.Err(); err != nil {
		return fmt.Errorf("htgotts: native player: %w", err)
	}
	return nil
}

var (
	openOnce sync.Once
	openCard *oto.Context
	openRate int
	openErr  error
)

// soundCard returns the process-wide oto context, opening the sound card on
// the first call at the given sample rate. oto supports one context per
// process, so a failure to open it is final, and a later call with another
// sample rate fails with ErrSampleRate.
func soundCard(sampleRate int) (*oto.Context, error) {
	openOnce.Do(func() {
		card, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: 2, // go-mp3 always decodes to stereo
			Format:       oto.FormatSignedInt16LE,
		})
		if err == nil {
			// oto v3.3.3's macOS driver sets its ready channel to nil from the
			// initialization goroutine right after closing it, without
			// synchronization, so the copy handed to us can already be nil;
			// nil means that initialization has finished.
			if ready != nil {
				<-ready
			}
			err = card.Err()
		}
		if err != nil {
			openErr = fmt.Errorf("htgotts: native player: open sound card: %w", err)
			return
		}
		openCard, openRate = card, sampleRate
	})
	if openErr != nil {
		return nil, openErr
	}
	if sampleRate != openRate {
		return nil, fmt.Errorf("%w: sound card opened at %d Hz, this MP3 is %d Hz", ErrSampleRate, openRate, sampleRate)
	}
	return openCard, nil
}
