// Package elevenlabs implements an engine.Engine on top of the ElevenLabs text
// to speech API, POST /v1/text-to-speech/{voice_id}/stream: a paid service
// with a library of thousands of voices, voice cloning, and models that speak
// dozens of languages with a chosen stability, similarity, style, speed and
// output format.
//
// Every request is billed, by characters, to the account of the API key, which
// comes from WithKey or from the ELEVENLABS_API_KEY environment variable; the
// free tier of the service covers a number of characters per month. The key
// only ever travels in the xi-api-key header: it is not part of the engine's
// fingerprint and never appears in an error. WithBaseURL sends the requests to
// a data residency endpoint of the service, or to a test server.
//
// The voice is DefaultVoice, one of the premade voices that every account has,
// or any voice of the account by its ID; the model is eleven_multilingual_v2
// without WithModel. Text longer than a few thousand characters is split at
// sentence and clause boundaries and requested piece by piece while the audio
// already streams to the caller, each piece together with its neighbours as
// previous_text and next_text, so that the service keeps the speech continuous
// across the cut.
//
// The MP3 output is sampled at 44.1 kHz, not at the 24 kHz of the other web
// engines of this module. The native player opens the sound card at the sample
// rate of the first MP3 it plays, so one process cannot play this engine and
// those through it, unless WithOutputFormat asks for mp3_24000_48. The PCM and
// Opus formats need a command-line player such as mplayer or ffplay.
package elevenlabs

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
	// DefaultTimeout bounds each request to the API when the engine uses its
	// own HTTP client.
	DefaultTimeout = 60 * time.Second

	// EnvKey is the environment variable that supplies the API key when
	// WithKey is not given.
	EnvKey = "ELEVENLABS_API_KEY"

	// DefaultBaseURL is the base URL of the API without WithBaseURL. The
	// speech requests go to <base URL>/text-to-speech/<voice ID>/stream.
	DefaultBaseURL = "https://api.elevenlabs.io/v1"

	// DefaultVoice is the voice that New uses without WithVoice: George, the
	// voice of the service's own quick start.
	DefaultVoice = VoiceGeorge

	// DefaultModel is the model that New uses without WithModel; it is also
	// the default of the API.
	DefaultModel = ModelMultilingualV2

	// maxInputChars is the longest text sent in one request: half of the
	// 5,000 characters that eleven_v3, the model with the smallest limit,
	// accepts per request, so that a piece fits every model however the
	// service counts its characters, and so that the audio of one piece, two
	// to three minutes, stays well within the default timeout.
	maxInputChars = 2500

	// maxErrorBodyBytes bounds the part of an error response that is read to
	// find the message of an error document.
	maxErrorBodyBytes = 32 << 10

	// maxMessageBytes bounds the message taken from an error document.
	maxMessageBytes = 500
)

// Voice is the ID of a voice, the voice_id of the API, for example
// "JBFqnCBsd6RMkjVDRZzb". The constants below are the premade voices that
// every account has. The ID of any other voice, one cloned or designed in the
// account or one added from the voice library, comes from the voices endpoint
// of the API, GET /v1/voices, or from the voice's page in the ElevenLabs app,
// and is used as Voice("id").
type Voice string

