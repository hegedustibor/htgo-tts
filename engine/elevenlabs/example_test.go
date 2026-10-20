package elevenlabs_test

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/hegedustibor/htgo-tts/v2/engine/elevenlabs"
)

// The example speaks a Hungarian sentence with the Sarah voice through the
// Flash v2.5 model, a little faster than the voice's own pace, and writes the
// MP3 into a file. It needs network access and an API key in
// ELEVENLABS_API_KEY, and every request is billed, so it is compiled but not
// run by go test.
func ExampleNew() {
	tts := elevenlabs.New(
		elevenlabs.WithVoice(elevenlabs.VoiceSarah),
		elevenlabs.WithModel(elevenlabs.ModelFlashV25),
		elevenlabs.WithSpeed(1.1),
	)

	audio, err := tts.Synthesize(context.Background(), "Szia, világ!")
	if err != nil {
		log.Fatal(err)
	}
	defer audio.Close()

	f, err := os.Create("szia" + tts.Format().Ext())
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	if _, err := io.Copy(f, audio); err != nil {
		log.Fatal(err)
	}
}
