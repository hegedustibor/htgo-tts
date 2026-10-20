// Package googlecloud implements an engine.Engine on top of the Google Cloud
// Text-to-Speech REST endpoint, POST /v1/text:synthesize: the official, paid
// counterpart of the Google Translate endpoint of engine/google, with
// hundreds of voices in dozens of languages, among them the Standard,
// WaveNet, Neural2, Studio and Chirp 3: HD kinds, and a choice of speaking
// rate, pitch, volume, sample rate and output format.
//
// Every request is billed to the Google Cloud project of the API key, which
// comes from WithKey or from the GOOGLE_CLOUD_API_KEY environment variable;
// the free tier of the service covers a number of characters per month for
// each kind of voice. The key only ever travels in the x-goog-api-key header,
// never in the URL: it is not part of the engine's fingerprint and never
// appears in an error.
//
// The text goes to the endpoint as plain text in a JSON document, and the
// audio comes back base64-encoded in the JSON answer. The endpoint accepts
// 5000 bytes of text per request, so longer text is split at sentence and
// clause boundaries and requested piece by piece while the audio already
// streams to the caller. The audio is sampled at 24 kHz unless WithSampleRate
// says otherwise; the native player of this module plays such MP3, and the
// other formats need a command-line player such as mplayer or ffplay.
package googlecloud

import (
	"bytes"
	"context"
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
	// DefaultEndpoint is the URL of the synthesis endpoint that the engine
	// posts to without WithEndpoint.
	DefaultEndpoint = "https://texttospeech.googleapis.com/v1/text:synthesize"

	// DefaultTimeout bounds each request to the endpoint when the engine
	// uses its own HTTP client.
	DefaultTimeout = 60 * time.Second

	// EnvKey is the environment variable that supplies the API key when
	// WithKey is not given.
	EnvKey = "GOOGLE_CLOUD_API_KEY"

	// DefaultLanguage is the language the engine requests a voice for when
	// neither WithVoice nor WithLanguage is given.
	DefaultLanguage = "en-US"

	// DefaultSampleRate is the sample rate of the audio, in hertz, without
	// WithSampleRate: the natural rate of the voices, and the rate the
	// native player of this module plays.
	DefaultSampleRate = 24000

	// MinSpeakingRate and MaxSpeakingRate bound the factor of
	// WithSpeakingRate.
	MinSpeakingRate = 0.25
	MaxSpeakingRate = 2.0

	// MinPitch and MaxPitch bound the semitones of WithPitch.
	MinPitch = -20.0
	MaxPitch = 20.0

	// MinVolumeGain and MaxVolumeGain bound the decibels of WithVolumeGain.
	MinVolumeGain = -96.0
	MaxVolumeGain = 16.0

	// maxInputBytes is the longest text the endpoint accepts in one request.
	maxInputBytes = 5000

	// maxInputRunes is the longest piece of text sent in one request. With
	// runes of up to three bytes, which covers every script except emoji
	// and a few rare characters, such a piece stays under maxInputBytes.
	maxInputRunes = 1500

	// maxErrorBodyBytes bounds the part of an error response that is read to
	// find the message of an error document.
	maxErrorBodyBytes = 32 << 10

	// maxMessageBytes bounds the message taken from an error document.
	maxMessageBytes = 500
)

// Voice is the name of a voice, as the endpoint expects it in the name field
// of the voice selection, for example "hu-HU-Wavenet-B". It is a string, so
// any of the hundreds of voices of the service can be used as Voice("name");
// the constants below are a few of them. The full list, with the gender and
// the natural sample rate of every voice, comes from the voices endpoint of
// the service, GET https://texttospeech.googleapis.com/v1/voices, which takes
// a languageCode query parameter to narrow it, and the first two parts of a
// name are the language that the voice speaks.
type Voice string

