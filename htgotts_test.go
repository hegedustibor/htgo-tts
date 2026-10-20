package htgotts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/player"
)

// testMD5 is the MD5 of "Test" in lowercase hexadecimal, as htgotts v1 and
// the cache spell it in file names.
const testMD5 = "0cbc6611f5540bd0809a388dc95a615b"

var errBoom = errors.New("boom")

// fakeEngine is an engine that serves fixed audio. It counts its calls,
// refuses empty text and a done context like a real engine, and can be told
// to fail or to hand out audio whose Close fails.
type fakeEngine struct {
	audio       string
	format      engine.Format // FormatMP3 when empty
	fingerprint string        // "fake" when empty
	err         error         // returned by every Synthesize
	closeErr    error         // returned by Close of every audio
	calls       atomic.Int32
}

func (e *fakeEngine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	e.calls.Add(1)
	if e.err != nil {
		return nil, fmt.Errorf("fake engine: %w", e.err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("fake engine: %w", engine.ErrEmptyText)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fake engine: %w", err)
	}
	return &fakeAudio{Reader: strings.NewReader(e.audio), closeErr: e.closeErr}, nil
}

func (e *fakeEngine) Format() engine.Format {
	if e.format == "" {
		return engine.FormatMP3
	}
	return e.format
}

func (e *fakeEngine) Fingerprint() string {
	if e.fingerprint == "" {
		return "fake"
	}
	return e.fingerprint
}

// fakeAudio is the stream a fakeEngine hands out.
type fakeAudio struct {
	io.Reader
	closeErr error
}

func (a *fakeAudio) Close() error { return a.closeErr }

// streamEngine serves its audio in two parts through a pipe: the first part
// at once, the second only when the test closes release. When ctx is done
// before that, the stream ends with ctx's error instead, like a real engine's.
type streamEngine struct {
	first, second string
	release       chan struct{}
}

func (e *streamEngine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		if _, err := pw.Write([]byte(e.first)); err != nil {
			return // closed by the reader before the first part was read
		}
		select {
		case <-e.release:
			pw.Write([]byte(e.second))
			pw.Close()
		case <-ctx.Done():
			pw.CloseWithError(fmt.Errorf("stream engine: %w", ctx.Err()))
		}
	}()
	return pr, nil
}

func (e *streamEngine) Format() engine.Format { return engine.FormatMP3 }
func (e *streamEngine) Fingerprint() string   { return "stream" }

// recorder is a player that keeps the audio it was given, so that a test can
// check the bytes, and that counts its plays, so that a test can check that
// the caller's own player instance is the one being used.
type recorder struct {
	got   []byte
	plays int
}

func (r *recorder) Play(ctx context.Context, audio io.Reader) error {
	r.plays++
	data, err := io.ReadAll(audio)
	r.got = append(r.got, data...)
	return err
}

// quitter is a player that stops after two bytes with err, like a program
// that crashed or was closed before the audio ended.
type quitter struct{ err error }

func (q quitter) Play(ctx context.Context, audio io.Reader) error {
	if _, err := io.ReadFull(audio, make([]byte, 2)); err != nil {
		return err
	}
	return q.err
}

// failingWriter is a writer whose every write fails.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// newSpeech calls New and fails the test when it errors.
func newSpeech(t *testing.T, e engine.Engine, opts ...Option) *Speech {
	t.Helper()
	s, err := New(e, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// entries returns the names of the entries in dir, sorted; a missing dir has
// none.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

// wantEntries fails the test unless dir holds exactly the named entries, in
// any order. Without names it checks that dir is empty or missing, which also
// catches a temporary file left behind.
func wantEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	want = slices.Clone(want)
	slices.Sort(want)
	if got := entries(t, dir); !slices.Equal(got, want) {
		t.Errorf("%s holds %q, want %q", dir, got, want)
	}
}

// readFile returns the content of path.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(b)
}

