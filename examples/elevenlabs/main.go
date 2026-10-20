// Command elevenlabs speaks a sentence through the ElevenLabs text to speech
// API, or saves it as an MP3 file. The API key comes from the
// ELEVENLABS_API_KEY environment variable, and every request is billed, by
// characters, to its account:
//
//	export ELEVENLABS_API_KEY=...
//	go run ./examples/elevenlabs -voice EXAVITQu4vr4xnSDxMaL -model eleven_flash_v2_5 "Szia, világ!"
//	go run ./examples/elevenlabs -save audio/szia.mp3 "Szia, világ!"
//
// The multilingual models read the text in its own language; -language
// enforces one, as an ISO 639-1 code, with the models that support it.
// Playback goes through mplayer and keeps the MP3 in the audio folder; the
// cache file name carries the voice, the model, the language and the speed,
// so the same sentence spoken differently is requested, and billed, again.
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
	"github.com/hegedustibor/htgo-tts/v2/engine/elevenlabs"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	voice := flag.String("voice", string(elevenlabs.DefaultVoice), "voice ID, such as JBFqnCBsd6RMkjVDRZzb (George) or EXAVITQu4vr4xnSDxMaL (Sarah)")
	model := flag.String("model", string(elevenlabs.DefaultModel), "model, such as eleven_multilingual_v2, eleven_flash_v2_5 or eleven_v4")
	language := flag.String("language", "", "language to enforce, an ISO 639-1 code such as hu or de")
	speed := flag.Float64("speed", 0, "speaking speed from 0.7 to 1.2; 0 keeps the voice's own pace")
	save := flag.String("save", "", "write an MP3 file to this path instead of playing the speech")
	timeout := flag.Duration("timeout", time.Minute, "time limit for the request and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Szia, világ!"
	}
	if os.Getenv(elevenlabs.EnvKey) == "" {
		return fmt.Errorf("set %s; elevenlabs.WithKey would pass it in code", elevenlabs.EnvKey)
	}

	// The key is not among the options: the engine reads the environment
	// itself.
	opts := []elevenlabs.Option{
		elevenlabs.WithVoice(elevenlabs.Voice(*voice)),
		elevenlabs.WithModel(elevenlabs.Model(*model)),
	}
	if *language != "" {
		opts = append(opts, elevenlabs.WithLanguage(*language))
	}
	if *speed != 0 {
		opts = append(opts, elevenlabs.WithSpeed(*speed))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *save != "" {
		// No player is involved, so this works without mplayer; an error of
		// the engine, such as a rejected key, surfaces here.
		tts, err := htgotts.New(elevenlabs.New(opts...))
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

	tts, err := htgotts.New(elevenlabs.New(opts...), htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}
	return tts.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: elevenlabs [flags] text...")
	flag.PrintDefaults()
}
