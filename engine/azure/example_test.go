package azure_test

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/hegedustibor/htgo-tts/v2/engine/azure"
)

// The example speaks a Hungarian sentence cheerfully with the Noémi voice
// and writes the MP3 into a file. It needs network access, a Speech resource
// key in AZURE_SPEECH_KEY and its region in AZURE_SPEECH_REGION, and every
// request is billed, so it is compiled but not run by go test.
func ExampleNew() {
	tts := azure.New(
		azure.WithVoice(azure.VoiceHuHUNoemi),
		azure.WithStyle("cheerful"),
		azure.WithRate("+10%"),
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
