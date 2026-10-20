package command

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// requireShell skips the test where the POSIX programs it runs are missing.
func requireShell(t *testing.T, programs ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	for _, p := range programs {
		if _, err := exec.LookPath(p); err != nil {
			t.Skipf("%s is not installed", p)
		}
	}
}

// useTempDir makes the engine create its temporary files under a fresh
// directory and returns it, so that a test can check what is left behind.
func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

// assertEmpty fails the test when the directory has an entry: a temporary
// file or directory of the engine that was not removed.
func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("%s was left behind in the temporary directory", entry.Name())
	}
}

func readAll(t *testing.T, e *Engine, text string) (string, error) {
	t.Helper()
	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	defer closeWithin(t, rc, 5*time.Second)
	b, err := io.ReadAll(rc)
	return string(b), err
}

// closeWithin fails the test when Close does not return in time, which means
// that the program or the forwarding goroutine is stuck.
func closeWithin(t *testing.T, rc io.Closer, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		rc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("Close did not return: the program is still running")
	}
}

func TestSynthesizeStreamsStandardOutput(t *testing.T) {
	requireShell(t, "sh", "cat")
	e := New(engine.FormatWAV, "sh", "-c", "cat")
	const text = "Szia, világ!\nMásodik sor."

	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading the audio: %v", err)
	}

	// cat echoes its standard input, so the audio is the text itself.
	if string(got) != text {
		t.Errorf("audio = %q, want the text %q", got, text)
	}
	closeWithin(t, rc, 5*time.Second)
	if _, err := rc.Read(make([]byte, 1)); err == nil {
		t.Error("Read after Close returned no error")
	}
	// A second Close is harmless.
	closeWithin(t, rc, 5*time.Second)
}

func TestSynthesizeStreamsLargeOutput(t *testing.T) {
	requireShell(t, "sh", "head")
	// More than a pipe buffer, so the audio has to be forwarded while the
	// program is still writing.
	const size = 1_000_000
	e := New(engine.FormatWAV, "sh", "-c", "head -c 1000000 /dev/zero")

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != size {
		t.Errorf("read %d bytes, want %d", len(got), size)
	}
}

func TestSynthesizeStreamsBeforeTheProgramExits(t *testing.T) {
	requireShell(t, "sh", "sleep")
	e := New(engine.FormatWAV, "sh", "-c", "printf first; exec sleep 10")

	rc, err := e.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	first := make([]byte, len("first"))
	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(rc, first)
		readErr <- err
	}()
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("reading the first bytes: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first bytes are not readable while the program is still running")
	}
	if string(first) != "first" {
		t.Errorf("first bytes = %q, want %q", first, "first")
	}

	// Close stops the sleeping program instead of waiting for it.
	start := time.Now()
	closeWithin(t, rc, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Close took %v, want the program interrupted at once", elapsed)
	}
}

func TestSynthesizeTextArgument(t *testing.T) {
	requireShell(t, "sh", "cat")
	t.Run("whole argument", func(t *testing.T) {
		// The text arrives as the argument, and standard input is empty.
		e := New(engine.FormatWAV, "sh", "-c", `printf "[%s]%s" "$(cat)" "$1"`, "_", PlaceholderText)
		got, err := readAll(t, e, "Hello, world!")
		if err != nil {
			t.Fatal(err)
		}
		if want := "[]Hello, world!"; got != want {
			t.Errorf("audio = %q, want %q", got, want)
		}
	})
	t.Run("inside an argument", func(t *testing.T) {
		e := New(engine.FormatWAV, "sh", "-c", `printf %s "$1"`, "_", "--text="+PlaceholderText)
		got, err := readAll(t, e, "Hello")
		if err != nil {
			t.Fatal(err)
		}
		if want := "--text=Hello"; got != want {
			t.Errorf("audio = %q, want %q", got, want)
		}
	})
	t.Run("text that looks like the other placeholder", func(t *testing.T) {
		e := New(engine.FormatWAV, "sh", "-c", `printf %s "$1"`, "_", PlaceholderText)
		got, err := readAll(t, e, "say {out} loud")
		if err != nil {
			t.Fatal(err)
		}
		if want := "say {out} loud"; got != want {
			t.Errorf("audio = %q, want the text unchanged %q", got, want)
		}
	})
}

