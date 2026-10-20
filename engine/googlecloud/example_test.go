package googlecloud_test

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/hegedustibor/htgo-tts/v2/engine/googlecloud"
)

// The example speaks a Hungarian sentence with the WaveNet voice, a little
// faster than the voice's own pace, and writes the MP3 into a file. It needs
// network access and an API key in GOOGLE_CLOUD_API_KEY, and every request
// is billed, so it is compiled but not run by go test.
func ExampleNew() {
	tts := googlecloud.New(
		googlecloud.WithVoice(googlecloud.VoiceHuHUWavenetB),
		googlecloud.WithSpeakingRate(1.1),
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
