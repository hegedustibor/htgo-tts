// Package htgotts turns text into speech: it asks a text-to-speech engine for
// the audio of a text and plays it, saves it to a file or hands it over as a
// stream or as bytes in memory.
//
// # Engines
//
// The engine does the synthesis and is the first argument of [New]. The
// module ships two, in subpackages:
//
//   - engine/google speaks through the unofficial Google Translate endpoint:
//     free, without an API key, one language per engine, as htgotts v1 did.
//   - engine/openai speaks through the OpenAI speech API, or any
//     OpenAI-compatible endpoint, with a choice of model, voice, speed and
//     format; it needs an API key, and every request is billed.
//
// Any type that implements [engine.Engine], an interface of three methods,
// works in their place, so a program switches engines by changing the one
// line that creates its engine. Every engine of the module needs network
// access.
//
// # Players
//
// [Speech.Speak], [Speech.Play] and [Speech.PlayFile] play audio through a
// [player.Player]. The default is [player.MPlayer], which runs mplayer, so
// mplayer must be installed; [WithPlayer] replaces it with [player.FFplay],
// another [player.Command] that plays audio from its standard input, with
// the native player of the player/native package, which plays MP3 through the
// sound card without an external program, or with any other implementation of
// the one-method interface. [player.Discard] plays nothing and serves tests
// and programs that only download speech.
//
// # Cache
//
// Without [WithFolder] no file is written: the audio streams from the engine
// to the player, or to the caller through [Speech.Open], [Speech.Synthesize]
// and [Speech.SynthesizeTo]. With [WithFolder], the audio of every text is
// kept in that folder under the name <fingerprint>_<md5 of the text>.<format>,
// where the fingerprint is the engine's, and a text spoken before is served
// from its file instead of the engine. A file appears under its final name
// only once its audio has been read to the end, so an interrupted download or
// playback leaves nothing behind. The Google engine's files carry the names
// that htgotts v1 wrote, so a folder filled by v1 keeps serving hits.
//
// # Usage
//
//	tts, err := htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"))
//	if err != nil {
//		log.Fatal(err)
//	}
//	if err := tts.Speak(context.Background(), "Hello, world!"); err != nil {
//		log.Fatal(err)
//	}
//
// Every network and playback call takes a [context.Context] and stops when it
// is done. Errors are wrapped, so errors.Is and errors.As work on them: the
// errors of the engine, among them [engine.ErrEmptyText], *[engine.HTTPError]
// and the error of a done context, and the errors of the player, among them
// [player.ErrPlayerNotFound], come through unchanged. The package and the
// engines and players of the module build without cgo.
package htgotts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/internal/cache"
	"github.com/hegedustibor/htgo-tts/v2/player"
)

var (
	// ErrNilEngine is returned by New when the engine is nil.
	ErrNilEngine = errors.New("htgotts: nil engine")

	// ErrNilPlayer is returned by New when WithPlayer is given a nil player.
	ErrNilPlayer = errors.New("htgotts: nil player")

	// ErrEmptyFolder is returned by New when WithFolder is given an empty
	// folder name.
	ErrEmptyFolder = errors.New("htgotts: empty folder")

	// ErrEmptyPath is returned by Save when the path is empty.
	ErrEmptyPath = errors.New("htgotts: empty path")
)

// Speech synthesizes text with an engine and plays the audio with a player,
// keeping the audio in a folder when one is given. Create it with [New]. A
// Speech is safe for concurrent use when its engine and player are.
type Speech struct {
	engine engine.Engine
	player player.Player
	cache  *cache.Store // nil without WithFolder: nothing is written then
}

// Option configures the Speech that [New] creates.
type Option func(*Speech) error

