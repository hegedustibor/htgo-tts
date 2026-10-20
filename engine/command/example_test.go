package command_test

import (
	"context"
	"fmt"
	"log"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/engine/command"
	"github.com/hegedustibor/htgo-tts/v2/player"
)

// The example saves a sentence spoken by the say command of macOS as a WAV
// file. It needs no network and no player, but it runs a program and writes
// a file, so it is compiled but not run by go test.
func ExampleSay() {
	tts, err := htgotts.New(command.Say("Samantha"))
	if err != nil {
		log.Fatal(err)
	}

	path, err := tts.Save(context.Background(), "Hello, world!", "hello") // hello.wav
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("written", path)
}

// The example speaks through pico2wave, a program the package has no
// ready-made engine for: the text is its last argument and the WAV goes to
// the file named by -w, so both placeholders are used. ffplay plays the WAV;
// the native player would not, as it plays MP3 only.
func ExampleNew() {
	e := command.New(engine.FormatWAV, "pico2wave", "-l", "en-GB", "-w", "{out}", "{text}").
		With(command.WithTimeout(30 * time.Second))

	tts, err := htgotts.New(e, htgotts.WithPlayer(player.FFplay()))
	if err != nil {
		log.Fatal(err)
	}
	if err := tts.Speak(context.Background(), "Hello, world!"); err != nil {
		log.Fatal(err)
	}
}