func TestSynthesizeOutputFile(t *testing.T) {
	requireShell(t, "sh")
	tmp := useTempDir(t)
	for _, format := range []engine.Format{engine.FormatWAV, engine.FormatMP3} {
		t.Run(string(format), func(t *testing.T) {
			// The program writes its own output path into the file, so the
			// audio tells where the file was.
			e := New(format, "sh", "-c", `printf "audio:%s" "$1" > "$1"`, "_", PlaceholderOut)

			rc, err := e.Synthesize(context.Background(), "Hello")
			if err != nil {
				t.Fatalf("Synthesize: %v", err)
			}
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("reading the audio: %v", err)
			}

			path, ok := strings.CutPrefix(string(got), "audio:")
			if !ok {
				t.Fatalf("audio = %q, want the file's content", got)
			}
			if filepath.Ext(path) != format.Ext() {
				t.Errorf("the file is %q, want the extension %q", path, format.Ext())
			}
			if !strings.HasPrefix(path, tmp) {
				t.Errorf("the file is %q, want it under the temporary directory %q", path, tmp)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("the file is gone before Close: %v", err)
			}

			closeWithin(t, rc, 5*time.Second)

			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("after Close, Stat = %v, want the file removed", err)
			}
			assertEmpty(t, tmp)
			// A second Close is harmless.
			if err := rc.Close(); err != nil {
				t.Errorf("second Close: %v", err)
			}
		})
	}
}

func TestSynthesizeOutputFileWithTextArgument(t *testing.T) {
	requireShell(t, "sh")
	useTempDir(t)
	// The shape of pico2wave: the text is an argument and the audio a file.
	e := New(engine.FormatWAV, "sh", "-c", `printf %s "$2" > "$1"`, "_", PlaceholderOut, PlaceholderText)

	got, err := readAll(t, e, "Hello, world!")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Hello, world!"; got != want {
		t.Errorf("audio = %q, want %q", got, want)
	}
}

func TestSynthesizeOutputFileMissing(t *testing.T) {
	requireShell(t, "sh")
	tmp := useTempDir(t)
	// The program exits successfully but writes no file.
	e := New(engine.FormatWAV, "sh", "-c", "true", "_", PlaceholderOut)

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader without an audio file")
	}
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "wrote no audio file") {
		t.Errorf("error = %v, want one about the missing audio file", err)
	}
	assertEmpty(t, tmp)
}

func TestSynthesizeProgramNotFound(t *testing.T) {
	const name = "htgotts-no-such-program"
	e := New(engine.FormatWAV, name, "-x")

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader for a missing program")
	}
	if !errors.Is(err, ErrProgramNotFound) {
		t.Fatalf("err = %v, want ErrProgramNotFound", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("err = %q, want it to name %q", err, name)
	}
}

