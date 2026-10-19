// Package player plays synthesized speech.
//
// A [Player] receives the audio as an [io.Reader] and knows nothing about its
// format: pairing an engine with a player that understands the engine's output
// is the caller's job. This package needs no cgo and offers two kinds of
// player:
//
//   - [Command] runs an external program that reads the audio from its
//     standard input. [MPlayer] and [FFplay] are ready-made commands for
//     mplayer and ffplay, which play every common format.
//   - [Discard] reads the audio without playing it, for tests and for
//     programs that only download or cache speech.
//
// A player that decodes MP3 in-process and plays it through the sound card
// lives in the player/native package, which needs cgo on Linux.
package player

import (
	"context"
	"errors"
	"io"
)

// Player plays audio.
type Player interface {
	// Play plays audio to its end and returns when playback has finished. It
	// returns early with an error wrapping ctx.Err() when ctx is done first.
	//
	// The audio is an opaque byte stream: the player does not know its
	// format, so the caller pairs the player with an engine whose output it
	// understands.
	Play(ctx context.Context, audio io.Reader) error
}

// ErrPlayerNotFound is returned by [Command.Play] when the program is not
// installed, that is, not found in PATH. The error message names the program.
var ErrPlayerNotFound = errors.New("htgotts: player not found")
