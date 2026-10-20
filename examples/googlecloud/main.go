// Command googlecloud speaks a sentence through the Google Cloud
// Text-to-Speech service, or saves it as a WAV file. The API key comes from
// the GOOGLE_CLOUD_API_KEY environment variable, and every request is billed
// to the project of the key:
//
//	export GOOGLE_CLOUD_API_KEY=...
//	go run ./examples/googlecloud -voice hu-HU-Wavenet-B -rate 1.1 "Szia, világ!"
//	go run ./examples/googlecloud -save audio/szia.wav "Szia, világ!"
//
// The language of the speech follows the voice, so a Hungarian voice reads
// Hungarian text. Playback goes through mplayer and keeps the MP3 in the
// audio folder; the cache file name carries the voice, the rate and the
// pitch, so the same sentence spoken differently is requested, and billed,
// again.
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
	"github.com/hegedustibor/htgo-tts/v2/engine/googlecloud"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	voice := flag.String("voice", string(googlecloud.VoiceHuHUWavenetB), "voice, such as hu-HU-Wavenet-B or en-US-Neural2-C")
	rate := flag.Float64("rate", 1, "speaking rate as a factor of the voice's own, between 0.25 and 2")
	pitch := flag.Float64("pitch", 0, "pitch change in semitones, between -20 and 20")
	save := flag.String("save", "", "write a WAV file to this path instead of playing the speech")
	timeout := flag.Duration("timeout", time.Minute, "time limit for the request and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Szia, világ!"
	}
	if os.Getenv(googlecloud.EnvKey) == "" {
		return fmt.Errorf("set %s; googlecloud.WithKey would pass it in code", googlecloud.EnvKey)
	}

	// options returns the settings of an engine that answers in format. The
	// key is not among them: the engine reads the environment itself.
	options := func(format engine.Format) []googlecloud.Option {
		return []googlecloud.Option{
			googlecloud.WithVoice(googlecloud.Voice(*voice)),
			googlecloud.WithSpeakingRate(*rate),
			googlecloud.WithPitch(*pitch),
			googlecloud.WithFormat(format),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *save != "" {
		// No player is involved, so this works without mplayer; an error of
		// the engine, such as a rejected key, surfaces here.
		wav, err := htgotts.New(googlecloud.New(options(engine.FormatWAV)...))
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

	mp3, err := htgotts.New(googlecloud.New(options(engine.FormatMP3)...), htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}
	return mp3.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: googlecloud [flags] text...")
	flag.PrintDefaults()
}
