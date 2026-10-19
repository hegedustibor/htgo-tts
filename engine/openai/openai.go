// Package openai implements an engine.Engine on top of the OpenAI speech
// endpoint, POST /v1/audio/speech: a paid service that speaks any language
// with a choice of models and voices, at a chosen speed, as MP3 or another
// format, and, with the gpt-4o-mini-tts model, in a tone described in words.
//
// Every request is billed to the account of the API key, which comes from
// WithAPIKey or from the OPENAI_API_KEY environment variable. The key only
// ever travels in the Authorization header: it is not part of the engine's
// fingerprint and never appears in an error.
//
// Services that offer the same endpoint, such as Azure OpenAI or a speech
// server running on the local machine, are reached with WithBaseURL. For
// Azure OpenAI the base URL is the full deployment URL, of the form
// https://<resource>.openai.azure.com/openai/deployments/<deployment>, with
// its api-version query parameter; the service's own authentication, such as
// an api-key header, is the caller's business, for example through the
// transport of a client given with WithHTTPClient.
//
// The endpoint accepts 4096 characters per request; longer text is split at
// sentence and clause boundaries and requested piece by piece while the
// audio already streams to the caller. The MP3 output is sampled at 24 kHz,
// which the native player of this module plays; the other formats need a
// command-line player such as mplayer or ffplay.
package openai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/internal/chunk"
	"github.com/hegedustibor/htgo-tts/v2/internal/httpx"
)

const (
	// DefaultBaseURL is the base URL of the OpenAI API; the engine posts to
	// its /audio/speech path.
	DefaultBaseURL = "https://api.openai.com/v1"

	// DefaultTimeout bounds each request to the endpoint when the engine
	// uses its own HTTP client.
	DefaultTimeout = 60 * time.Second

	// EnvAPIKey is the environment variable that supplies the API key when
	// WithAPIKey is not given.
	EnvAPIKey = "OPENAI_API_KEY"

	// MinSpeed and MaxSpeed bound the speed factor of WithSpeed.
	MinSpeed = 0.25
	MaxSpeed = 4.0

	// speechPath is the path of the speech endpoint under the base URL.
	speechPath = "audio/speech"

	// maxInputChars is the longest text the endpoint accepts in one request.
	maxInputChars = 4096

	// maxErrorBodyBytes bounds the part of an error response that is read to
	// find the message of an OpenAI error document.
	maxErrorBodyBytes = 32 << 10

	// maxMessageBytes bounds the message taken from an error document.
	maxMessageBytes = 500
)

// Model is the name of a speech model, as the API expects it in the "model"
// field. It is a string, so a model that this package has no constant for,
// such as a newer release or a model of an OpenAI-compatible server, can be
// used as Model("name") without waiting for a new version of the package.
type Model string

// The speech models of the OpenAI API at the time of writing.
const (
	// ModelTTS1 is the fastest and cheapest model, and the default.
	ModelTTS1 Model = "tts-1"
	// ModelTTS1HD trades speed for audio quality.
	ModelTTS1HD Model = "tts-1-hd"
	// ModelGPT4oMiniTTS is the newest model, and the only one of the three
	// that follows WithInstructions.
	ModelGPT4oMiniTTS Model = "gpt-4o-mini-tts"
)

// Voice is the name of a voice, as the API expects it in the "voice" field.
// It is a string, so a voice that this package has no constant for, such as
// one added later or a voice of an OpenAI-compatible server, can be used as
// Voice("name") without waiting for a new version of the package.
type Voice string

// The built-in voices of the OpenAI API at the time of writing; every model
// speaks every one of them. OpenAI recommends VoiceMarin and VoiceCedar for
// the best quality.
const (
	VoiceAlloy   Voice = "alloy"
	VoiceAsh     Voice = "ash"
	VoiceBallad  Voice = "ballad"
	VoiceCoral   Voice = "coral"
	VoiceEcho    Voice = "echo"
	VoiceFable   Voice = "fable"
	VoiceNova    Voice = "nova"
	VoiceOnyx    Voice = "onyx"
	VoiceSage    Voice = "sage"
	VoiceShimmer Voice = "shimmer"
	VoiceVerse   Voice = "verse"
	VoiceMarin   Voice = "marin"
	VoiceCedar   Voice = "cedar"
)