// A few of the voices of the service, checked against its voice list at the
// time of writing; the comments give the gender of each. The Standard voices
// are the cheapest, WaveNet and Neural2 sound more natural, the Studio voices
// are made for long narration, and Chirp 3: HD, the newest kind, sounds the
// most natural but takes no speaking rate and no pitch.
const (
	VoiceHuHUStandardB      Voice = "hu-HU-Standard-B"       // female
	VoiceHuHUWavenetB       Voice = "hu-HU-Wavenet-B"        // female
	VoiceHuHUChirp3HDAoede  Voice = "hu-HU-Chirp3-HD-Aoede"  // female
	VoiceHuHUChirp3HDCharon Voice = "hu-HU-Chirp3-HD-Charon" // male
	VoiceEnUSStandardC      Voice = "en-US-Standard-C"       // female
	VoiceEnUSStandardD      Voice = "en-US-Standard-D"       // male
	VoiceEnUSWavenetC       Voice = "en-US-Wavenet-C"        // female
	VoiceEnUSWavenetD       Voice = "en-US-Wavenet-D"        // male
	VoiceEnUSNeural2C       Voice = "en-US-Neural2-C"        // female
	VoiceEnUSNeural2D       Voice = "en-US-Neural2-D"        // male
	VoiceEnUSStudioO        Voice = "en-US-Studio-O"         // female
	VoiceEnUSStudioQ        Voice = "en-US-Studio-Q"         // male
	VoiceEnUSChirp3HDAoede  Voice = "en-US-Chirp3-HD-Aoede"  // female
	VoiceEnUSChirp3HDCharon Voice = "en-US-Chirp3-HD-Charon" // male
	VoiceEnGBStandardA      Voice = "en-GB-Standard-A"       // female
	VoiceEnGBStandardB      Voice = "en-GB-Standard-B"       // male
	VoiceEnGBWavenetA       Voice = "en-GB-Wavenet-A"        // female
	VoiceEnGBWavenetB       Voice = "en-GB-Wavenet-B"        // male
	VoiceEnGBNeural2A       Voice = "en-GB-Neural2-A"        // female
	VoiceEnGBNeural2B       Voice = "en-GB-Neural2-B"        // male
	VoiceDeDEStandardG      Voice = "de-DE-Standard-G"       // female
	VoiceDeDEStandardH      Voice = "de-DE-Standard-H"       // male
	VoiceDeDEWavenetG       Voice = "de-DE-Wavenet-G"        // female
	VoiceDeDEWavenetH       Voice = "de-DE-Wavenet-H"        // male
	VoiceDeDENeural2G       Voice = "de-DE-Neural2-G"        // female
	VoiceDeDENeural2H       Voice = "de-DE-Neural2-H"        // male
)

// Gender is the gender the service should prefer when it chooses the voice,
// as the endpoint expects it in the ssmlGender field of the voice selection.
type Gender string

// The genders of WithGender.
const (
	GenderMale   Gender = "MALE"
	GenderFemale Gender = "FEMALE"
	// GenderNeutral asks for a gender-neutral voice, which the service does
	// not have at the time of writing: it then answers with a voice of
	// another gender.
	GenderNeutral Gender = "NEUTRAL"
)

// encodings maps the formats of WithFormat to the audioEncoding values of
// the endpoint.
var encodings = map[engine.Format]string{
	engine.FormatMP3:  "MP3",
	engine.FormatWAV:  "LINEAR16",
	engine.FormatOpus: "OGG_OPUS",
	engine.FormatPCM:  "PCM",
}

// Engine speaks text through the Google Cloud Text-to-Speech endpoint.
// Create one with New. An Engine is safe for concurrent use.
type Engine struct {
	key        string
	endpoint   string
	voice      Voice
	language   string
	gender     Gender
	rate       float64 // the speaking rate as a factor; 1 is the voice's own
	pitch      float64 // in semitones; 0 is the voice's own
	volumeGain float64 // in decibels; 0 is the voice's own
	format     engine.Format
	sampleRate int
	client     *http.Client
	timeout    time.Duration
	timeoutSet bool
	err        error // the first invalid argument, returned by every Synthesize
}