func TestNew(t *testing.T) {
	e := &fakeEngine{}

	t.Run("nil engine", func(t *testing.T) {
		s, err := New(nil)
		if !errors.Is(err, ErrNilEngine) {
			t.Fatalf("New(nil) error = %v, want ErrNilEngine", err)
		}
		if s != nil {
			t.Error("New(nil) returned a Speech with the error")
		}
	})

	t.Run("defaults", func(t *testing.T) {
		s := newSpeech(t, e)
		cmd, ok := s.player.(*player.Command)
		if !ok || cmd.Name != "mplayer" {
			t.Errorf("default player = %#v, want player.MPlayer()", s.player)
		}
		if s.cache != nil {
			t.Error("New without WithFolder set up a cache")
		}
	})

	t.Run("nil player", func(t *testing.T) {
		if _, err := New(e, WithPlayer(nil)); !errors.Is(err, ErrNilPlayer) {
			t.Fatalf("New(WithPlayer(nil)) error = %v, want ErrNilPlayer", err)
		}
	})

	t.Run("empty folder", func(t *testing.T) {
		if _, err := New(e, WithFolder("")); !errors.Is(err, ErrEmptyFolder) {
			t.Fatalf(`New(WithFolder("")) error = %v, want ErrEmptyFolder`, err)
		}
	})

	t.Run("the first invalid option fails New", func(t *testing.T) {
		if _, err := New(e, WithFolder(""), WithPlayer(nil)); !errors.Is(err, ErrEmptyFolder) {
			t.Fatalf("error = %v, want ErrEmptyFolder, the first invalid option", err)
		}
	})

	t.Run("every option applies", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "audio")
		rec := &recorder{}
		s := newSpeech(t, e, WithFolder(dir), WithPlayer(rec))
		if s.player != rec {
			t.Errorf("player = %#v, want the recorder given to WithPlayer", s.player)
		}
		if s.cache == nil || s.cache.Dir != dir {
			t.Errorf("cache = %#v, want a store in %s", s.cache, dir)
		}
		// The folder is the cache's business, created on the first write.
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after New, Stat(%s) = %v, want not exist", dir, err)
		}
	})
}

func TestSpeakWithoutAFolderWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	e := &fakeEngine{audio: "mp3 bytes"}
	rec := &recorder{}
	s := newSpeech(t, e, WithPlayer(rec))

	if err := s.Speak(context.Background(), "Hello"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if string(rec.got) != e.audio {
		t.Errorf("the player received %q, want the engine's %q", rec.got, e.audio)
	}
	if calls := e.calls.Load(); calls != 1 {
		t.Errorf("the engine was called %d times, want 1", calls)
	}
	if rec.plays != 1 {
		t.Errorf("the player was called %d times, want 1", rec.plays)
	}
	wantEntries(t, dir)
}

