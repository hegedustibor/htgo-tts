package google_test

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

// The example writes the Hungarian speech of a sentence into an MP3 file; it
// needs network access, so it is compiled but not run by go test.
func ExampleNew() {
	tts := google.New(voices.Hungarian, google.WithSlow())

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
