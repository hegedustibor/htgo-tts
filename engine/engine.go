// Package engine defines the text-to-speech engine contract that the root
// htgotts package drives, together with the audio formats and the errors
// that every engine shares.
//
// An engine turns text into a stream of audio. The module ships engines in
// its subpackages, such as engine/google for the Google Translate endpoint;
// any type that implements Engine, for example one that calls Azure Speech or
// ElevenLabs, can be used in their place.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Engine is a text-to-speech backend.
//
// An Engine must be safe for concurrent use.
type Engine interface {
	// Synthesize converts text into audio and returns it as a stream, so
	// that playback can start before the whole utterance has been produced.
	// The audio is encoded as reported by Format. The caller must Close the
	// returned reader, which also stops any work still in progress.
	//
	// Synthesize returns ErrEmptyText when text is empty or contains only
	// white space, and stops as soon as ctx is done; an unexpected answer
	// from the speech service is reported as *HTTPError. Errors are wrapped,
	// so errors.Is and errors.As work on them.
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)

	// Format reports the audio encoding that Synthesize produces. It decides
	// the file extension of saved and cached audio and which players can
	// play it.
	Format() Format

	// Fingerprint identifies the engine together with every setting that
	// changes the sound of the audio, such as the language, the model, the
	// voice or the speed. It becomes part of cache file names, so it must be
	// short, stable and safe in a file name, and it must never contain
	// secrets such as API keys or endpoint URLs.
	Fingerprint() string
}

// Format is an audio encoding, named by its usual file extension.
type Format string

// The audio formats that engines can produce.
const (
	FormatMP3  Format = "mp3"
	FormatWAV  Format = "wav"
	FormatOpus Format = "opus"
	FormatAAC  Format = "aac"
	FormatFLAC Format = "flac"
	FormatPCM  Format = "pcm"
)

// Ext returns the file extension of the format with its leading dot, for
// example ".mp3".
func (f Format) Ext() string { return "." + string(f) }

// ErrEmptyText is returned by Synthesize when the text is empty or contains
// only white space.
var ErrEmptyText = errors.New("htgotts: empty text")

// HTTPError reports that the speech service answered a request with
// something other than the expected audio: a status other than 200 OK, or a
// body of another content type, typically an HTML error page.
type HTTPError struct {
	// StatusCode is the HTTP status code of the response.
	StatusCode int
	// URL is the requested URL without its query string, user information
	// and fragment, so that neither the spoken text nor a credential ends up
	// in logs.
	URL string
	// Message describes the answer: the beginning of the response body, at
	// most about 200 bytes with white space collapsed, preceded by a note
	// about the content type when the status was fine but the body was not
	// the expected kind of data.
	Message string
}

// Error formats the error as "<url>: HTTP <code> <status text>: <message>".
func (e *HTTPError) Error() string {
	var b strings.Builder
	if e.URL != "" {
		b.WriteString(e.URL)
		b.WriteString(": ")
	}
	fmt.Fprintf(&b, "HTTP %d", e.StatusCode)
	if text := http.StatusText(e.StatusCode); text != "" {
		b.WriteString(" ")
		b.WriteString(text)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}
