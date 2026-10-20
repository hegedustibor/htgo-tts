// Command custom-engine shows how to write an engine of your own: a type
// with the three methods of engine.Engine, which htgotts.New accepts in place
// of the Google Translate and OpenAI engines. This one fetches the speech
// from an HTTP server that takes the text in a query parameter and answers
// with the audio, as the /api/tts endpoint of a Coqui TTS server or the root
// path of Piper's HTTP server does:
//
//	go run ./examples/custom-engine -url http://localhost:5002/api/tts -format wav "Hello, world!"
//
// The speech is played with mplayer, which plays WAV as well as MP3; the
// native player would need a server that answers with MP3. Adapt the request
// in Synthesize to the service you use, for example Azure Speech or
// ElevenLabs; the shape of the engine stays the same.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	endpoint := flag.String("url", "http://localhost:5002/api/tts", "endpoint that takes the text in its text query parameter and answers with audio; other parameters the server needs, such as a voice, can be part of it")
	format := flag.String("format", "wav", "format the endpoint answers with: wav, mp3, opus, aac, flac or pcm")
	name := flag.String("name", "custom", "name of the server's voice or model, which keeps its cache files apart from those of other engines")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit for the request and the playback together")
	flag.Usage = usage
	flag.Parse()

	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello from an engine of my own!"
	}

	e := &httpEngine{
		endpoint: *endpoint,
		format:   engine.Format(*format),
		name:     *name,
		client:   http.DefaultClient,
	}

	// The Speech treats this engine exactly like the built-in ones. Its
	// cache files are named after Fingerprint and Format, so they never
	// clash with the files of the Google Translate engine in the same
	// folder, and Save would add the format's extension.
	tts, err := htgotts.New(e, htgotts.WithFolder("audio"))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return tts.Speak(ctx, text)
}

// httpEngine is an engine.Engine that fetches the speech with
// GET <endpoint>?text=<text> and streams the response body.
type httpEngine struct {
	endpoint string        // the URL, with any query parameters the server needs
	format   engine.Format // what the server answers with
	name     string        // the voice or model, for Fingerprint
	client   *http.Client
}

// Synthesize asks the endpoint for the audio of text and returns the response
// body as the stream, so playback starts while the audio is still arriving;
// the caller closes it. The contract of engine.Engine: engine.ErrEmptyText for
// empty text, an error wrapping ctx.Err() when ctx is done, which the error
// of the HTTP client does, and *engine.HTTPError when the server answers with
// something other than audio.
func (e *httpEngine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if strings.TrimSpace(text) == "" {
		return nil, engine.ErrEmptyText
	}

	u, err := url.Parse(e.endpoint)
	if err != nil {
		return nil, fmt.Errorf("custom engine: %w", err)
	}
	q := u.Query()
	q.Set("text", text)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("custom engine: %w", err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom engine: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		u.RawQuery, u.User = "", nil // neither the text nor a credential belongs in an error
		return nil, fmt.Errorf("custom engine: %w", &engine.HTTPError{
			StatusCode: resp.StatusCode,
			URL:        u.String(),
			Message:    strings.Join(strings.Fields(string(body)), " "),
		})
	}
	return resp.Body, nil
}

// Format reports the encoding the server answers with. The Speech uses it for
// the extension of cached and saved files, and the caller uses it to pick a
// player that understands it.
func (e *httpEngine) Format() engine.Format { return e.format }

// Fingerprint names the engine and the settings that change the sound, so
// that cached audio of this engine, or of this voice, is never mistaken for
// another's. It ends up in file names: keep it short, and keep secrets and
// URLs out of it.
func (e *httpEngine) Fingerprint() string { return "custom-" + e.name }

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: custom-engine [flags] text...")
	flag.PrintDefaults()
}