// Engine speaks text through the OpenAI speech endpoint. Create one with
// New. An Engine is safe for concurrent use.
type Engine struct {
	apiKey       string
	endpoint     string // the speech endpoint: <base URL>/audio/speech
	model        Model
	voice        Voice
	speed        float64
	instructions string
	format       engine.Format
	client       *http.Client
	timeout      time.Duration
	timeoutSet   bool
	err          error // the first invalid argument, returned by every Synthesize
}

// Option configures an Engine; see the With functions.
type Option func(*Engine)

// New returns an engine that speaks with the tts-1 model in the alloy voice,
// at normal speed, as MP3; options change these and are applied in order.
//
// The API key comes from WithAPIKey or, without it, from the OPENAI_API_KEY
// environment variable, which New reads once.
//
// An invalid argument, such as a missing API key, a speed outside 0.25–4, a
// malformed base URL or an unknown format, does not make New fail: the
// engine remembers the first error and every call to Synthesize returns it,
// so that a configuration mistake surfaces at the first use instead of being
// ignored.
func New(opts ...Option) *Engine {
	e := &Engine{
		model:   ModelTTS1,
		voice:   VoiceAlloy,
		speed:   1,
		format:  engine.FormatMP3,
		timeout: DefaultTimeout,
	}
	WithBaseURL(DefaultBaseURL)(e)
	for _, opt := range opts {
		opt(e)
	}
	if e.apiKey == "" {
		e.apiKey = strings.TrimSpace(os.Getenv(EnvAPIKey))
	}
	if e.apiKey == "" {
		e.fail(errors.New("htgotts/openai: missing API key: use WithAPIKey or set " + EnvAPIKey))
	}
	if e.client == nil {
		// The timeout is applied per request in fetch, where it also covers
		// a client given with WithHTTPClient, so the client itself has none.
		e.client = httpx.NewClient(0, nil)
		return e
	}
	if !e.timeoutSet {
		e.timeout = 0
	}
	return e
}

// WithAPIKey authenticates the requests with key instead of the value of the
// OPENAI_API_KEY environment variable. White space around the key is
// dropped; an empty key means the environment variable, as without the
// option.
func WithAPIKey(key string) Option {
	return func(e *Engine) { e.apiKey = strings.TrimSpace(key) }
}

// WithBaseURL sends the requests to the speech endpoint under rawURL instead
// of DefaultBaseURL, that is to <rawURL>/audio/speech: to Azure OpenAI, to an
// OpenAI-compatible speech server on the local machine, or to a test server.
// A trailing slash is dropped, and query parameters of rawURL are kept in
// every request.
func WithBaseURL(rawURL string) Option {
	return func(e *Engine) {
		u, err := parseURL("base", rawURL, "http", "https")
		if err != nil {
			e.fail(err)
			return
		}
		e.endpoint = u.JoinPath(speechPath).String()
	}
}

// WithModel selects the speech model; the default is ModelTTS1.
func WithModel(model Model) Option {
	return func(e *Engine) {
		if model == "" {
			e.fail(errors.New("htgotts/openai: empty model"))
			return
		}
		e.model = model
	}
}

// WithVoice selects the voice; the default is VoiceAlloy.
func WithVoice(voice Voice) Option {
	return func(e *Engine) {
		if voice == "" {
			e.fail(errors.New("htgotts/openai: empty voice"))
			return
		}
		e.voice = voice
	}
}

// WithSpeed sets the speed of the speech as a factor between MinSpeed and
// MaxSpeed; 1, the default, is the normal speed and is not sent to the
// endpoint.
func WithSpeed(speed float64) Option {
	return func(e *Engine) {
		if math.IsNaN(speed) || speed < MinSpeed || speed > MaxSpeed {
			e.fail(fmt.Errorf("htgotts/openai: speed %v is not between %v and %v", speed, MinSpeed, MaxSpeed))
			return
		}
		e.speed = speed
	}
}

