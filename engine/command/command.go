// Package command implements an engine.Engine that runs a speech program
// installed on the machine, such as the say command of macOS, espeak-ng or
// piper: the program turns the text into audio, so speech needs neither an
// API key nor a network connection and works offline.
//
// By default the text goes to the program's standard input and the audio
// comes back from its standard output as a stream, so playback can start
// before the program has finished. Two placeholders in the arguments change
// that: PlaceholderText, "{text}", passes the text as an argument and leaves
// standard input empty, and PlaceholderOut, "{out}", names a temporary file,
// with the extension of the format, that the program writes the audio to;
// the engine then waits for the program to exit, reads the file and deletes
// it when the stream is closed.
//
// Say, ESpeakNG and Piper are ready-made engines for say, espeak-ng and
// piper, all of which produce WAV; New takes any other program, and With
// applies the options. The native player of this module plays MP3 only, so
// the audio of these engines is played with a command-line player such as
// mplayer or ffplay, or saved with Save.
//
// A program that is not installed fails with ErrProgramNotFound; a non-zero
// exit is reported with the program's last standard error output; when the
// context is done, the program is interrupted, killed if it does not exit,
// and the error wraps the context's. The package uses only the standard
// library and runs on Linux, macOS and Windows; the programs themselves
// decide where they run.
package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

const (
	// PlaceholderText in an argument is replaced by the text, which then
	// does not go to the program's standard input. The text is one
	// argument, however long; a text that starts with "-" may look like an
	// option to the program, which "--" before the placeholder prevents
	// where the program supports it.
	PlaceholderText = "{text}"

	// PlaceholderOut in an argument is replaced by the path of a temporary
	// file that the program writes the audio to, named with the extension
	// of the format. The engine reads the file once the program has exited
	// and deletes it when the stream is closed.
	PlaceholderOut = "{out}"

	// stderrTail is how much of the program's standard error is kept for
	// the error message.
	stderrTail = 512

	// stopGrace is how long the engine waits for the program to exit after
	// interrupting it before killing it, and how long it waits for the
	// program's pipes to close after it has exited.
	stopGrace = 500 * time.Millisecond

	// outFile is the name of the temporary audio file, before the
	// extension.
	outFile = "speech"
)

// ErrProgramNotFound is returned by Synthesize when the program is not
// installed, that is, not found in PATH. The error message names it.
var ErrProgramNotFound = errors.New("htgotts/command: program not found")

// Engine runs a speech program. Create one with New, Say, ESpeakNG or Piper
// and change its settings with With. An Engine is safe for concurrent use:
// every call to Synthesize runs a process of its own.
type Engine struct {
	name    string
	args    []string
	format  engine.Format
	dir     string
	env     []string
	timeout time.Duration
	hasText bool  // an argument contains PlaceholderText
	hasOut  bool  // an argument contains PlaceholderOut
	err     error // the first invalid setting, returned by every Synthesize
}

// Option configures an Engine through With; see the With functions.
type Option func(*Engine)

// New returns an engine that runs the program name with the arguments and
// reports its audio as format. The name is looked up in PATH at every
// Synthesize unless it contains a path separator. The text goes to the
// program's standard input and the audio comes from its standard output,
// unless the arguments contain PlaceholderText or PlaceholderOut. For
// example, pico2wave takes the text as its last argument and writes a WAV
// file whose name must end in .wav:
//
//	e := command.New(engine.FormatWAV, "pico2wave", "-l", "en-GB", "-w", "{out}", "{text}")
//
// An empty name or an empty format does not make New fail: the engine
// remembers the error and every call to Synthesize returns it, so that a
// configuration mistake surfaces at the first use instead of being ignored.
func New(format engine.Format, name string, args ...string) *Engine {
	e := &Engine{name: name, args: slices.Clone(args), format: format}
	if strings.TrimSpace(name) == "" {
		e.fail(errors.New("htgotts/command: empty program name"))
	}
	if strings.TrimSpace(string(format)) == "" {
		e.fail(errors.New("htgotts/command: empty format"))
	}
	for _, a := range args {
		e.hasText = e.hasText || strings.Contains(a, PlaceholderText)
		e.hasOut = e.hasOut || strings.Contains(a, PlaceholderOut)
	}
	return e
}

