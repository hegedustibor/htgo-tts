// Command save writes the speech of a sentence into a file without playing
// it, so it needs neither mplayer nor a sound card, only network access:
//
//	go run ./examples/save -lang hu -out audio/szia "Szia, világ!"
//
// Save adds the engine's extension when the path has none, so the command
// above writes audio/szia.mp3, and it creates the folder when needed. The
// file appears under its name only once the audio is complete: a failed
// download leaves no half-written file behind.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	lang := flag.String("lang", string(voices.English), "language of the Google Translate engine, such as en, en-GB or hu")
	out := flag.String("out", "audio/hello", "file to write; the .mp3 extension is added when the path has none")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit for the download")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello, world!"
	}

	// No player is involved: New does not look for mplayer, and Save never
	// plays, so this runs on a machine without any player installed.
	tts, err := htgotts.New(google.New(voices.Language(*lang)))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	path, err := tts.Save(ctx, text, *out)
	if err != nil {
		return err
	}
	fmt.Println("written", path)
	return nil
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: save [flags] text...")
	flag.PrintDefaults()
}
