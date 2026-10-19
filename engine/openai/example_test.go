package openai_test

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/hegedustibor/htgo-tts/v2/engine/openai"
)

// The example speaks a sentence with the gpt-4o-mini-tts model in the nova
// voice and writes the MP3 into a file. It needs network access and an API
// key in the OPENAI_API_KEY environment variable, and every request is
// billed, so it is compiled but not run by go test.
func ExampleNew() {
	tts := openai.New(
		openai.WithModel(openai.ModelGPT4oMiniTTS),
		openai.WithVoice(openai.VoiceNova),
		openai.WithInstructions("Speak in a calm, friendly tone."),
	)

	audio, err := tts.Synthesize(context.Background(), "Hello, world!")
	if err != nil {
		log.Fatal(err)
	}
	defer audio.Close()

	f, err := os.Create("hello" + tts.Format().Ext())
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	if _, err := io.Copy(f, audio); err != nil {
		log.Fatal(err)
	}
}