// With returns a copy of the engine with the options applied in order. The
// engine itself stays as it is, so a ready-made engine can be varied:
//
//	e := command.ESpeakNG("en-gb").With(command.WithTimeout(30 * time.Second))
//
// An invalid option, such as a negative timeout, is remembered like an
// invalid argument of New and returned by every Synthesize of the copy.
func (e *Engine) With(opts ...Option) *Engine {
	c := *e
	c.args = slices.Clone(e.args)
	c.env = slices.Clone(e.env)
	for _, opt := range opts {
		opt(&c)
	}
	return &c
}

// WithDir runs the program in the directory dir instead of the current
// directory of the process, for a program that looks for its model or its
// data files there. An empty dir is an invalid setting.
func WithDir(dir string) Option {
	return func(e *Engine) {
		if strings.TrimSpace(dir) == "" {
			e.fail(errors.New("htgotts/command: empty directory"))
			return
		}
		e.dir = dir
	}
}

// WithEnv adds the variables, each of the form "NAME=value", to the
// environment of the program, which otherwise is the environment of the
// process; a variable that the environment already has takes the new value.
// For example "ESPEAK_DATA_PATH=/opt/espeak-ng-data" points espeak-ng at its
// data. An entry without a name or without "=" is an invalid setting.
func WithEnv(env []string) Option {
	return func(e *Engine) {
		for _, kv := range env {
			if name, _, ok := strings.Cut(kv, "="); !ok || name == "" {
				e.fail(fmt.Errorf("htgotts/command: invalid environment entry %q: expected NAME=value", kv))
				return
			}
		}
		e.env = append(e.env, env...)
	}
}

// WithTimeout bounds the run of the program to d; zero, the default, means
// no limit. When the time is up, the program is interrupted and the audio
// ends with an error wrapping context.DeadlineExceeded. In the default,
// streaming mode the run includes the time the caller takes to read the
// audio, because the program cannot finish before its output has been read.
// A negative d is an invalid setting.
func WithTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d < 0 {
			e.fail(fmt.Errorf("htgotts/command: negative timeout %v", d))
			return
		}
		e.timeout = d
	}
}

// Say returns an engine for the say command of macOS, which speaks with the
// voices of the system: it runs "say -v <voice> -o <file> --file-format=WAVE
// --data-format=LEI16@22050" with the text on standard input, and the audio
// is 16-bit mono WAV at 22.05 kHz. say writes the audio to a temporary file,
// because it cannot write a WAV to standard output, so playback starts once
// the whole text has been synthesized. The voice is a name from "say -v ?",
// such as "Samantha" or "Daniel"; an empty voice leaves the choice to the
// settings of the system. Checked on macOS 27: a voice that is not installed
// does not fail, say then speaks with the default voice and reports nothing.
//
// The audio needs a command-line player such as mplayer or ffplay, or Save;
// the native player of this module plays MP3 only.
func Say(voice string) *Engine {
	var args []string
	if voice = strings.TrimSpace(voice); voice != "" {
		args = append(args, "-v", voice)
	}
	args = append(args, "-o", PlaceholderOut, "--file-format=WAVE", "--data-format=LEI16@22050")
	return New(engine.FormatWAV, "say", args...)
}

// ESpeakNG returns an engine for espeak-ng, the open source synthesizer for
// over a hundred languages, packaged for Linux, available through Homebrew
// on macOS and as an installer on Windows: it runs "espeak-ng -v <voice>
// --stdin --stdout" with the text on standard input. The voice is a language
// or voice name from "espeak-ng --voices", such as "en-gb", "hu" or "en+f3"
// for a variant; an empty voice leaves the default, English. --stdin makes
// espeak-ng read the whole text at once instead of line by line, and
// --stdout writes the audio, 16-bit mono WAV at 22.05 kHz, to standard
// output as it is produced, so playback starts at once. The WAV header
// written to standard output carries placeholder lengths, because espeak-ng
// cannot seek back to fix them; players and Save cope, but a tool that
// trusts the header may get the duration wrong.
//
// The arguments come from the espeak-ng manual and source and have not been
// checked against a running espeak-ng. The audio needs a command-line player
// such as mplayer or ffplay, or Save; the native player of this module plays
// MP3 only.
func ESpeakNG(voice string) *Engine {
	var args []string
	if voice = strings.TrimSpace(voice); voice != "" {
		args = append(args, "-v", voice)
	}
	args = append(args, "--stdin", "--stdout")
	return New(engine.FormatWAV, "espeak-ng", args...)
}