// Option configures an Engine; see the With functions.
type Option func(*Engine)

// New returns an engine that lets the service choose a voice for
// DefaultLanguage and speaks at the voice's own rate, pitch and volume, as
// MP3 sampled at DefaultSampleRate; options change these and are applied in
// order.
//
// The API key comes from WithKey or, without it, from the GOOGLE_CLOUD_API_KEY
// environment variable, which New reads once.
//
// An invalid argument, such as a missing key, a speaking rate or a pitch
// outside its range, a format the service does not produce or a malformed
// endpoint URL, does not make New fail: the engine remembers the first error
// and every call to Synthesize returns it, so that a configuration mistake
// surfaces at the first use instead of being ignored.
func New(opts ...Option) *Engine {
	e := &Engine{
		endpoint:   DefaultEndpoint,
		rate:       1,
		format:     engine.FormatMP3,
		sampleRate: DefaultSampleRate,
		timeout:    DefaultTimeout,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.key == "" {
		e.key = strings.TrimSpace(os.Getenv(EnvKey))
	}
	if e.key == "" {
		e.fail(errors.New("htgotts/googlecloud: missing API key: use WithKey or set " + EnvKey))
	}
	if e.language == "" {
		e.language = DefaultLanguage
		if e.voice != "" {
			e.language = languageOf(e.voice)
		}
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

// WithKey authenticates the requests with the API key instead of the value
// of the GOOGLE_CLOUD_API_KEY environment variable. White space around the
// key is dropped; an empty key means the environment variable, as without
// the option. The key goes into the x-goog-api-key header of every request,
// never into its URL.
func WithKey(key string) Option {
	return func(e *Engine) { e.key = strings.TrimSpace(key) }
}

// WithEndpoint sends the requests to rawURL instead of DefaultEndpoint: to a
// regional endpoint of the service, such as
// https://eu-texttospeech.googleapis.com/v1/text:synthesize, which keeps the
// processing in that region but offers fewer voices, to a proxy of the
// service, or to a test server. The URL is used as given, query parameters
// included.
func WithEndpoint(rawURL string) Option {
	return func(e *Engine) {
		u, err := parseURL(rawURL, "http", "https")
		if err != nil {
			e.fail(err)
			return
		}
		e.endpoint = u.String()
	}
}

// WithVoice selects the voice by its name, for example VoiceHuHUWavenetB.
// Without the option the service chooses a voice for the language of
// WithLanguage, preferring the gender of WithGender. Without WithLanguage the
// language of the speech is the one in the voice's name, hu-HU for
// VoiceHuHUWavenetB.
func WithVoice(voice Voice) Option {
	return func(e *Engine) {
		if strings.TrimSpace(string(voice)) == "" {
			e.fail(errors.New("htgotts/googlecloud: empty voice"))
			return
		}
		e.voice = voice
	}
}

// WithLanguage sets the language of the request, a BCP 47 tag such as
// "en-US", "de-DE" or "cmn-CN": the language the service chooses a voice for
// without WithVoice, DefaultLanguage by default, or, with WithVoice, the
// language in place of the one in the voice's name.
func WithLanguage(language string) Option {
	return func(e *Engine) {
		language = strings.TrimSpace(language)
		if language == "" {
			e.fail(errors.New("htgotts/googlecloud: empty language"))
			return
		}
		e.language = language
	}
}

// WithGender asks the service to prefer a voice of the given gender when it
// chooses one, that is without WithVoice; a voice named with WithVoice is
// used whatever its gender. It is a preference, not a requirement: without
// a voice of that gender in the language, the service answers with another
// one. The case of the gender does not matter.
func WithGender(gender Gender) Option {
	return func(e *Engine) {
		g := Gender(strings.ToUpper(strings.TrimSpace(string(gender))))
		switch g {
		case GenderMale, GenderFemale, GenderNeutral:
			e.gender = g
		default:
			e.fail(fmt.Errorf("htgotts/googlecloud: unknown gender %q: use GenderMale, GenderFemale or GenderNeutral", gender))
		}
	}
}

// WithSpeakingRate sets the pace of the speech as a factor of the voice's
// own, between MinSpeakingRate and MaxSpeakingRate: 2 is twice as fast and
// 0.5 half as fast. 1, the default, is not sent to the endpoint. The Chirp
// 3: HD voices do not support it.
func WithSpeakingRate(rate float64) Option {
	return func(e *Engine) {
		if math.IsNaN(rate) || rate < MinSpeakingRate || rate > MaxSpeakingRate {
			e.fail(fmt.Errorf("htgotts/googlecloud: speaking rate %v is not between %v and %v", rate, MinSpeakingRate, MaxSpeakingRate))
			return
		}
		e.rate = rate
	}
}

// WithPitch raises or lowers the pitch of the voice by the given number of
// semitones, between MinPitch and MaxPitch. 0, the default, is not sent to
// the endpoint. The Chirp 3: HD voices do not support it.
func WithPitch(semitones float64) Option {
	return func(e *Engine) {
		if math.IsNaN(semitones) || semitones < MinPitch || semitones > MaxPitch {
			e.fail(fmt.Errorf("htgotts/googlecloud: pitch %v is not between %v and %v", semitones, MinPitch, MaxPitch))
			return
		}
		e.pitch = semitones
	}
}

// WithVolumeGain raises or lowers the volume of the voice by the given
// number of decibels, between MinVolumeGain and MaxVolumeGain: -6 halves the
// amplitude and +6 doubles it, and the service says that a gain above +10
// adds no loudness. 0, the default, is not sent to the endpoint.
func WithVolumeGain(decibels float64) Option {
	return func(e *Engine) {
		if math.IsNaN(decibels) || decibels < MinVolumeGain || decibels > MaxVolumeGain {
			e.fail(fmt.Errorf("htgotts/googlecloud: volume gain %v is not between %v and %v", decibels, MinVolumeGain, MaxVolumeGain))
			return
		}
		e.volumeGain = decibels
	}
}

// WithFormat selects the encoding of the audio: engine.FormatMP3, the
// default, at 32 kbit/s; engine.FormatWAV, 16-bit PCM in a RIFF container;
// engine.FormatOpus in an Ogg container; or engine.FormatPCM, raw 16-bit
// little-endian mono samples. All are sampled at the rate of WithSampleRate.
// The service does not produce AAC or FLAC. The native player of this module
// plays MP3 only; the other formats need a command-line player.
func WithFormat(format engine.Format) Option {
	return func(e *Engine) {
		if _, ok := encodings[format]; !ok {
			e.fail(fmt.Errorf("htgotts/googlecloud: unsupported format %q", format))
			return
		}
		e.format = format
	}
}

// WithSampleRate sets the sample rate of the audio in hertz instead of
// DefaultSampleRate, the natural rate of the voices and the rate the native
// player of this module plays. The service resamples the audio when the rate
// differs from the voice's own, and refuses a rate that the encoding cannot
// carry.
func WithSampleRate(hertz int) Option {
	return func(e *Engine) {
		if hertz <= 0 {
			e.fail(fmt.Errorf("htgotts/googlecloud: invalid sample rate %d Hz", hertz))
			return
		}
		e.sampleRate = hertz
	}
}

// WithHTTPClient makes the engine send its requests with client instead of
// its own one, for example to use a proxy or to record the requests in a
// test. The engine's default timeout does not apply to such a client; an
// explicit WithTimeout does.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		if client == nil {
			e.fail(errors.New("htgotts/googlecloud: nil HTTP client"))
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
			e.fail(fmt.Errorf("htgotts/googlecloud: negative timeout %v", d))
			return
		}
		e.timeout = d
		e.timeoutSet = true
	}
}