func TestSpeakWithAFolderCachesTheAudio(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audio")
	e := &fakeEngine{audio: "mp3 bytes", fingerprint: "hu"}
	rec := &recorder{}
	s := newSpeech(t, e, WithFolder(dir), WithPlayer(rec))

	if err := s.Speak(context.Background(), "Test"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if string(rec.got) != e.audio {
		t.Errorf("the player received %q, want the engine's %q", rec.got, e.audio)
	}
	name := "hu_" + testMD5 + ".mp3"
	wantEntries(t, dir, name)
	if got := readFile(t, filepath.Join(dir, name)); got != e.audio {
		t.Errorf("the cached file holds %q, want %q", got, e.audio)
	}

	// The second Speak of the same text comes from the file.
	rec.got = nil
	if err := s.Speak(context.Background(), "Test"); err != nil {
		t.Fatalf("second Speak: %v", err)
	}
	if calls := e.calls.Load(); calls != 1 {
		t.Errorf("the engine was called %d times for two Speaks, want 1", calls)
	}
	if string(rec.got) != e.audio {
		t.Errorf("the player received %q from the cache, want %q", rec.got, e.audio)
	}
	if rec.plays != 2 {
		t.Errorf("the player was called %d times, want 2", rec.plays)
	}
	wantEntries(t, dir, name)
}

func TestSpeakRecognisesV1Files(t *testing.T) {
	dir := t.TempDir()
	// htgotts v1 named the file of a text <language>_<md5 of the text>.mp3,
	// and the Google engine's fingerprint is the language code, so a folder
	// filled by v1 keeps serving hits.
	if err := os.WriteFile(filepath.Join(dir, "en_"+testMD5+".mp3"), []byte("v1 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{audio: "fresh audio", fingerprint: "en"}
	rec := &recorder{}
	s := newSpeech(t, e, WithFolder(dir), WithPlayer(rec))

	if err := s.Speak(context.Background(), "Test"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if calls := e.calls.Load(); calls != 0 {
		t.Errorf("the engine was called %d times for a text cached by v1, want 0", calls)
	}
	if string(rec.got) != "v1 audio" {
		t.Errorf("the player received %q, want the v1 file", rec.got)
	}
}

func TestSpeakReturnsThePlayersErrorAndCachesNothing(t *testing.T) {
	dir := t.TempDir()
	e := &fakeEngine{audio: "mp3 bytes"}
	s := newSpeech(t, e, WithFolder(dir), WithPlayer(quitter{errBoom}))

	if err := s.Speak(context.Background(), "Hello"); !errors.Is(err, errBoom) {
		t.Fatalf("Speak error = %v, want the player's", err)
	}
	wantEntries(t, dir)
}

func TestSpeakPrefersThePlayersErrorToTheCloseError(t *testing.T) {
	errPlayer := errors.New("player")
	e := &fakeEngine{audio: "mp3 bytes", closeErr: errBoom}
	s := newSpeech(t, e, WithPlayer(quitter{errPlayer}))

	if err := s.Speak(context.Background(), "Hello"); !errors.Is(err, errPlayer) {
		t.Fatalf("Speak error = %v, want the player's", err)
	}
}

func TestSpeakReturnsTheCloseErrorWhenPlaybackSucceeds(t *testing.T) {
	e := &fakeEngine{audio: "mp3 bytes", closeErr: errBoom}
	s := newSpeech(t, e, WithPlayer(player.Discard))

	if err := s.Speak(context.Background(), "Hello"); !errors.Is(err, errBoom) {
		t.Fatalf("Speak error = %v, want the audio's Close error", err)
	}
}

func TestSpeakFailsFastOnACancelledContext(t *testing.T) {
	e := &fakeEngine{audio: "mp3 bytes"}
	s := newSpeech(t, e, WithPlayer(player.Discard))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Speak(ctx, "Hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Speak error = %v, want context.Canceled", err)
	}
}

func TestSpeakStopsWhenTheContextIsDoneMidway(t *testing.T) {
	dir := t.TempDir()
	// The engine delivers the first part and then waits for a release that
	// never comes, like a speech service that stalls.
	e := &streamEngine{first: "first,", second: "second", release: make(chan struct{})}
	s := newSpeech(t, e, WithFolder(dir), WithPlayer(player.Discard))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := s.Speak(ctx, "Hello")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Speak error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Speak returned after %v, want soon after the deadline", elapsed)
	}
	wantEntries(t, dir)
}

func TestOpenStreams(t *testing.T) {
	tests := []struct {
		name   string
		folder bool
	}{
		{"without a folder", false},
		{"with a folder", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &streamEngine{first: "first,", second: "second", release: make(chan struct{})}
			var opts []Option
			if tt.folder {
				opts = append(opts, WithFolder(t.TempDir()))
			}
			s := newSpeech(t, e, opts...)

			rc, err := s.Open(context.Background(), "Hello")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			buf := make([]byte, 64)
			n, err := rc.Read(buf)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got := string(buf[:n]); got != e.first {
				t.Errorf("first Read = %q, want %q before the engine has finished", got, e.first)
			}
			close(e.release)
			rest, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(rest) != e.second {
				t.Errorf("the rest = %q, want %q", rest, e.second)
			}
			if err := rc.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestSynthesize(t *testing.T) {
	e := &fakeEngine{audio: "mp3 bytes"}
	// The default player is never involved, so no mplayer is needed.
	s := newSpeech(t, e)

	got, err := s.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(got) != e.audio {
		t.Errorf("Synthesize = %q, want %q", got, e.audio)
	}

	var buf bytes.Buffer
	if err := s.SynthesizeTo(context.Background(), "Hello", &buf); err != nil {
		t.Fatalf("SynthesizeTo: %v", err)
	}
	if buf.String() != e.audio {
		t.Errorf("SynthesizeTo wrote %q, want %q", buf.String(), e.audio)
	}
	if calls := e.calls.Load(); calls != 2 {
		t.Errorf("the engine was called %d times, want 2", calls)
	}

	t.Run("writer error", func(t *testing.T) {
		err := s.SynthesizeTo(context.Background(), "Hello", failingWriter{errBoom})
		if !errors.Is(err, errBoom) {
			t.Fatalf("SynthesizeTo error = %v, want the writer's", err)
		}
	})
}

func TestSave(t *testing.T) {
	t.Run("adds the engine's extension and creates the folder", func(t *testing.T) {
		e := &fakeEngine{audio: "wav bytes", format: engine.FormatWAV}
		s := newSpeech(t, e)
		path, err := s.Save(context.Background(), "Hello", filepath.Join(t.TempDir(), "new", "hello"))
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if filepath.Base(path) != "hello.wav" || filepath.Base(filepath.Dir(path)) != "new" {
			t.Errorf("Save returned %q, want new/hello.wav", path)
		}
		if got := readFile(t, path); got != e.audio {
			t.Errorf("the saved file holds %q, want %q", got, e.audio)
		}
	})

	t.Run("keeps the caller's extension", func(t *testing.T) {
		e := &fakeEngine{audio: "mp3 bytes"}
		s := newSpeech(t, e)
		want := filepath.Join(t.TempDir(), "hello.mpeg")
		path, err := s.Save(context.Background(), "Hello", want)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if path != want {
			t.Errorf("Save returned %q, want %q", path, want)
		}
		if got := readFile(t, path); got != e.audio {
			t.Errorf("the saved file holds %q, want %q", got, e.audio)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		e := &fakeEngine{audio: "mp3 bytes"}
		s := newSpeech(t, e)
		if _, err := s.Save(context.Background(), "Hello", ""); !errors.Is(err, ErrEmptyPath) {
			t.Fatalf("Save error = %v, want ErrEmptyPath", err)
		}
		if calls := e.calls.Load(); calls != 0 {
			t.Errorf("the engine was called %d times for an empty path, want 0", calls)
		}
	})

	t.Run("an engine error leaves no file", func(t *testing.T) {
		dir := t.TempDir()
		s := newSpeech(t, &fakeEngine{err: errBoom})
		path, err := s.Save(context.Background(), "Hello", filepath.Join(dir, "hello"))
		if !errors.Is(err, errBoom) {
			t.Fatalf("Save error = %v, want the engine's", err)
		}
		if path != "" {
			t.Errorf("Save returned %q with the error, want an empty path", path)
		}
		wantEntries(t, dir)
	})

	t.Run("with a folder the audio is cached as well", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "audio")
		outDir := t.TempDir()
		e := &fakeEngine{audio: "mp3 bytes", fingerprint: "hu"}
		s := newSpeech(t, e, WithFolder(cacheDir))

		path, err := s.Save(context.Background(), "Test", filepath.Join(outDir, "first"))
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		wantEntries(t, outDir, "first.mp3")
		wantEntries(t, cacheDir, "hu_"+testMD5+".mp3")
		if got := readFile(t, path); got != e.audio {
			t.Errorf("the saved file holds %q, want %q", got, e.audio)
		}

		// The second Save of the same text is served from the cache.
		path, err = s.Save(context.Background(), "Test", filepath.Join(outDir, "second"))
		if err != nil {
			t.Fatalf("second Save: %v", err)
		}
		if calls := e.calls.Load(); calls != 1 {
			t.Errorf("the engine was called %d times for two Saves, want 1", calls)
		}
		if got := readFile(t, path); got != e.audio {
			t.Errorf("the second saved file holds %q, want %q", got, e.audio)
		}
	})
}

func TestPlay(t *testing.T) {
	rec := &recorder{}
	s := newSpeech(t, &fakeEngine{}, WithPlayer(rec))

	if err := s.Play(context.Background(), strings.NewReader("mp3 bytes")); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if string(rec.got) != "mp3 bytes" {
		t.Errorf("the player received %q, want %q", rec.got, "mp3 bytes")
	}
}

func TestPlayFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.mp3")
	if err := os.WriteFile(path, []byte("mp3 bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	s := newSpeech(t, &fakeEngine{}, WithPlayer(rec))

	if err := s.PlayFile(context.Background(), path); err != nil {
		t.Fatalf("PlayFile: %v", err)
	}
	if string(rec.got) != "mp3 bytes" {
		t.Errorf("the player received %q, want the file's %q", rec.got, "mp3 bytes")
	}

	err := s.PlayFile(context.Background(), filepath.Join(dir, "missing.mp3"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("PlayFile of a missing file error = %v, want fs.ErrNotExist", err)
	}
	if rec.plays != 1 {
		t.Errorf("the player was called %d times, want 1: not for a missing file", rec.plays)
	}
}

func TestEngineErrorsComeThroughUnchanged(t *testing.T) {
	t.Run("empty text", func(t *testing.T) {
		dir := t.TempDir()
		s := newSpeech(t, &fakeEngine{audio: "mp3 bytes"}, WithFolder(dir), WithPlayer(player.Discard))
		if err := s.Speak(context.Background(), "   "); !errors.Is(err, engine.ErrEmptyText) {
			t.Fatalf("Speak error = %v, want engine.ErrEmptyText", err)
		}
		wantEntries(t, dir)
	})

	t.Run("http error", func(t *testing.T) {
		httpErr := &engine.HTTPError{StatusCode: 503, URL: "https://example.com/tts", Message: "busy"}
		s := newSpeech(t, &fakeEngine{err: httpErr}, WithPlayer(player.Discard))
		_, err := s.Synthesize(context.Background(), "Hello")
		var got *engine.HTTPError
		if !errors.As(err, &got) {
			t.Fatalf("Synthesize error = %v, want it to wrap the *engine.HTTPError", err)
		}
		if got.StatusCode != 503 {
			t.Errorf("StatusCode = %d, want 503", got.StatusCode)
		}
	})
}

func TestSpeakIsSafeForConcurrentUse(t *testing.T) {
	dir := t.TempDir()
	e := &fakeEngine{audio: strings.Repeat("mp3 bytes ", 1000), fingerprint: "hu"}
	s := newSpeech(t, e, WithFolder(dir), WithPlayer(player.Discard))

	const goroutines = 8
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Speak(context.Background(), "Test")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Speak %d: %v", i, err)
		}
	}
	name := "hu_" + testMD5 + ".mp3"
	wantEntries(t, dir, name)
	if got := readFile(t, filepath.Join(dir, name)); got != e.audio {
		t.Errorf("the cached file holds %d bytes, want %d", len(got), len(e.audio))
	}
}