// Piper returns an engine for piper, the neural synthesizer with voices for
// dozens of languages, released as a program for Linux, macOS and Windows
// and as a Python package (pip install piper-tts): it runs "piper --model
// <model> --output_file <file>" with the text on standard input, and the
// audio is WAV at the sample rate of the voice, 22.05 kHz for most of them,
// written to a temporary file, so playback starts once the whole text has
// been synthesized. The model is the path of the voice's .onnx file, with
// its .onnx.json next to it; the Python package also takes the name of a
// voice, such as "en_US-lessac-medium". An empty model is an invalid
// setting.
//
// The arguments come from the piper documentation and source, where both
// the program and the Python package accept them, and have not been checked
// against a running piper. The audio needs a command-line player such as
// mplayer or ffplay, or Save; the native player of this module plays MP3
// only.
func Piper(model string) *Engine {
	model = strings.TrimSpace(model)
	e := New(engine.FormatWAV, "piper", "--model", model, "--output_file", PlaceholderOut)
	if model == "" {
		e.fail(errors.New("htgotts/command: empty model"))
	}
	return e
}

func (e *Engine) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Format reports the format given to New, WAV for the ready-made engines.
func (e *Engine) Format() engine.Format { return e.format }

// Fingerprint identifies the program and its arguments:
// "command-<program>-<hash>", where the program is the base name of the
// name given to New and the hash is the first eight hex digits of the
// SHA-256 of the name and the arguments, with the placeholders as written,
// for example "command-say-9f86d081". Other arguments, such as another
// voice, give another fingerprint and so another cache file; the working
// directory, the environment and the timeout do not change the sound and
// are not part of it. A character that does not belong in a file name is
// replaced by "-".
func (e *Engine) Fingerprint() string {
	h := sha256.New()
	io.WriteString(h, e.name)
	for _, a := range e.args {
		h.Write([]byte{0})
		io.WriteString(h, a)
	}
	return sanitize("command-" + filepath.Base(e.name) + "-" + hex.EncodeToString(h.Sum(nil)[:4]))
}

// sanitize keeps ASCII letters, digits, "-", "." and "_" and replaces every
// other character by "-", so that the result fits into a file name on every
// platform.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			return r
		}
		return '-'
	}, s)
}

// Synthesize runs the program for text and returns its audio as a stream in
// the format of Format.
//
// Without PlaceholderOut among the arguments the stream is the program's
// standard output: the first bytes are readable as soon as the program
// writes them, and the stream ends when the program has exited, with the
// program's error in place of io.EOF when it exited with a non-zero status,
// so that a failure is never taken for the end of the audio. With
// PlaceholderOut, Synthesize returns once the program has exited and the
// stream reads the file it wrote.
//
// Close on the stream interrupts a program that is still running, kills it
// after half a second if it has not exited, waits for it and removes the
// temporary file; call it also after reading the stream to its end. A text
// that is empty or white space only is engine.ErrEmptyText; a context that
// is done ends the run with an error wrapping ctx.Err(); a program that is
// not installed is ErrProgramNotFound; a non-zero exit wraps the
// *exec.ExitError and ends with the program's last standard error output.
func (e *Engine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if e.err != nil {
		return nil, e.err
	}
	if strings.TrimSpace(text) == "" {
		return nil, engine.ErrEmptyText
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("htgotts/command: %w", err)
	}
	path, err := exec.LookPath(e.name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrProgramNotFound, e.name)
	}

	var cancel context.CancelFunc
	if e.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	var dir, out string
	if e.hasOut {
		dir, err = os.MkdirTemp("", "htgotts-")
		if err != nil {
			cancel()
			return nil, fmt.Errorf("htgotts/command: %w", err)
		}
		out = filepath.Join(dir, outFile+e.format.Ext())
	}

	stderr := &tail{limit: stderrTail}
	cmd := exec.CommandContext(ctx, path, e.expand(text, out)...)
	cmd.Dir = e.dir
	if e.env != nil {
		cmd.Env = append(os.Environ(), e.env...)
	}
	if !e.hasText {
		// exec writes the text from a goroutine of its own and closes the
		// pipe after it, so the program sees the end of its input.
		cmd.Stdin = strings.NewReader(text)
	}
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = stopGrace

	if e.hasOut {
		return e.runToFile(ctx, cancel, cmd, stderr, dir, out)
	}
	return e.stream(ctx, cancel, cmd, stderr)
}

