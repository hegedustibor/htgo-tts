// Command azure speaks a sentence through the Azure Speech service, or saves
// it as a WAV file. The resource key and its region come from the
// AZURE_SPEECH_KEY and AZURE_SPEECH_REGION environment variables, and every
// request is billed to that resource:
//
//	export AZURE_SPEECH_KEY=...
//	export AZURE_SPEECH_REGION=westeurope
//	go run ./examples/azure -voice hu-HU-NoemiNeural -style cheerful "Szia, világ!"
//	go run ./examples/azure -save audio/szia.wav "Szia, világ!"
//
// The language of the speech follows the voice, so a Hungarian voice reads
// Hungarian text. Playback goes through mplayer and keeps the MP3 in the
// audio folder; the cache file name carries the voice, the style, the rate
// and the pitch, so the same sentence spoken differently is requested, and
// billed, again.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/engine/azure"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	voice := flag.String("voice", string(azure.VoiceHuHUNoemi), "voice, such as hu-HU-NoemiNeural or en-US-JennyNeural")
	style := flag.String("style", "", "speaking style the voice has, such as cheerful, sad or newscast")
	rate := flag.String("rate", "", "speaking rate, such as 0.9, +10% or slow")
	pitch := flag.String("pitch", "", "pitch, such as -2st, +50Hz or high")
	save := flag.String("save", "", "write a WAV file to this path instead of playing the speech")
	timeout := flag.Duration("timeout", time.Minute, "time limit for the request and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Szia, világ!"
	}
	if os.Getenv(azure.EnvKey) == "" || os.Getenv(azure.EnvRegion) == "" {
		return fmt.Errorf("set %s and %s; azure.WithKey and azure.WithRegion would pass them in code", azure.EnvKey, azure.EnvRegion)
	}

	// options returns the settings of an engine that answers in format. The
	// key and the region are not among them: the engine reads the
	// environment itself.
	options := func(format engine.Format) []azure.Option {
		opts := []azure.Option{azure.WithVoice(azure.Voice(*voice)), azure.WithFormat(format)}
		if *style != "" {
			opts = append(opts, azure.WithStyle(*style))
		}
		if *rate != "" {
			opts = append(opts, azure.WithRate(*rate))
		}
		if *pitch != "" {
			opts = append(opts, azure.WithPitch(*pitch))
		}
		return opts
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *save != "" {
		// No player is involved, so this works without mplayer; an error of
		// the engine, such as a rejected key, surfaces here.
		wav, err := htgotts.New(azure.New(options(engine.FormatWAV)...))
		if err != nil {
			return err
		}
		path, err := wav.Save(ctx, text, *save)
		if err != nil {
			return err
		}
		fmt.Println("written", path)
		return nil
	}

	mp3, err := htgotts.New(azure.New(options(engine.FormatMP3)...), htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}
	return mp3.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: azure [flags] text...")
	flag.PrintDefaults()
}
