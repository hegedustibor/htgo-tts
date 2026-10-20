// Command openai speaks a sentence through the OpenAI speech API: it saves
// the speech as a WAV file and then plays it as MP3 through mplayer. The API
// key comes from the OPENAI_API_KEY environment variable, and every request
// is billed to its account:
//
//	export OPENAI_API_KEY=sk-...
//	go run ./examples/openai -model gpt-4o-mini-tts -voice nova "Hello, world!"
//
// The format is a setting of the engine, so the WAV and the MP3 come from two
// engines that share every other setting: model, voice, speed and, with the
// gpt-4o-mini-tts model, the instructions that describe how to speak.
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
	"github.com/hegedustibor/htgo-tts/v2/engine/openai"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	model := flag.String("model", string(openai.ModelTTS1), "speech model: tts-1, tts-1-hd or gpt-4o-mini-tts")
	voice := flag.String("voice", string(openai.VoiceAlloy), "voice: alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse, marin or cedar")
	speed := flag.Float64("speed", 1, "speed factor between 0.25 and 4")
	instructions := flag.String("instructions", "", "how to speak, such as \"Speak in a calm, friendly tone.\"; gpt-4o-mini-tts only")
	out := flag.String("out", "audio/hello.wav", "WAV file to write")
	timeout := flag.Duration("timeout", 2*time.Minute, "time limit for the two requests and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello, world!"
	}
	if os.Getenv(openai.EnvAPIKey) == "" {
		return fmt.Errorf("set %s to your API key; openai.WithAPIKey would pass it in code", openai.EnvAPIKey)
	}

	// options returns the settings of an engine that answers in format. The
	// key is not among them: the engine reads OPENAI_API_KEY itself.
	options := func(format engine.Format) []openai.Option {
		opts := []openai.Option{
			openai.WithModel(openai.Model(*model)),
			openai.WithVoice(openai.Voice(*voice)),
			openai.WithSpeed(*speed),
			openai.WithFormat(format),
		}
		if *instructions != "" {
			opts = append(opts, openai.WithInstructions(*instructions))
		}
		return opts
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Save the WAV. No player is involved, so this part works without
	// mplayer; an error of the engine, such as a rejected key, surfaces here.
	wav, err := htgotts.New(openai.New(options(engine.FormatWAV)...))
	if err != nil {
		return err
	}
	path, err := wav.Save(ctx, text, *out)
	if err != nil {
		return err
	}
	fmt.Println("written", path)

	// Play the MP3 through mplayer, keeping it in the audio folder. The
	// cache file name carries the model, the voice and the speed, so the
	// same sentence in another voice is requested, and billed, again.
	mp3, err := htgotts.New(openai.New(options(engine.FormatMP3)...), htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}
	return mp3.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: openai [flags] text...")
	flag.PrintDefaults()
}
