// Command speak says a sentence through the Google Translate engine and
// mplayer, keeping the audio in the audio folder so that a sentence is
// downloaded only once. It needs network access and mplayer in PATH:
//
//	go run ./examples/speak -lang en-GB "Hello, world!"
//
// The context carries a time limit for the download and the playback
// together: when it expires, both stop and Speak returns the context's error.
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
	slow := flag.Bool("slow", false, "speak slowly")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit for the download and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello, world!"
	}

	var opts []google.Option
	if *slow {
		opts = append(opts, google.WithSlow())
	}

	// Without WithFolder no file would be written; with it, the audio of
	// every sentence is kept in the folder and served from there next time.
	// mplayer is the default player, so no WithPlayer is needed.
	tts, err := htgotts.New(google.New(voices.Language(*lang), opts...), htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return tts.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: speak [flags] text...")
	flag.PrintDefaults()
}