// The premade voices of the service, as GET /v1/voices lists them. All of
// them speak English natively; the multilingual models speak the other
// languages with them.
const (
	VoiceAdam    Voice = "pNInz6obpgDQGcFmaJgB" // Adam: dominant, firm; American man, middle-aged
	VoiceAlice   Voice = "Xb7hH8MSUJpSbSDYk0k2" // Alice: clear, engaging educator; British woman, middle-aged
	VoiceBella   Voice = "hpp4J3VqNfWAUOO0d1Us" // Bella: professional, bright, warm; American woman, middle-aged
	VoiceBill    Voice = "pqHfZKP75CvOlQylNhV4" // Bill: wise, mature, balanced; American man, old
	VoiceBrian   Voice = "nPczCjzI2devNBz1zQrb" // Brian: deep, resonant and comforting; American man, middle-aged
	VoiceCallum  Voice = "N2lVS1w4EtoT3dr4eOWO" // Callum: husky trickster; American man, middle-aged
	VoiceCharlie Voice = "IKne3meq5aSn9XLyUdCD" // Charlie: deep, confident, energetic; Australian man, young
	VoiceChris   Voice = "iP95p4xoKVk53GoZ742B" // Chris: charming, down-to-earth; American man, middle-aged
	VoiceDaniel  Voice = "onwK4e9ZLuTAKqWW03F9" // Daniel: steady broadcaster; British man, middle-aged
	VoiceEric    Voice = "cjVigY5qzO86Huf0OWal" // Eric: smooth, trustworthy; American man, middle-aged
	VoiceGeorge  Voice = "JBFqnCBsd6RMkjVDRZzb" // George: warm, captivating storyteller; British man, middle-aged
	VoiceHarry   Voice = "SOYHLrjzK2X1ezoPC6cr" // Harry: fierce warrior; American man, young
	VoiceJessica Voice = "cgSgspJ2msm6clMCkdW9" // Jessica: playful, bright, warm; American woman, young
	VoiceLaura   Voice = "FGY2WhTYpPnrIDTdsKH5" // Laura: enthusiast, quirky attitude; American woman, young
	VoiceLiam    Voice = "TX3LPaxmHKxFdv7VOQHJ" // Liam: energetic social media creator; American man, young
	VoiceLily    Voice = "pFZP5JQG7iQjIQuC4Bku" // Lily: velvety actress; British woman, middle-aged
	VoiceMatilda Voice = "XrExE9yKIg1WjnnlVkGX" // Matilda: knowledgeable, professional; American woman, middle-aged
	VoiceRiver   Voice = "SAz9YHcvj6GT2YYXdXww" // River: relaxed, neutral, informative; American, middle-aged
	VoiceRoger   Voice = "CwhRBWXzGAHq8TQ4Fs17" // Roger: laid-back, casual, resonant; American man, middle-aged
	VoiceSarah   Voice = "EXAVITQu4vr4xnSDxMaL" // Sarah: mature, reassuring, confident; American woman, young
	VoiceWill    Voice = "bIHbv24MWmeRgasZH58o" // Will: relaxed optimist; American man, young
)

// Model is the ID of a model, the model_id of the API, for example
// "eleven_multilingual_v2". It is a string, so a model that the package has
// no constant for, such as a newer one, is Model("id"); the models endpoint of
// the API, GET /v1/models, lists them with their languages and their character
// limits per request.
type Model string

// The text to speech models of the service, with the limits that its
// documentation gives.
const (
	// ModelMultilingualV2 is the default of the API and of this package: an
	// emotionally aware model that speaks 29 languages and accepts 10,000
	// characters per request. The documentation lists language_code, the
	// setting of WithLanguage, as not supported for it.
	ModelMultilingualV2 Model = "eleven_multilingual_v2"

	// ModelFlashV25 answers in about 75 ms, speaks 32 languages, accepts
	// 40,000 characters per request and costs less per character.
	ModelFlashV25 Model = "eleven_flash_v2_5"

	// ModelFlashV2 is the English-only Flash model, with 30,000 characters
	// per request.
	ModelFlashV2 Model = "eleven_flash_v2"

	// ModelV3 is the expressive model of the third generation: over seventy
	// languages, audio tags such as [whispers] in the text, and 5,000
	// characters per request. The speed, similarity and speaker boost
	// settings are not available for it.
	ModelV3 Model = "eleven_v3"

	// ModelV3Conversational is the low-latency variant of ModelV3 for
	// dialogue.
	ModelV3Conversational Model = "eleven_v3_conversational"

	// ModelV4 is the newest generation: over ninety languages and 10,000
	// characters per request.
	ModelV4 Model = "eleven_v4"

	// ModelV4Turbo is the low-latency variant of ModelV4.
	ModelV4Turbo Model = "eleven_v4_turbo"

	// ModelTurboV25 and ModelTurboV2 are deprecated: the documentation calls
	// them equivalent to ModelFlashV25 and ModelFlashV2 with a higher latency
	// and recommends the Flash models in their place.
	ModelTurboV25 Model = "eleven_turbo_v2_5"
	ModelTurboV2  Model = "eleven_turbo_v2"
)

