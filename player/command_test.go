package player

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestCommand_PlayWritesTheAudioToStandardInput(t *testing.T) {
	requireShell(t, "sh", "cat")
	received := filepath.Join(t.TempDir(), "received")
	// More than a pipe buffer, so the program has to keep reading.
	audio := bytes.Repeat([]byte("htgotts "), 100_000)

	cmd := &Command{Name: "sh", Args: []string{"-c", `cat > "$0"`, received}}
	if err := cmd.Play(context.Background(), bytes.NewReader(audio)); err != nil {
		t.Fatalf("Play: %v", err)
	}

	got, err := os.ReadFile(received)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, audio) {
		t.Fatalf("the program received %d bytes, want %d", len(got), len(audio))
	}
}

func TestCommand_PlayNamesTheMissingProgram(t *testing.T) {
	const name = "htgotts-no-such-player"
	cmd := &Command{Name: name, Args: []string{"-"}}
	err := cmd.Play(context.Background(), strings.NewReader("audio"))
	if !errors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("err = %v, want ErrPlayerNotFound", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("err = %q, want it to name %q", err, name)
	}
}

func TestCommand_PlayReportsStandardErrorOnFailure(t *testing.T) {
	requireShell(t, "sh")
	cmd := &Command{Name: "sh", Args: []string{"-c", `echo "cannot open audio device" >&2; exit 3`}}
	err := cmd.Play(context.Background(), strings.NewReader("audio"))
	if err == nil {
		t.Fatal("Play returned nil for exit status 3")
	}
	if !strings.Contains(err.Error(), "cannot open audio device") {
		t.Fatalf("err = %q, want the program's standard error in it", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("err = %v, want it to wrap an ExitError with code 3", err)
	}
}

func TestCommand_PlayStopsTheProgramWhenTheContextIsDone(t *testing.T) {
	requireShell(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := (&Command{Name: "sleep", Args: []string{"10"}}).Play(ctx, endless{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Play returned after %v, want within a second", elapsed)
	}
}

func TestCommand_PlayFailsFastOnACancelledContext(t *testing.T) {
	requireShell(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&Command{Name: "sleep", Args: []string{"10"}}).Play(ctx, strings.NewReader("audio"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestMPlayer(t *testing.T) {
	c := MPlayer()
	want := []string{"-really-quiet", "-cache", "8092", "-"}
	if c.Name != "mplayer" || !slices.Equal(c.Args, want) {
		t.Fatalf("MPlayer() = %q %q, want mplayer %q", c.Name, c.Args, want)
	}
}

func TestFFplay(t *testing.T) {
	c := FFplay()
	want := []string{"-nodisp", "-autoexit", "-loglevel", "error", "-"}
	if c.Name != "ffplay" || !slices.Equal(c.Args, want) {
		t.Fatalf("FFplay() = %q %q, want ffplay %q", c.Name, c.Args, want)
	}
}

func TestTail_KeepsTheLastBytes(t *testing.T) {
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

// endless is audio that never ends, like a live stream.
type endless struct{}

func (endless) Read(p []byte) (int, error) { return len(p), nil }