// WithInstructions describes how the text should be spoken, for example
// "Speak in a calm, friendly tone." The instructions go with every request;
// ModelGPT4oMiniTTS follows them, while ModelTTS1 and ModelTTS1HD reject a
// request that carries them. Because they change the sound of the audio, a
// hash of them is part of Fingerprint.
func WithInstructions(instructions string) Option {
	return func(e *Engine) { e.instructions = instructions }
}

// WithFormat selects the encoding of the audio: engine.FormatMP3, the
// default, or engine.FormatOpus, engine.FormatAAC, engine.FormatFLAC,
// engine.FormatWAV or engine.FormatPCM, the last one being raw 24 kHz 16-bit
// little-endian samples. The native player of this module plays MP3 only;
// the other formats need a command-line player.
func WithFormat(format engine.Format) Option {
	return func(e *Engine) {
		switch format {
		case engine.FormatMP3, engine.FormatOpus, engine.FormatAAC, engine.FormatFLAC, engine.FormatWAV, engine.FormatPCM:
			e.format = format
		default:
			e.fail(fmt.Errorf("htgotts/openai: unsupported format %q", format))
		}
	}
}

// WithHTTPClient makes the engine send its requests with client instead of
// its own one, for example to use a proxy, to add the headers that an
// OpenAI-compatible service needs, or to record the requests in a test. The
// engine's default timeout does not apply to such a client; an explicit
// WithTimeout does.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		if client == nil {
			e.fail(errors.New("htgotts/openai: nil HTTP client"))
			return
		}
		e.client = client
	}
}

// WithTimeout bounds every request to the endpoint, including reading its
// body, to d; zero means no limit. Without this option the engine's own
// client waits at most DefaultTimeout per request, while a client given with
// WithHTTPClient keeps its own settings.
func WithTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d < 0 {
			e.fail(fmt.Errorf("htgotts/openai: negative timeout %v", d))
			return
		}
		e.timeout = d
		e.timeoutSet = true
	}
}

// parseURL parses rawURL for the option named what and checks that it has a
// host and one of the given schemes. Its errors never contain the password
// that rawURL may carry.
func parseURL(what, rawURL string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("htgotts/openai: invalid %s URL: %w", what, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("htgotts/openai: invalid %s URL %q: missing host", what, u.Redacted())
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("htgotts/openai: invalid %s URL %q: unsupported scheme %q", what, u.Redacted(), u.Scheme)
}

func (e *Engine) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Format reports the format set with WithFormat, engine.FormatMP3 by default.
func (e *Engine) Format() engine.Format { return e.format }

// Fingerprint identifies the model, the voice, the speed and, when given, the
// instructions: "openai-<model>-<voice>-<speed>", for example
// "openai-tts-1-alloy-1" or "openai-gpt-4o-mini-tts-nova-1.25", followed by
// "-" and the first eight hex digits of the SHA-256 hash of the instructions,
// so that the same text spoken with other instructions gets a cache file of
// its own without the instructions themselves ending up in a file name. A
// character that does not belong in a file name is replaced by "-". The
// format is not part of the fingerprint, because cached files carry it as
// their extension.
func (e *Engine) Fingerprint() string {
	fp := "openai-" + string(e.model) + "-" + string(e.voice) + "-" + strconv.FormatFloat(e.speed, 'f', -1, 64)
	if e.instructions != "" {
		sum := sha256.Sum256([]byte(e.instructions))
		fp += "-" + hex.EncodeToString(sum[:4])
	}
	return sanitize(fp)
}

// sanitize keeps ASCII letters, digits, "-", "." and "_" and replaces every
// other character by "-", so that the result fits into a file name on every
// platform.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			return r
		}
		return '-'
	}, s)
}