// outputFormats maps the formats of WithFormat to the names of the
// output_format query parameter.
var outputFormats = map[engine.Format]string{
	engine.FormatMP3:  "mp3_44100_128",
	engine.FormatPCM:  "pcm_24000",
	engine.FormatOpus: "opus_48000_64",
}

// Engine speaks text through the ElevenLabs API. Create one with New. An
// Engine is safe for concurrent use.
type Engine struct {
	key          string
	baseURL      *url.URL
	voice        Voice
	model        Model
	language     string
	format       engine.Format
	outputFormat string // the output_format query parameter
	stability    *float64
	similarity   *float64
	style        *float64
	speakerBoost *bool
	speed        *float64
	client       *http.Client
	timeout      time.Duration
	timeoutSet   bool
	err          error // the first invalid argument, returned by every Synthesize
}

// Option configures an Engine; see the With functions.
type Option func(*Engine)

// New returns an engine that speaks with DefaultVoice through DefaultModel,
// with the voice's own settings, as MP3; options change these and are applied
// in order.
//
// The API key comes from WithKey or, without it, from the ELEVENLABS_API_KEY
// environment variable, which New reads once.
//
// An invalid argument, such as a missing key, an empty voice, a setting
// outside its range, a format the service does not produce or a malformed
// base URL, does not make New fail: the engine remembers the first error and
// every call to Synthesize returns it, so that a configuration mistake
// surfaces at the first use instead of being ignored.
func New(opts ...Option) *Engine {
	e := &Engine{
		voice:        DefaultVoice,
		model:        DefaultModel,
		format:       engine.FormatMP3,
		outputFormat: outputFormats[engine.FormatMP3],
		timeout:      DefaultTimeout,
	}
	WithBaseURL(DefaultBaseURL)(e)
	for _, opt := range opts {
		opt(e)
	}
	if e.key == "" {
		e.key = strings.TrimSpace(os.Getenv(EnvKey))
	}
	if e.key == "" {
		e.fail(errors.New("htgotts/elevenlabs: missing API key: use WithKey or set " + EnvKey))
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

// WithKey authenticates the requests with the API key instead of the value of
// the ELEVENLABS_API_KEY environment variable. White space around the key is
// dropped; an empty key means the environment variable, as without the
// option.
func WithKey(key string) Option {
	return func(e *Engine) { e.key = strings.TrimSpace(key) }
}

// WithBaseURL sends the requests to another server with the same API, at
// <base URL>/text-to-speech/<voice ID>/stream, instead of DefaultBaseURL: to
// a data residency endpoint of the service, such as
// https://api.eu.residency.elevenlabs.io/v1, or to a test server. A trailing
// slash is dropped, and query parameters of the URL are kept on every
// request.
func WithBaseURL(rawURL string) Option {
	return func(e *Engine) {
		u, err := parseURL(strings.TrimSpace(rawURL), "http", "https")
		if err != nil {
			e.fail(err)
			return
		}
		u.Path = strings.TrimSuffix(u.Path, "/")
		u.RawPath = ""
		u.Fragment = ""
		u.RawFragment = ""
		e.baseURL = u
	}
}

// WithVoice selects the voice by its ID; the default is DefaultVoice. The
// constants of the package are the premade voices; the ID of any other voice
// of the account comes from the voices endpoint of the API or from the
// ElevenLabs app.
func WithVoice(voice Voice) Option {
	return func(e *Engine) {
		voice = Voice(strings.TrimSpace(string(voice)))
		if voice == "" {
			e.fail(errors.New("htgotts/elevenlabs: empty voice"))
			return
		}
		e.voice = voice
	}
}

// WithModel selects the model; the default is DefaultModel. The constants of
// the package are the documented models; a newer one is Model("id").
func WithModel(model Model) Option {
	return func(e *Engine) {
		model = Model(strings.TrimSpace(string(model)))
		if model == "" {
			e.fail(errors.New("htgotts/elevenlabs: empty model"))
			return
		}
		e.model = model
	}
}

// WithLanguage enforces the language of the speech and of the text
// normalization, the reading of numbers and dates, as an ISO 639-1 code such
// as "hu" or "de". It is sent as language_code, which the documentation
// describes for the Eleven v3 and v4 models, lists as not supported for
// eleven_multilingual_v2, and says is ignored by a model that does not
// support it. Without the option the model recognizes the language from the
// text.
func WithLanguage(language string) Option {
	return func(e *Engine) {
		language = strings.TrimSpace(language)
		if language == "" {
			e.fail(errors.New("htgotts/elevenlabs: empty language"))
			return
		}
		e.language = language
	}
}

// WithStability sets the stability of the voice, from 0 to 1: lower values
// give a broader emotional range, higher values a steadier and, at the top, a
// monotonous delivery. Without the option the service uses the setting stored
// with the voice; the default of the service is 0.5.
func WithStability(stability float64) Option {
	return func(e *Engine) { e.setting(&e.stability, "stability", stability, 0, 1) }
}

// WithSimilarity sets the similarity boost, from 0 to 1: how closely the
// speech should follow the original voice. Without the option the service
// uses the setting stored with the voice; the default of the service is
// 0.75. The setting is not available for the Eleven v3 models.
func WithSimilarity(similarity float64) Option {
	return func(e *Engine) { e.setting(&e.similarity, "similarity", similarity, 0, 1) }
}

// WithStyle sets the style exaggeration, from 0 to 1, which amplifies the
// style of the original speaker at the cost of some stability and latency.
// Without the option the service uses the setting stored with the voice; the
// default of the service is 0, which its documentation recommends.
func WithStyle(style float64) Option {
	return func(e *Engine) { e.setting(&e.style, "style", style, 0, 1) }
}

// WithSpeakerBoost turns the speaker boost on or off: on, the speech
// resembles the original speaker more closely, at the cost of a little
// latency. Without the option the service uses the setting stored with the
// voice; the default of the service is on. The setting is not available for
// the Eleven v3 models.
func WithSpeakerBoost(on bool) Option {
	return func(e *Engine) { e.speakerBoost = &on }
}

// WithSpeed sets the speaking speed, from 0.7 to 1.2, where 1 is the voice's
// natural pace; the documentation warns that values near the ends of the
// range may affect the quality of the speech. Without the option the service
// uses the setting stored with the voice; the default of the service is 1.
// The setting is not available for the Eleven v3 models.
func WithSpeed(speed float64) Option {
	return func(e *Engine) { e.setting(&e.speed, "speed", speed, 0.7, 1.2) }
}

// setting validates value for the voice setting called name, which must be
// between lo and hi, and stores it in dst.
func (e *Engine) setting(dst **float64, name string, value, lo, hi float64) {
	if math.IsNaN(value) || value < lo || value > hi {
		e.fail(fmt.Errorf("htgotts/elevenlabs: %s %v out of range: expected %v to %v", name, value, lo, hi))
		return
	}
	*dst = &value
}

// WithFormat selects the encoding of the audio: engine.FormatMP3, the
// default, at 44.1 kHz and 128 kbit/s; engine.FormatPCM, raw 16-bit
// little-endian mono samples at 24 kHz; or engine.FormatOpus at 48 kHz and
// 64 kbit/s. The streaming endpoint of the service does not produce WAV, AAC
// or FLAC; PCM is WAV without its header. The native player of this module
// plays MP3 only, at one sample rate per process; the other formats need a
// command-line player. WithOutputFormat selects any other format of the
// service, among them a 24 kHz MP3 that the native player can mix with the
// other engines.
func WithFormat(format engine.Format) Option {
	return func(e *Engine) {
		name, ok := outputFormats[format]
		if !ok {
			e.fail(fmt.Errorf("htgotts/elevenlabs: unsupported format %q: the service streams mp3, pcm and opus", format))
			return
		}
		e.format = format
		e.outputFormat = name
	}
}

// WithOutputFormat requests the audio in any format of the service by the
// name the output_format query parameter takes, "mp3_24000_48",
// "pcm_16000", "opus_48000_128" or "ulaw_8000" for example, together with the
// engine.Format that names the encoding: the extension of saved and cached
// files, and the hint for choosing a player. The documentation reserves the
// 192 kbit/s MP3 for the Creator plan and above and the 44.1 kHz PCM for the
// Pro plan and above.
func WithOutputFormat(name string, format engine.Format) Option {
	return func(e *Engine) {
		name = strings.TrimSpace(name)
		if name == "" {
			e.fail(errors.New("htgotts/elevenlabs: empty output format"))
			return
		}
		if format == "" {
			e.fail(fmt.Errorf("htgotts/elevenlabs: output format %q: empty engine format", name))
			return
		}
		e.outputFormat = name
		e.format = format
	}
}

// WithHTTPClient makes the engine send its requests with client instead of
// its own one, for example to use a proxy or to record the requests in a
// test. The engine's default timeout does not apply to such a client; an
// explicit WithTimeout does.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		if client == nil {
			e.fail(errors.New("htgotts/elevenlabs: nil HTTP client"))
			return
		}
		e.client = client
	}
}