// New returns a Speech that synthesizes with e and, unless [WithPlayer] says
// otherwise, plays through mplayer. A nil engine fails with [ErrNilEngine].
// The options are applied in order, and the first invalid one makes New fail
// with its error: [ErrNilPlayer] or [ErrEmptyFolder]. New touches neither the
// network nor the file system; the cache folder is created when the first
// audio is written to it.
func New(e engine.Engine, opts ...Option) (*Speech, error) {
	if e == nil {
		return nil, ErrNilEngine
	}
	s := &Speech{engine: e, player: player.MPlayer()}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// WithFolder keeps the audio of every text in the folder dir and serves a
// text spoken before from its file, so that the engine is asked for it only
// once. The folder and its parents are created when the first audio is
// written. An empty dir fails New with [ErrEmptyFolder].
func WithFolder(dir string) Option {
	return func(s *Speech) error {
		if dir == "" {
			return ErrEmptyFolder
		}
		s.cache = &cache.Store{Dir: dir}
		return nil
	}
}

// WithPlayer plays the audio with p instead of mplayer. The player must
// understand the engine's format: mplayer and ffplay play every format the
// engines produce, the native player plays MP3 only. A nil player fails New
// with [ErrNilPlayer].
func WithPlayer(p player.Player) Option {
	return func(s *Speech) error {
		if p == nil {
			return ErrNilPlayer
		}
		s.player = p
		return nil
	}
}

// Open returns the audio of text as a stream, in the engine's format, to be
// read and then closed by the caller. Without a cache folder the stream comes
// straight from the engine, so its first bytes are available before the whole
// text has been synthesized. With one, a text spoken before is read from its
// file, and a new text is written to the folder while the stream is read; the
// file is kept only when the stream has been read to io.EOF before Close.
//
// The errors of the engine are returned unchanged: [engine.ErrEmptyText] for
// empty text, *[engine.HTTPError] for an unexpected answer of the speech
// service, and an error wrapping ctx.Err() when ctx is done.
func (s *Speech) Open(ctx context.Context, text string) (io.ReadCloser, error) {
	if s.cache == nil {
		return s.engine.Synthesize(ctx, text)
	}
	return s.cache.Open(ctx, s.engine.Fingerprint(), text, s.engine.Format(), func(ctx context.Context) (io.ReadCloser, error) {
		return s.engine.Synthesize(ctx, text)
	})
}

// Speak synthesizes text, through the cache when there is one, and plays it,
// returning when playback has finished or when ctx is done. The player's
// error comes first; when playback succeeds but closing the audio fails, that
// error is returned. The audio is cached only when the player has read it to
// the end, so an interrupted playback leaves no file behind.
func (s *Speech) Speak(ctx context.Context, text string) error {
	return s.withAudio(ctx, text, func(audio io.Reader) error {
		return s.player.Play(ctx, audio)
	})
}

// Synthesize returns the audio of text, in the engine's format, in memory.
// Without a cache folder it writes no file.
func (s *Speech) Synthesize(ctx context.Context, text string) ([]byte, error) {
	var buf bytes.Buffer
	if err := s.SynthesizeTo(ctx, text, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SynthesizeTo writes the audio of text, in the engine's format, to w.
// Without a cache folder it writes no file.
func (s *Speech) SynthesizeTo(ctx context.Context, text string, w io.Writer) error {
	return s.withAudio(ctx, text, func(audio io.Reader) error {
		_, err := io.Copy(w, audio)
		return err
	})
}

// Save writes the audio of text to the file at path and returns the path it
// wrote. When path has no extension, the one of the engine's format is
// appended, so "out/hello" becomes "out/hello.mp3" with an MP3 engine. The
// parent directory is created when needed, and the file is written
// atomically: it appears under its name only once the audio is complete, and
// a failed download leaves a file that was there as it was. With a cache
// folder, the audio of a text spoken before comes from the cache, and a new
// text is cached as well. An empty path fails with [ErrEmptyPath].
func (s *Speech) Save(ctx context.Context, text, path string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}
	if filepath.Ext(path) == "" {
		path += s.engine.Format().Ext()
	}
	err := s.withAudio(ctx, text, func(audio io.Reader) error {
		return cache.WriteFile(path, audio)
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// Play plays audio with the player and returns when playback has finished or
// when ctx is done. The audio must be in a format the player understands,
// normally the one of the engine.
func (s *Speech) Play(ctx context.Context, audio io.Reader) error {
	return s.player.Play(ctx, audio)
}

// PlayFile plays the audio file at path, for example one written by Save,
// with the player.
func (s *Speech) PlayFile(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("htgotts: %w", err)
	}
	defer f.Close()
	return s.player.Play(ctx, f)
}

// withAudio opens the audio of text, hands it to use and closes it. The error
// of use comes first; when use succeeds, the error of Close is returned.
func (s *Speech) withAudio(ctx context.Context, text string, use func(audio io.Reader) error) error {
	audio, err := s.Open(ctx, text)
	if err != nil {
		return err
	}
	err = use(audio)
	if closeErr := audio.Close(); err == nil {
		err = closeErr
	}
	return err
}