// Synthesize requests the speech for text and returns it as a stream in the
// format of Format.
//
// Text longer than the 4096 characters the endpoint accepts in one request
// is split at sentence and clause boundaries, and the pieces are requested
// one after the other in the background while the caller reads: the first
// bytes arrive once the first piece has been received, and the audio of the
// pieces simply follows each other. That is sound for MP3 and raw PCM; with
// the container formats (WAV, FLAC, AAC, Opus) every piece carries a header
// of its own, so keep the text within one request when using them. A failed
// request ends the stream with its error, wrapped with the index of the
// piece; an answer other than audio is an *engine.HTTPError whose Message is
// the message of the OpenAI error document when there is one. The stream
// stops when ctx is done, with an error that wraps ctx.Err().
//
// Close on the returned reader cancels the request in flight, prevents any
// further one and returns once the background work has stopped. Call it even
// after reading the stream to its end.
func (e *Engine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if e.err != nil {
		return nil, e.err
	}
	chunks := chunk.Split(text, maxInputChars)
	if len(chunks) == 0 {
		return nil, engine.ErrEmptyText
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("htgotts/openai: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		pw.CloseWithError(e.download(ctx, chunks, pw))
	}()
	return &stream{PipeReader: pr, cancel: cancel, done: done}, nil
}

// download requests the chunks in order and writes their audio to w.
func (e *Engine) download(ctx context.Context, chunks []string, w io.Writer) error {
	for i, text := range chunks {
		if err := e.fetch(ctx, text, w); err != nil {
			return fmt.Errorf("htgotts/openai: chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// request is the JSON body of a speech request.
type request struct {
	Model          Model         `json:"model"`
	Input          string        `json:"input"`
	Voice          Voice         `json:"voice"`
	ResponseFormat engine.Format `json:"response_format"`
	Speed          float64       `json:"speed,omitempty"`
	Instructions   string        `json:"instructions,omitempty"`
}

// requestBody builds the body of the request for one chunk. The speed is
// sent only when it differs from the endpoint's default of 1, and the
// instructions only when there are any.
func (e *Engine) requestBody(text string) request {
	body := request{
		Model:          e.model,
		Input:          text,
		Voice:          e.voice,
		ResponseFormat: e.format,
		Instructions:   e.instructions,
	}
	if e.speed != 1 {
		body.Speed = e.speed
	}
	return body
}

// fetch requests the audio of one chunk and copies it to w.
func (e *Engine) fetch(ctx context.Context, text string, w io.Writer) error {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	body, err := json.Marshal(e.requestBody(text))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return orContextError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	if err := httpx.CheckResponse(resp, "audio/"); err != nil {
		return err
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return orContextError(ctx, err)
	}
	return nil
}

// apiError turns a response with a status other than 200 OK into an
// *engine.HTTPError whose Message is the message of the OpenAI error
// document in the body, when the body is one, and the beginning of the body
// otherwise.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	// httpx builds the error from a copy of the response that serves the
	// bytes read above; the original body is still closed by the caller.
	r := *resp
	r.Body = io.NopCloser(bytes.NewReader(raw))
	err := httpx.CheckResponse(&r, "")
	var httpErr *engine.HTTPError
	if msg := errorMessage(raw); msg != "" && errors.As(err, &httpErr) {
		httpErr.Message = msg
	}
	return err
}

// errorMessage returns the message of an OpenAI error document, a JSON body
// of the form {"error": {"message": "..."}}, with its white space collapsed
// and its length bounded, or "" when raw is not such a document.
func errorMessage(raw []byte) string {
	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return truncate(strings.Join(strings.Fields(doc.Error.Message), " "), maxMessageBytes)
}

// truncate shortens s to at most n bytes without splitting a UTF-8 sequence,
// marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s + "…"
}

// orContextError returns ctx.Err() when ctx is done, so that a request that
// failed because it was cancelled or timed out reports that cause, and err
// otherwise.
func orContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// stream is the reader that Synthesize returns: the read end of the pipe that
// the download goroutine fills.
type stream struct {
	*io.PipeReader
	cancel context.CancelFunc
	done   <-chan struct{}
}

// Close stops the download: it cancels the request in flight, makes the next
// write of the goroutine fail, and waits until the goroutine has returned, so
// that no request is sent after Close returns.
func (s *stream) Close() error {
	s.cancel()
	s.PipeReader.Close()
	<-s.done
	return nil
}