// WithTimeout bounds every request to the API, including reading its body,
// to d; zero means no limit. Without this option the engine's own client
// waits at most DefaultTimeout per request, while a client given with
// WithHTTPClient keeps its own settings.
func WithTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d < 0 {
			e.fail(fmt.Errorf("htgotts/elevenlabs: negative timeout %v", d))
			return
		}
		e.timeout = d
		e.timeoutSet = true
	}
}

// parseURL parses rawURL for WithBaseURL and checks that it has a host and
// one of the given schemes. Its errors never contain the password that
// rawURL may carry.
func parseURL(rawURL string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("htgotts/elevenlabs: invalid base URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("htgotts/elevenlabs: invalid base URL %q: missing host", u.Redacted())
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("htgotts/elevenlabs: invalid base URL %q: unsupported scheme %q", u.Redacted(), u.Scheme)
}

func (e *Engine) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Format reports the format set with WithFormat or WithOutputFormat,
// engine.FormatMP3 by default.
func (e *Engine) Format() engine.Format { return e.format }

// Fingerprint identifies the voice, the model and every setting that changes
// the sound: "elevenlabs-<voice ID>-<model>", for example
// "elevenlabs-JBFqnCBsd6RMkjVDRZzb-eleven_multilingual_v2", followed by "-"
// and the output format's name when WithOutputFormat chose one other than the
// default for the format, and by "-" and the first eight hex digits of the
// SHA-256 hash of the stability, the similarity, the style, the speaker
// boost, the speed and the language when any of them is set, so that the same
// text spoken differently gets a cache file of its own. A character that does
// not belong in a file name is replaced by "-". Neither the key nor the base
// URL is part of it.
func (e *Engine) Fingerprint() string {
	fp := "elevenlabs-" + string(e.voice) + "-" + string(e.model)
	if e.outputFormat != outputFormats[e.format] {
		fp += "-" + e.outputFormat
	}
	if settings := e.settings(); settings != "" {
		sum := sha256.Sum256([]byte(settings))
		fp += "-" + hex.EncodeToString(sum[:4])
	}
	return sanitize(fp)
}

// settings serializes the voice settings and the language for the
// fingerprint, each in its own field, or returns "" when none of them is set.
func (e *Engine) settings() string {
	fields := []string{
		floatField(e.stability),
		floatField(e.similarity),
		floatField(e.style),
		boolField(e.speakerBoost),
		floatField(e.speed),
		e.language,
	}
	if strings.Join(fields, "") == "" {
		return ""
	}
	return strings.Join(fields, "\x00")
}

func floatField(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'g', -1, 64)
}

