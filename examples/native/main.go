// Command native plays speech through the sound card with the native player
// of the player/native package, so no external program is needed. It speaks
// its arguments one after the other, which shows that one process plays any
// number of sentences:
//
//	go run ./examples/native -lang en "First sentence." "Second sentence."
//
// The program builds without cgo. On Linux the machine that runs it needs the
// ALSA shared library (package libasound2, or libasound2t64 on Debian 13 and
// Ubuntu 24.04 and later); macOS and Windows need nothing extra. The native
// player plays MP3 only, which the Google Translate engine used here
// produces, as the other web engines of the module do by default.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/player/native"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	lang := flag.String("lang", string(voices.English), "language of the Google Translate engine, such as en, en-GB or hu")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit for each sentence, download and playback together")
	flag.Usage = usage
	flag.Parse()

	texts := flag.Args()
	if len(texts) == 0 {
		texts = []string{
			"The native player needs no external program.",
			"It plays as many sentences as you like.",
		}
	}

	tts, err := htgotts.New(
		google.New(voices.Language(*lang)),
		htgotts.WithPlayer(native.New()),
		htgotts.WithFolder("audio"),
	)
	if err != nil {
		return err
	}

	// The sound card is opened by the first Speak and reused by the rest.
	for _, text := range texts {
		if err := speak(tts, text, *timeout); err != nil {
			return err
		}
	}
	return nil
}

func speak(tts *htgotts.Speech, text string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return tts.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: native [flags] sentence...")
	flag.PrintDefaults()
}
