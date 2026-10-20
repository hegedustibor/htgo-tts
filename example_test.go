package htgotts_test

import (
	"context"
	"fmt"
	"log"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/engine/openai"
	"github.com/hegedustibor/htgo-tts/v2/player/native"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

// The example speaks an English sentence through the Google Translate engine
// and mplayer, keeping the audio in the audio folder so that a sentence is
// downloaded only once. It needs network access and mplayer, so it is
// compiled but not run by go test.
func Example() {
	tts, err := htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"))
	if err != nil {
		log.Fatal(err)
	}
	if err := tts.Speak(context.Background(), "Hello, world!"); err != nil {
		log.Fatal(err)
	}
}

// Switching to the OpenAI engine changes the line that creates the engine and
// nothing else. Here the native player plays the MP3 through the sound card,
// without an external program. The API key comes from the OPENAI_API_KEY
// environment variable, and every request is billed, so the example is
// compiled but not run by go test.
func ExampleNew_openai() {
	tts, err := htgotts.New(
		openai.New(openai.WithVoice(openai.VoiceNova)),
		htgotts.WithPlayer(native.New()),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := tts.Speak(context.Background(), "Hello, world!"); err != nil {
		log.Fatal(err)
	}
}

// Save writes the speech into a file, adding the engine's extension when the
// path has none, and creates the folder when needed. It needs network access,
// so it is compiled but not run by go test.
func ExampleSpeech_Save() {
	tts, err := htgotts.New(google.New(voices.Hungarian))
	if err != nil {
		log.Fatal(err)
	}
	path, err := tts.Save(context.Background(), "Szia, világ!", "out/szia")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(path) // out/szia.mp3
}

// Synthesize keeps the audio in memory: no file is written and no player is
// involved. It needs network access, so it is compiled but not run by go
// test.
func ExampleSpeech_Synthesize() {
	tts, err := htgotts.New(google.New(voices.English))
	if err != nil {
		log.Fatal(err)
	}
	mp3, err := tts.Synthesize(context.Background(), "Hello, world!")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d bytes of MP3\n", len(mp3))
}
