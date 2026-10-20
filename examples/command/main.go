// Command command speaks a sentence with a speech program installed on this
// machine, offline and without an API key: the say command of macOS, which
// is the default there, espeak-ng, the default elsewhere, piper, or any
// program given with -cmd. The speech is played with mplayer, which plays
// the WAV these programs produce, or saved to a file with -save, which needs
// no player:
//
//	go run ./examples/command "Hello, world!"
//	go run ./examples/command -program espeak-ng -voice en-gb "Hello, world!"
//	go run ./examples/command -program piper -model en_US-lessac-medium.onnx "Hello, world!"
//	go run ./examples/command -cmd "pico2wave -l en-GB -w {out} {text}" "Hello, world!"
//	go run ./examples/command -save audio/hello "Hello, world!"
//
// With -cmd the program and its arguments are separated by spaces, so an
// argument with a space in it needs command.New in code; {text} and {out}
// are the placeholders of the command package, and -format names the format
// the program produces.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/engine/command"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	program := flag.String("program", defaultProgram(), "ready-made engine to use: say (macOS), espeak-ng or piper")
	voice := flag.String("voice", "", "voice for say or espeak-ng, such as Samantha or en-gb; empty for the program's default")
	model := flag.String("model", "", "voice model for piper, such as en_US-lessac-medium.onnx")
	cmdline := flag.String("cmd", "", "any program with its arguments instead of -program, with {text} and {out} as placeholders")
	format := flag.String("format", "wav", "format the -cmd program produces: wav, mp3, opus, aac, flac or pcm")
	save := flag.String("save", "", "write the audio to this path instead of playing it; the extension is added when the path has none")
	timeout := flag.Duration("timeout", time.Minute, "time limit for the synthesis and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello, world!"
	}

	e, err := newEngine(*program, *voice, *model, *cmdline, *format)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *save != "" {
		// No player is involved, so this works without mplayer; an error of
		// the engine, such as a program that is not installed, surfaces
		// here.
		tts, err := htgotts.New(e)
		if err != nil {
			return err
		}
		path, err := tts.Save(ctx, text, *save)
		if err != nil {
			return err
		}
		fmt.Println("written", path)
		return nil
	}

	// mplayer is the default player and plays WAV, so no WithPlayer is
	// needed; the native player would not do, as it plays MP3 only.
	tts, err := htgotts.New(e)
	if err != nil {
		return err
	}
	return tts.Speak(ctx, text)
}

// newEngine returns the engine the flags ask for: the program of -cmd, or
// one of the ready-made engines.
func newEngine(program, voice, model, cmdline, format string) (*command.Engine, error) {
	if cmdline != "" {
		fields := strings.Fields(cmdline)
		if len(fields) == 0 {
			return nil, errors.New("-cmd names no program")
		}
		return command.New(engine.Format(format), fields[0], fields[1:]...), nil
	}
	switch program {
	case "say":
		return command.Say(voice), nil
	case "espeak-ng":
		return command.ESpeakNG(voice), nil
	case "piper":
		if model == "" {
			return nil, errors.New("piper needs -model, the voice model to speak with")
		}
		return command.Piper(model), nil
	}
	return nil, fmt.Errorf("unknown program %q: use say, espeak-ng or piper, or give a command with -cmd", program)
}

// defaultProgram is say on macOS, where it is part of the system, and
// espeak-ng elsewhere.
func defaultProgram() string {
	if runtime.GOOS == "darwin" {
		return "say"
	}
	return "espeak-ng"
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: command [flags] text...")
	flag.PrintDefaults()
}