func TestSynthesizeReportsStandardErrorInStreamMode(t *testing.T) {
	requireShell(t, "sh")
	e := New(engine.FormatWAV, "sh", "-c", `printf partial; echo "voice not found" >&2; exit 3`)

	rc, err := e.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	defer closeWithin(t, rc, 5*time.Second)
	got, err := io.ReadAll(rc)

	// The audio written before the failure arrives, and the failure comes
	// in place of EOF.
	if string(got) != "partial" {
		t.Errorf("audio = %q, want %q", got, "partial")
	}
	if err == nil {
		t.Fatal("the stream ended without an error for exit status 3")
	}
	if !strings.Contains(err.Error(), "voice not found") {
		t.Errorf("err = %q, want the program's standard error in it", err)
	}
	if !strings.Contains(err.Error(), "sh") {
		t.Errorf("err = %q, want the program's name in it", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("err = %v, want it to wrap an ExitError with code 3", err)
	}
}

func TestSynthesizeReportsStandardErrorInFileMode(t *testing.T) {
	requireShell(t, "sh")
	tmp := useTempDir(t)
	e := New(engine.FormatWAV, "sh", "-c", `echo "no such model" >&2; exit 2`, "_", PlaceholderOut)

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader for a failed program")
	}
	if err == nil {
		t.Fatal("Synthesize returned nil for exit status 2")
	}
	if !strings.Contains(err.Error(), "no such model") {
		t.Errorf("err = %q, want the program's standard error in it", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Errorf("err = %v, want it to wrap an ExitError with code 2", err)
	}
	assertEmpty(t, tmp)
}

func TestSynthesizeKeepsTheTailOfStandardError(t *testing.T) {
	requireShell(t, "sh")
	// 2000 bytes of noise, then the part that matters.
	const script = `i=0; while [ $i -lt 200 ]; do printf 0123456789 >&2; i=$((i+1)); done; printf END >&2; exit 1`
	e := New(engine.FormatWAV, "sh", "-c", script)

	_, err := readAll(t, e, "Hello")

	if err == nil {
		t.Fatal("the stream ended without an error for exit status 1")
	}
	if !strings.HasSuffix(err.Error(), "END") {
		t.Errorf("err = %q, want it to end with the last standard error output", err)
	}
	if n := strings.Count(err.Error(), "0123456789"); n > stderrTail/10 {
		t.Errorf("err carries %d repetitions of the noise, want at most %d: only the tail is kept", n, stderrTail/10)
	}
}

func TestSynthesizeStopsTheProgramWhenTheContextIsDone(t *testing.T) {
	requireShell(t, "sh", "sleep")
	t.Run("stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		e := New(engine.FormatWAV, "sleep", "10")

		rc, err := e.Synthesize(ctx, "Hello")
		if err != nil {
			t.Fatalf("Synthesize: %v", err)
		}
		defer closeWithin(t, rc, 5*time.Second)
		start := time.Now()
		got, err := io.ReadAll(rc)
		elapsed := time.Since(start)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
		if len(got) != 0 {
			t.Errorf("read %q, want nothing", got)
		}
		if elapsed > time.Second {
			t.Errorf("the stream ended after %v, want within a second", elapsed)
		}
	})
	t.Run("file", func(t *testing.T) {
		tmp := useTempDir(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		e := New(engine.FormatWAV, "sh", "-c", "exec sleep 10", "_", PlaceholderOut)

		start := time.Now()
		rc, err := e.Synthesize(ctx, "Hello")
		elapsed := time.Since(start)

		if rc != nil {
			rc.Close()
			t.Error("Synthesize returned a reader for an interrupted program")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
		if elapsed > time.Second {
			t.Errorf("Synthesize returned after %v, want within a second", elapsed)
		}
		assertEmpty(t, tmp)
	})
}

func TestSynthesizeFailsFastOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The context is checked before the program is even looked up.
	e := New(engine.FormatWAV, "htgotts-no-such-program")

	rc, err := e.Synthesize(ctx, "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestWithTimeout(t *testing.T) {
	requireShell(t, "sleep")
	e := New(engine.FormatWAV, "sleep", "10").With(WithTimeout(100 * time.Millisecond))

	rc, err := e.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	defer closeWithin(t, rc, 5*time.Second)
	start := time.Now()
	_, err = io.ReadAll(rc)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("the stream ended after %v, want within a second", elapsed)
	}
}

func TestCloseStopsTheProgram(t *testing.T) {
	requireShell(t, "yes")
	// yes writes forever and blocks once nobody reads.
	e := New(engine.FormatWAV, "yes")

	rc, err := e.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if _, err := io.ReadFull(rc, make([]byte, 10)); err != nil {
		t.Fatalf("reading the first bytes: %v", err)
	}

	start := time.Now()
	closeWithin(t, rc, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Close took %v, want the program stopped at once", elapsed)
	}
}

func TestSynthesizeEmptyText(t *testing.T) {
	// A missing program would be ErrProgramNotFound, so ErrEmptyText shows
	// that the text is checked before anything runs.
	e := New(engine.FormatWAV, "htgotts-no-such-program")
	for _, text := range []string{"", "   ", "\n\t "} {
		rc, err := e.Synthesize(context.Background(), text)
		if rc != nil {
			rc.Close()
			t.Errorf("Synthesize(%q) returned a reader", text)
		}
		if !errors.Is(err, engine.ErrEmptyText) {
			t.Errorf("Synthesize(%q) error is %v, want engine.ErrEmptyText", text, err)
		}
	}
}

func TestInvalidSettings(t *testing.T) {
	tests := []struct {
		name string
		e    *Engine
		want string
	}{
		{"empty name", New(engine.FormatWAV, ""), "empty program name"},
		{"blank name", New(engine.FormatWAV, "  "), "empty program name"},
		{"empty format", New("", "sh"), "empty format"},
		{"blank format", New(" ", "sh"), "empty format"},
		{"empty directory", New(engine.FormatWAV, "sh").With(WithDir("")), "empty directory"},
		{"environment entry without =", New(engine.FormatWAV, "sh").With(WithEnv([]string{"NOEQUALS"})), `invalid environment entry "NOEQUALS"`},
		{"environment entry without a name", New(engine.FormatWAV, "sh").With(WithEnv([]string{"=value"})), `invalid environment entry "=value"`},
		{"negative timeout", New(engine.FormatWAV, "sh").With(WithTimeout(-time.Second)), "negative timeout"},
		{"empty model", Piper(""), "empty model"},
		{"the first error wins", New("", ""), "empty program name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := tt.e.Synthesize(context.Background(), "Hello")
			if rc != nil {
				rc.Close()
				t.Error("Synthesize returned a reader for an invalid setting")
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestWithDir(t *testing.T) {
	requireShell(t, "sh")
	dir := t.TempDir()
	e := New(engine.FormatWAV, "sh", "-c", "pwd -P").With(WithDir(dir))

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}

	// The temporary directory may be reached through a symbolic link, as
	// /var is on macOS, so both sides are resolved.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != want {
		t.Errorf("the program ran in %q, want %q", strings.TrimSpace(got), want)
	}
}

func TestWithEnv(t *testing.T) {
	requireShell(t, "sh")
	t.Setenv("HTGOTTS_TEST", "inherited")
	const script = `printf "%s|%s" "$HTGOTTS_TEST" "$PATH"`

	t.Run("inherits the environment", func(t *testing.T) {
		got, err := readAll(t, New(engine.FormatWAV, "sh", "-c", script), "Hello")
		if err != nil {
			t.Fatal(err)
		}
		value, path, _ := strings.Cut(got, "|")
		if value != "inherited" {
			t.Errorf("HTGOTTS_TEST = %q in the program, want the inherited %q", value, "inherited")
		}
		if path == "" {
			t.Error("PATH is empty in the program")
		}
	})
	t.Run("adds and overrides", func(t *testing.T) {
		e := New(engine.FormatWAV, "sh", "-c", script).With(WithEnv([]string{"HTGOTTS_TEST=spoken"}))
		got, err := readAll(t, e, "Hello")
		if err != nil {
			t.Fatal(err)
		}
		value, path, _ := strings.Cut(got, "|")
		if value != "spoken" {
			t.Errorf("HTGOTTS_TEST = %q in the program, want the added %q", value, "spoken")
		}
		if path == "" {
			t.Error("PATH is empty in the program: the environment was replaced instead of extended")
		}
	})
}

func TestWithCopiesTheEngine(t *testing.T) {
	base := Say("Daniel")
	e := base.With(WithTimeout(time.Second), WithDir("/opt/voices"), WithEnv([]string{"A=1"}))

	if base.timeout != 0 || base.dir != "" || base.env != nil {
		t.Errorf("With changed the original engine: timeout %v, dir %q, env %q", base.timeout, base.dir, base.env)
	}
	if e.timeout != time.Second || e.dir != "/opt/voices" || !slices.Equal(e.env, []string{"A=1"}) {
		t.Errorf("With did not apply the options: timeout %v, dir %q, env %q", e.timeout, e.dir, e.env)
	}
	if !slices.Equal(e.args, base.args) {
		t.Errorf("the copy's args = %q, want %q", e.args, base.args)
	}
	if &e.args[0] == &base.args[0] {
		t.Error("the copy shares its args with the original")
	}
	if e.Fingerprint() != base.Fingerprint() {
		t.Errorf("Fingerprint() = %q after With, want %q: the options do not change the sound", e.Fingerprint(), base.Fingerprint())
	}
}

func TestFormat(t *testing.T) {
	if got := New(engine.FormatMP3, "sh").Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want mp3", got)
	}
	for name, e := range map[string]*Engine{"Say": Say(""), "ESpeakNG": ESpeakNG(""), "Piper": Piper("voice.onnx")} {
		if got := e.Format(); got != engine.FormatWAV {
			t.Errorf("%s().Format() = %q, want wav", name, got)
		}
	}
}

func TestFingerprint(t *testing.T) {
	const prefix = "command-say-"
	fp := New(engine.FormatWAV, "say", "-v", "Daniel", "-o", PlaceholderOut).Fingerprint()

	if !strings.HasPrefix(fp, prefix) || len(fp) != len(prefix)+8 {
		t.Fatalf("Fingerprint() = %q, want %q followed by eight hex digits", fp, prefix)
	}
	if strings.Trim(fp[len(prefix):], "0123456789abcdef") != "" {
		t.Errorf("Fingerprint() = %q, want hex digits after %q", fp, prefix)
	}
	if again := New(engine.FormatWAV, "say", "-v", "Daniel", "-o", PlaceholderOut).Fingerprint(); again != fp {
		t.Errorf("Fingerprint() = %q, want the same %q for the same settings", again, fp)
	}
	if other := New(engine.FormatWAV, "say", "-v", "Samantha", "-o", PlaceholderOut).Fingerprint(); other == fp {
		t.Errorf("Fingerprint() = %q with another voice, want a different one", other)
	}
	if other := New(engine.FormatWAV, "say", "-v", "Daniel", "-o", PlaceholderOut, "-r", "200").Fingerprint(); other == fp {
		t.Errorf("Fingerprint() = %q with another argument, want a different one", other)
	}
	// The format is the extension of the cached file, not part of the name.
	if same := New(engine.FormatMP3, "say", "-v", "Daniel", "-o", PlaceholderOut).Fingerprint(); same != fp {
		t.Errorf("Fingerprint() = %q with another format, want %q", same, fp)
	}
	// The placeholder is hashed as written, so the fingerprint does not
	// depend on the temporary file's path.
	if same := New(engine.FormatWAV, "say", "-v", "Daniel", "-o", "{out}").Fingerprint(); same != fp {
		t.Errorf("Fingerprint() = %q with the placeholder spelled out, want %q", same, fp)
	}
	// A path to the program keeps the base name and changes the hash.
	full := New(engine.FormatWAV, "/usr/bin/say", "-v", "Daniel", "-o", PlaceholderOut).Fingerprint()
	if !strings.HasPrefix(full, prefix) || full == fp {
		t.Errorf("Fingerprint() = %q for a path, want %q with another hash than %q", full, prefix, fp)
	}
	if got := New(engine.FormatWAV, "piper").Fingerprint(); !strings.HasPrefix(got, "command-piper-") {
		t.Errorf("Fingerprint() = %q without arguments, want the prefix command-piper-", got)
	}
	if got := New(engine.FormatWAV, "my tts/speak.exe").Fingerprint(); !strings.HasPrefix(got, "command-speak.exe-") {
		t.Errorf("Fingerprint() = %q, want the base name of the program in it", got)
	}
	if got := New(engine.FormatWAV, "my tts").Fingerprint(); !strings.HasPrefix(got, "command-my-tts-") {
		t.Errorf("Fingerprint() = %q, want the space replaced by -", got)
	}
}

func TestSay(t *testing.T) {
	e := Say("Daniel")
	want := []string{"-v", "Daniel", "-o", "{out}", "--file-format=WAVE", "--data-format=LEI16@22050"}
	if e.name != "say" || !slices.Equal(e.args, want) {
		t.Errorf("Say(Daniel) = %q %q, want say %q", e.name, e.args, want)
	}
	if e.err != nil {
		t.Errorf("Say(Daniel) remembers the error %v", e.err)
	}
	if !e.hasOut || e.hasText {
		t.Errorf("Say(Daniel) has hasOut %v, hasText %v; want the file mode with the text on standard input", e.hasOut, e.hasText)
	}

	e = Say(" ")
	want = []string{"-o", "{out}", "--file-format=WAVE", "--data-format=LEI16@22050"}
	if !slices.Equal(e.args, want) {
		t.Errorf("Say(\" \") args = %q, want %q without a voice", e.args, want)
	}
}

func TestESpeakNG(t *testing.T) {
	e := ESpeakNG("en-gb")
	want := []string{"-v", "en-gb", "--stdin", "--stdout"}
	if e.name != "espeak-ng" || !slices.Equal(e.args, want) {
		t.Errorf("ESpeakNG(en-gb) = %q %q, want espeak-ng %q", e.name, e.args, want)
	}
	if e.err != nil {
		t.Errorf("ESpeakNG(en-gb) remembers the error %v", e.err)
	}
	if e.hasOut || e.hasText {
		t.Errorf("ESpeakNG(en-gb) has hasOut %v, hasText %v; want the streaming mode with the text on standard input", e.hasOut, e.hasText)
	}

	if e := ESpeakNG(""); !slices.Equal(e.args, []string{"--stdin", "--stdout"}) {
		t.Errorf("ESpeakNG(\"\") args = %q, want only --stdin --stdout", e.args)
	}
}

func TestPiper(t *testing.T) {
	e := Piper(" en_US-lessac-medium.onnx ")
	want := []string{"--model", "en_US-lessac-medium.onnx", "--output_file", "{out}"}
	if e.name != "piper" || !slices.Equal(e.args, want) {
		t.Errorf("Piper(model) = %q %q, want piper %q", e.name, e.args, want)
	}
	if e.err != nil {
		t.Errorf("Piper(model) remembers the error %v", e.err)
	}
	if !e.hasOut || e.hasText {
		t.Errorf("Piper(model) has hasOut %v, hasText %v; want the file mode with the text on standard input", e.hasOut, e.hasText)
	}
}

func TestTailKeepsTheLastBytes(t *testing.T) {
	tl := &tail{limit: 8}
	for _, s := range []string{"abc", "defgh", "ij"} {
		if n, err := tl.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := tl.String(); got != "cdefghij" {
		t.Fatalf("after small writes tail = %q, want %q", got, "cdefghij")
	}
	if _, err := tl.Write([]byte("klmnopqrstuvwxyz0123")); err != nil {
		t.Fatal(err)
	}
	if got := tl.String(); got != "wxyz0123" {
		t.Fatalf("after a large write tail = %q, want %q", got, "wxyz0123")
	}
}