func boolField(b *bool) string {
	if b == nil {
		return ""
	}
	return strconv.FormatBool(*b)
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
// The models accept between 5,000 and 40,000 characters per request, so text
// longer than 2,500 characters is split at sentence and clause boundaries,
// and the pieces are requested one after the other in the background while
// the caller reads: the first bytes arrive once the first piece has been
// received, and the audio of the pieces simply follows each other. Every
// piece travels with its neighbours as previous_text and next_text, which the
// service uses to keep the delivery continuous across the cut. That is sound
// for MP3 and raw PCM; with Opus every piece is a stream of its own, so keep
// the text within one request when using it. A failed request ends the stream
// with its error, wrapped with the index of the piece; an answer other than
// audio is an *engine.HTTPError whose Message is the message of the service's
// error document when there is one. The stream stops when ctx is done, with
// an error that wraps ctx.Err().
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
		return nil, fmt.Errorf("htgotts/elevenlabs: %w", err)
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

// download requests the chunks in order, each with its neighbours as context,
// and writes their audio to w.
func (e *Engine) download(ctx context.Context, chunks []string, w io.Writer) error {
	for i, text := range chunks {
		var previous, next string
		if i > 0 {
			previous = chunks[i-1]
		}
		if i+1 < len(chunks) {
			next = chunks[i+1]
		}
		if err := e.fetch(ctx, text, previous, next, w); err != nil {
			return fmt.Errorf("htgotts/elevenlabs: chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// payload is the JSON body of a speech request. The fields that the engine
// does not set stay out of it, so that the service applies its own defaults
// and the settings stored with the voice.
type payload struct {
	Text          string         `json:"text"`
	ModelID       Model          `json:"model_id"`
	LanguageCode  string         `json:"language_code,omitempty"`
	VoiceSettings *voiceSettings `json:"voice_settings,omitempty"`
	PreviousText  string         `json:"previous_text,omitempty"`
	NextText      string         `json:"next_text,omitempty"`
}

// voiceSettings is the voice_settings object of a request; a nil field is
// left out, so a setting given as 0 is still sent.
type voiceSettings struct {
	Stability       *float64 `json:"stability,omitempty"`
	SimilarityBoost *float64 `json:"similarity_boost,omitempty"`
	Style           *float64 `json:"style,omitempty"`
	UseSpeakerBoost *bool    `json:"use_speaker_boost,omitempty"`
	Speed           *float64 `json:"speed,omitempty"`
}

// body returns the JSON body of the request for one chunk, with the
// neighbouring chunks, when there are any, as previous_text and next_text.
func (e *Engine) body(text, previous, next string) ([]byte, error) {
	p := payload{
		Text:         text,
		ModelID:      e.model,
		LanguageCode: e.language,
		PreviousText: previous,
		NextText:     next,
	}
	if e.stability != nil || e.similarity != nil || e.style != nil || e.speakerBoost != nil || e.speed != nil {
		p.VoiceSettings = &voiceSettings{
			Stability:       e.stability,
			SimilarityBoost: e.similarity,
			Style:           e.style,
			UseSpeakerBoost: e.speakerBoost,
			Speed:           e.speed,
		}
	}
	return json.Marshal(p)
}

// endpoint returns the URL of the speech request for the engine's voice,
// with the output format in its query. The voice ID is escaped as one path
// segment.
func (e *Engine) endpoint() string {
	u := *e.baseURL
	base := u.EscapedPath()
	u.Path += "/text-to-speech/" + string(e.voice) + "/stream"
	u.RawPath = base + "/text-to-speech/" + url.PathEscape(string(e.voice)) + "/stream"
	q := u.Query()
	q.Set("output_format", e.outputFormat)
	u.RawQuery = q.Encode()
	return u.String()
}

// fetch requests the audio of one chunk and copies it to w.
func (e *Engine) fetch(ctx context.Context, text, previous, next string, w io.Writer) error {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	body, err := e.body(text, previous, next)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", e.key)
	req.Header.Set("Content-Type", "application/json")
	if e.format == engine.FormatMP3 {
		req.Header.Set("Accept", "audio/mpeg")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return orContextError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	// The service names the audio type of MP3; a raw format may come as an
	// octet stream or without a type.
	if resp.Header.Get("Content-Type") != "" {
		if err := httpx.CheckResponse(resp, "audio/", "application/octet-stream"); err != nil {
			return err
		}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return orContextError(ctx, err)
	}
	return nil
}

// apiError turns a response with a status other than 200 OK into an
// *engine.HTTPError whose Message is the message of the service's error
// document in the body, when the body is one, and the beginning of the body
// otherwise.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	// httpx builds the error from a copy of the response that serves the
	// bytes read above; the original body is still closed by the caller.
	r := *resp
	r.Body = io.NopCloser(bytes.NewReader(raw))
	err := httpx.CheckResponse(&r)
	var httpErr *engine.HTTPError
	if msg := errorMessage(raw); msg != "" && errors.As(err, &httpErr) {
		httpErr.Message = msg
	}
	return err
}

// errorMessage returns the message of an error document of the service, with
// its white space collapsed and its length bounded, or "" when raw is not
// such a document. The service answers most errors with a detail object,
// {"detail": {"code": "invalid_api_key", "message": "..."}}, whose code names
// the cause (older documents carry it as status), and a request that fails
// validation with a detail list, {"detail": [{"loc": ["body", "text"], "msg":
// "...", "type": "..."}]}, one item per invalid field.
func errorMessage(raw []byte) string {
	var doc struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	detail := bytes.TrimSpace(doc.Detail)
	if len(detail) == 0 {
		return ""
	}
	var msg string
	switch detail[0] {
	case '{':
		var d struct {
			Code    string `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if json.Unmarshal(detail, &d) != nil {
			return ""
		}
		code := d.Code
		if code == "" {
			code = d.Status
		}
		msg = d.Message
		if code != "" && msg != "" {
			msg = code + ": " + msg
		} else if msg == "" {
			msg = code
		}
	case '[':
		var items []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		}
		if json.Unmarshal(detail, &items) != nil {
			return ""
		}
		var parts []string
		for _, item := range items {
			if item.Msg == "" {
				continue
			}
			if loc := joinLoc(item.Loc); loc != "" {
				parts = append(parts, loc+": "+item.Msg)
			} else {
				parts = append(parts, item.Msg)
			}
		}
		msg = strings.Join(parts, "; ")
	case '"':
		if json.Unmarshal(detail, &msg) != nil {
			return ""
		}
	}
	return truncate(strings.Join(strings.Fields(msg), " "), maxMessageBytes)
}

// joinLoc joins the location of a validation error, a list of field names and
// indexes such as ["body", "voice_settings", "speed"], with dots.
func joinLoc(loc []any) string {
	parts := make([]string, 0, len(loc))
	for _, part := range loc {
		parts = append(parts, fmt.Sprint(part))
	}
	return strings.Join(parts, ".")
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