// parseURL parses rawURL for WithEndpoint and checks that it has a host and
// one of the given schemes. Its errors never contain the password that
// rawURL may carry.
func parseURL(rawURL string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("htgotts/googlecloud: invalid endpoint URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("htgotts/googlecloud: invalid endpoint URL %q: missing host", u.Redacted())
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("htgotts/googlecloud: invalid endpoint URL %q: unsupported scheme %q", u.Redacted(), u.Scheme)
}

// languageOf returns the language in a voice name such as "hu-HU-Wavenet-B",
// that is its first two parts, or DefaultLanguage when the name does not
// start with a language and a region.
func languageOf(voice Voice) string {
	parts := strings.SplitN(string(voice), "-", 3)
	if len(parts) == 3 && isLanguage(parts[0]) && isRegionCode(parts[1]) {
		return parts[0] + "-" + parts[1]
	}
	return DefaultLanguage
}

// isLanguage reports whether s is a two- or three-letter lowercase language
// code.
func isLanguage(s string) bool {
	if len(s) < 2 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// isRegionCode reports whether s is a two- to four-character region or script
// code of ASCII letters and digits, such as "US" or "Hans".
func isRegionCode(s string) bool {
	if len(s) < 2 || len(s) > 4 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func (e *Engine) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Format reports the format set with WithFormat, engine.FormatMP3 by default.
func (e *Engine) Format() engine.Format { return e.format }

// Fingerprint identifies the voice and every setting that changes the sound:
// "googlecloud-<voice>-<rate>-<pitch>", for example
// "googlecloud-hu-HU-Wavenet-B-1-0", or, without a voice, the language and
// the gender in place of the voice, for example "googlecloud-en-US-1-0" or
// "googlecloud-de-DE-male-1.25-0"; followed by "-" and the sample rate when
// it is not DefaultSampleRate, and by "-g" and the volume gain when there is
// one, as in "googlecloud-en-US-1-0-48000-g-6". A character that does not
// belong in a file name is replaced by "-". With a voice the language is not
// part of it, because the voice decides the sound; the format is not either,
// because cached files carry it as their extension; and neither is the key
// or the endpoint.
func (e *Engine) Fingerprint() string {
	fp := "googlecloud-"
	if e.voice != "" {
		fp += string(e.voice)
	} else {
		fp += e.language
		if e.gender != "" {
			fp += "-" + strings.ToLower(string(e.gender))
		}
	}
	fp += "-" + formatNumber(e.rate) + "-" + formatNumber(e.pitch)
	if e.sampleRate != DefaultSampleRate {
		fp += "-" + strconv.Itoa(e.sampleRate)
	}
	if e.volumeGain != 0 {
		fp += "-g" + formatNumber(e.volumeGain)
	}
	return sanitize(fp)
}

// formatNumber formats f as short as possible, "1" for 1.0 and "1.25" for
// 1.25; a negative zero reads as "0".
func formatNumber(f float64) string {
	if f == 0 {
		f = 0
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
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
// The endpoint accepts 5000 bytes of text in one request, so longer text is
// split at sentence and clause boundaries into pieces of at most 1500
// characters, and the pieces are requested one after the other in the
// background while the caller reads: the first bytes arrive once the first
// piece has been received, and the audio of the pieces simply follows each
// other. That is sound for MP3 and raw PCM; with the container formats (WAV,
// Opus) every piece carries a header of its own, so keep the text within one
// request when using them. A failed request ends the stream with its error,
// wrapped with the index of the piece; an answer other than audio is an
// *engine.HTTPError whose Message is the message of the service's error
// document when there is one. The stream stops when ctx is done, with an
// error that wraps ctx.Err().
//
// Close on the returned reader cancels the request in flight, prevents any
// further one and returns once the background work has stopped. Call it even
// after reading the stream to its end.
func (e *Engine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if e.err != nil {
		return nil, e.err
	}
	chunks := split(text)
	if len(chunks) == 0 {
		return nil, engine.ErrEmptyText
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("htgotts/googlecloud: %w", err)
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

// split breaks text into the pieces that Synthesize requests one by one: at
// most maxInputRunes runes each, which keeps a piece under maxInputBytes with
// runes of up to three bytes. A piece that is still longer than that because
// of four-byte runes, such as emoji, is split again by a rune count that fits
// any rune.
func split(text string) []string {
	var chunks []string
	for _, c := range chunk.Split(text, maxInputRunes) {
		if len(c) <= maxInputBytes {
			chunks = append(chunks, c)
			continue
		}
		chunks = append(chunks, chunk.Split(c, maxInputBytes/utf8.UTFMax)...)
	}
	return chunks
}

// download requests the chunks in order and writes their audio to w.
func (e *Engine) download(ctx context.Context, chunks []string, w io.Writer) error {
	for i, text := range chunks {
		if err := e.fetch(ctx, text, w); err != nil {
			return fmt.Errorf("htgotts/googlecloud: chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// request is the JSON body of a synthesis request.
type request struct {
	Input       synthesisInput `json:"input"`
	Voice       voiceSelection `json:"voice"`
	AudioConfig audioConfig    `json:"audioConfig"`
}

type synthesisInput struct {
	Text string `json:"text"`
}

type voiceSelection struct {
	LanguageCode string `json:"languageCode"`
	Name         Voice  `json:"name,omitempty"`
	SSMLGender   Gender `json:"ssmlGender,omitempty"`
}

type audioConfig struct {
	AudioEncoding   string  `json:"audioEncoding"`
	SampleRateHertz int     `json:"sampleRateHertz"`
	SpeakingRate    float64 `json:"speakingRate,omitempty"`
	Pitch           float64 `json:"pitch,omitempty"`
	VolumeGainDb    float64 `json:"volumeGainDb,omitempty"`
}

// response is the JSON answer of the endpoint; the audio is base64-encoded
// in the document, which encoding/json decodes into the byte slice.
type response struct {
	AudioContent []byte `json:"audioContent"`
}

// requestBody builds the body of the request for one chunk. The voice name
// and the gender go only when they are set, and the speaking rate, the pitch
// and the volume gain only when they differ from the voice's own, which is
// what the endpoint uses without them.
func (e *Engine) requestBody(text string) request {
	body := request{
		Input: synthesisInput{Text: text},
		Voice: voiceSelection{LanguageCode: e.language, Name: e.voice, SSMLGender: e.gender},
		AudioConfig: audioConfig{
			AudioEncoding:   encodings[e.format],
			SampleRateHertz: e.sampleRate,
			Pitch:           e.pitch,
			VolumeGainDb:    e.volumeGain,
		},
	}
	if e.rate != 1 {
		body.AudioConfig.SpeakingRate = e.rate
	}
	return body
}

// fetch requests the audio of one chunk, decodes it from the JSON answer and
// writes it to w.
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
	req.Header.Set("x-goog-api-key", e.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return orContextError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	if err := httpx.CheckResponse(resp, "application/json"); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return orContextError(ctx, err)
	}
	audio, err := audioOf(raw)
	if err != nil {
		return err
	}
	if _, err := w.Write(audio); err != nil {
		return orContextError(ctx, err)
	}
	return nil
}

// audioOf returns the audio of the JSON answer of the endpoint, a document
// of the form {"audioContent": "<base64>"}, decoded.
func audioOf(raw []byte) ([]byte, error) {
	var doc response
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	if len(doc.AudioContent) == 0 {
		return nil, errors.New("malformed response: no audioContent")
	}
	return doc.AudioContent, nil
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

// errorMessage returns the message of an error document of the service, a
// JSON body of the form {"error": {"code": 400, "message": "...", "status":
// "INVALID_ARGUMENT"}}, with its white space collapsed and its length
// bounded, or "" when raw is not such a document.
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