// expand returns the arguments with the placeholders replaced, PlaceholderOut
// first, so that a text containing "{out}" stays as it is.
func (e *Engine) expand(text, out string) []string {
	args := make([]string, len(e.args))
	for i, a := range e.args {
		a = strings.ReplaceAll(a, PlaceholderOut, out)
		args[i] = strings.ReplaceAll(a, PlaceholderText, text)
	}
	return args
}

// runToFile runs the program to its end and returns the file it wrote to
// out, inside the temporary directory dir, which the stream's Close removes.
func (e *Engine) runToFile(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, stderr *tail, dir, out string) (io.ReadCloser, error) {
	defer cancel()
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		return nil, e.wrap(ctx, err, stderr)
	}
	f, err := os.Open(out)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("htgotts/command: %s wrote no audio file: %w", e.name, err)
	}
	return &file{f: f, dir: dir}, nil
}

// stream starts the program with its standard output on a pipe and returns
// a stream that reads the pipe. The program's exit is awaited in the
// background from the moment it starts, so that a successful exit before
// the caller has read everything still ends the stream with EOF, and a
// goroutine forwards the pipe to the stream and ends it with the exit
// error once the program has closed its output.
func (e *Engine) stream(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, stderr *tail) (io.ReadCloser, error) {
	r, w, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("htgotts/command: %w", err)
	}
	cmd.Stdout = w
	err = cmd.Start()
	// The program holds its own copy of the write end; ours must go so that
	// the read end reaches EOF when the program closes its output.
	w.Close()
	if err != nil {
		r.Close()
		cancel()
		return nil, e.wrap(ctx, err, stderr)
	}

	exited := make(chan error, 1)
	go func() { exited <- e.wrap(ctx, cmd.Wait(), stderr) }()

	pr, pw := io.Pipe()
	s := &stream{pr: pr, stdout: r, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		// Until the program closes its output, or Close closes either end.
		io.Copy(pw, r)
		err := <-exited
		cancel()
		r.Close()
		pw.CloseWithError(err)
	}()
	return s, nil
}

// wrap turns the error of a run into the engine's: the context's error when
// the context is done, otherwise err with the program's name and its last
// standard error output.
func (e *Engine) wrap(ctx context.Context, err error, stderr *tail) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("htgotts/command: %s: %w", e.name, ctxErr)
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return fmt.Errorf("htgotts/command: %s: %w: %s", e.name, err, msg)
	}
	return fmt.Errorf("htgotts/command: %s: %w", e.name, err)
}

// interrupt asks the program to exit. SIGINT lets it shut down cleanly;
// Windows has no such signal, so the process is killed there.
func interrupt(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(os.Interrupt)
}

// stream is the reader that Synthesize returns in the streaming mode: the
// program's standard output, forwarded through a pipe that ends with the
// program's exit error.
type stream struct {
	pr     *io.PipeReader
	stdout *os.File // the read end of the program's standard output
	cancel context.CancelFunc
	done   chan struct{} // closed once the forwarding goroutine has ended
}

func (s *stream) Read(p []byte) (int, error) { return s.pr.Read(p) }

// Close stops the program when it is still running: it is interrupted,
// killed after stopGrace if it has not exited, and waited for, so that no
// process is left behind when Close returns. A Read after Close fails.
func (s *stream) Close() error {
	s.cancel()
	// Unblock the forwarding goroutine, whether it is writing to the stream
	// or reading the output, which an orphaned subprocess may still hold.
	s.pr.Close()
	s.stdout.Close()
	<-s.done
	return nil
}

// file is the reader that Synthesize returns in the file mode: the audio
// file the program wrote, in a temporary directory of its own.
type file struct {
	f   *os.File
	dir string
}

func (f *file) Read(p []byte) (int, error) { return f.f.Read(p) }

// Close closes the file and removes it together with its directory. A second
// Close is harmless.
func (f *file) Close() error {
	err := f.f.Close()
	if errors.Is(err, os.ErrClosed) {
		err = nil
	}
	if rmErr := os.RemoveAll(f.dir); err == nil {
		err = rmErr
	}
	return err
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
