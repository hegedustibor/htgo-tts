package player

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Command is a [Player] that runs an external program and writes the audio to
// its standard input. The program's standard output is discarded; the tail of
// its standard error is kept and reported when the program exits with a
// non-zero status. [MPlayer] and [FFplay] return ready-made commands, and any
// other program that plays audio from its standard input works the same way,
// for example SoX:
//
//	p := &player.Command{Name: "play", Args: []string{"-q", "-t", "mp3", "-"}}
type Command struct {
	// Name is the program to run. It is looked up in PATH unless it contains
	// a path separator.
	Name string

	// Args are the program's arguments. Most players take "-" to mean
	// standard input.
	Args []string
}

// MPlayer returns a Command that plays the audio with mplayer, reading it
// from standard input and printing nothing.
func MPlayer() *Command {
	return &Command{
		Name: "mplayer",
		Args: []string{"-really-quiet", "-cache", "8092", "-"},
	}
}

// FFplay returns a Command that plays the audio with ffplay, part of FFmpeg,
// without opening a window and exiting when the audio ends. FFmpeg documents
// "-" as standard input; this has not yet been checked against a running
// ffplay, so if yours does not play, replace the final argument with
// "pipe:0".
func FFplay() *Command {
	return &Command{
		Name: "ffplay",
		Args: []string{"-nodisp", "-autoexit", "-loglevel", "error", "-"},
	}
}

// stderrTail is how much of the program's standard error Play keeps for the
// error message.
const stderrTail = 512

// stopGrace is how long Play waits for the program to exit after asking it to
// stop before killing it, and how long it waits for the program's pipes to
// close after it has exited.
const stopGrace = 500 * time.Millisecond

// Play runs the program with the audio on its standard input and returns when
// the program exits. When ctx is done first, the program is asked to stop,
// killed if it has not exited after a short grace period, and Play returns an
// error wrapping ctx.Err(). A program that is not installed gives an error
// wrapping [ErrPlayerNotFound]; a non-zero exit gives an error wrapping the
// [exec.ExitError] and ending with the program's last standard error output.
//
// The audio is read until EOF or until the program stops reading it. A Read
// blocked beyond ctx cannot be interrupted, so audio should honour ctx too.
func (c *Command) Play(ctx context.Context, audio io.Reader) error {
	path, err := exec.LookPath(c.Name)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrPlayerNotFound, c.Name)
	}

	stderr := &tail{limit: stderrTail}
	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Stdin = audio
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = stopGrace

	err = cmd.Run()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("htgotts: %s: %w", c.Name, ctx.Err())
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return fmt.Errorf("htgotts: %s: %w: %s", c.Name, err, msg)
	}
	return fmt.Errorf("htgotts: %s: %w", c.Name, err)
}

// interrupt asks the program to exit. SIGINT lets mplayer and ffplay shut
// down cleanly; Windows has no such signal, so the process is killed there.
func interrupt(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(os.Interrupt)
}

// tail is a writer that keeps only the last limit bytes written to it.
type tail struct {
	limit int
	buf   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	switch {
	case len(p) >= t.limit:
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
	case len(t.buf)+len(p) > t.limit:
		drop := len(t.buf) + len(p) - t.limit
		t.buf = append(t.buf[:0], t.buf[drop:]...)
		t.buf = append(t.buf, p...)
	default:
		t.buf = append(t.buf, p...)
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
