// Package azure implements an engine.Engine on top of the Azure Speech text
// to speech REST endpoint, POST /cognitiveservices/v1: a paid service with
// hundreds of neural voices in over a hundred languages, speaking styles for
// many of them, and a choice of speaking rate, pitch and output format.
//
// Every request is billed to the Speech resource of the key, which comes from
// WithKey or from the AZURE_SPEECH_KEY environment variable; the free tier of
// the service covers a number of characters per month. The key only ever
// travels in the Ocp-Apim-Subscription-Key header: it is not part of the
// engine's fingerprint and never appears in an error. The region of the
// resource comes from WithRegion or from AZURE_SPEECH_REGION, and WithEndpoint
// replaces the regional endpoint with any other, such as the endpoint of a
// custom voice or of a sovereign cloud.
//
// The text goes to the endpoint as SSML, with the voice, the language and the
// optional style, rate and pitch around it; characters with a meaning in XML
// are escaped. The service cuts the audio of one request at ten minutes, so
// longer text is split at sentence and clause boundaries and requested piece
// by piece while the audio already streams to the caller. The MP3 output is
// sampled at 24 kHz, which the native player of this module plays; the other
// formats need a command-line player such as mplayer or ffplay.
package azure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/internal/chunk"
	"github.com/hegedustibor/htgo-tts/v2/internal/httpx"
)

const (
	// DefaultTimeout bounds each request to the endpoint when the engine
	// uses its own HTTP client.
	DefaultTimeout = 60 * time.Second

	// EnvKey is the environment variable that supplies the resource key when
	// WithKey is not given.
	EnvKey = "AZURE_SPEECH_KEY"

	// EnvRegion is the environment variable that supplies the region of the
	// Speech resource when neither WithRegion nor WithEndpoint is given.
	EnvRegion = "AZURE_SPEECH_REGION"

	// UserAgent is the User-Agent header of every request; the endpoint
	// refuses a request without one.
	UserAgent = "htgo-tts"

	// DefaultVoice is the voice that New uses without WithVoice.
	DefaultVoice Voice = "en-US-JennyNeural"

	// endpointPath is the path of the speech endpoint under a regional host.
	endpointPath = "cognitiveservices/v1"

	// maxInputChars is the longest text sent in one request. The endpoint
	// cuts the audio of a request at ten minutes; at the speed of the
	// voices, about 150 words a minute, this many characters stay well
	// below that.
	maxInputChars = 3000

	// maxErrorBodyBytes bounds the part of an error response that is read to
	// find the message of an error document.
	maxErrorBodyBytes = 32 << 10

	// maxMessageBytes bounds the message taken from an error document.
	maxMessageBytes = 500
)

// Voice is the name of a voice, as the endpoint expects it in the name
// attribute of the SSML voice element, for example "hu-HU-NoemiNeural". It is
// a string, so any of the hundreds of voices of the service, or a custom
// voice, can be used as Voice("name"); the constants below are a few of them.
// The full list comes from the voices/list endpoint of the Speech resource,
// and the first two parts of a name are the language that the voice speaks.
type Voice string

// A few of the neural voices of the service.
const (
	VoiceEnUSJenny    Voice = "en-US-JennyNeural"
	VoiceEnUSGuy      Voice = "en-US-GuyNeural"
	VoiceEnUSAva      Voice = "en-US-AvaNeural"
	VoiceEnUSAndrew   Voice = "en-US-AndrewNeural"
	VoiceEnGBSonia    Voice = "en-GB-SoniaNeural"
	VoiceEnGBRyan     Voice = "en-GB-RyanNeural"
	VoiceDeDEKatja    Voice = "de-DE-KatjaNeural"
	VoiceDeDEConrad   Voice = "de-DE-ConradNeural"
	VoiceFrFRDenise   Voice = "fr-FR-DeniseNeural"
	VoiceFrFRHenri    Voice = "fr-FR-HenriNeural"
	VoiceEsESElvira   Voice = "es-ES-ElviraNeural"
	VoiceEsESAlvaro   Voice = "es-ES-AlvaroNeural"
	VoiceHuHUNoemi    Voice = "hu-HU-NoemiNeural"
	VoiceHuHUTamas    Voice = "hu-HU-TamasNeural"
	VoiceJaJPNanami   Voice = "ja-JP-NanamiNeural"
	VoiceJaJPKeita    Voice = "ja-JP-KeitaNeural"
	VoiceZhCNXiaoxiao Voice = "zh-CN-XiaoxiaoNeural"
	VoiceZhCNYunxi    Voice = "zh-CN-YunxiNeural"
)

// outputFormats maps the formats of WithFormat to the names of the
// X-Microsoft-OutputFormat header, all at 24 kHz.
var outputFormats = map[engine.Format]string{
	engine.FormatMP3:  "audio-24khz-48kbitrate-mono-mp3",
	engine.FormatWAV:  "riff-24khz-16bit-mono-pcm",
	engine.FormatOpus: "ogg-24khz-16bit-mono-opus",
	engine.FormatPCM:  "raw-24khz-16bit-mono-pcm",
}

// Engine speaks text through the Azure Speech endpoint. Create one with New.
// An Engine is safe for concurrent use.
type Engine struct {
	key          string
	endpoint     string
	voice        Voice
	language     string
	format       engine.Format
	outputFormat string // the X-Microsoft-OutputFormat header
	rate         string
	pitch        string
	style        string
	client       *http.Client
	timeout      time.Duration
	timeoutSet   bool
	err          error // the first invalid argument, returned by every Synthesize
}

// Option configures an Engine; see the With functions.
type Option func(*Engine)

// New returns an engine that speaks with DefaultVoice, in the language of
// the voice, at the voice's own rate and pitch, as MP3; options change these
// and are applied in order.
//
// The resource key comes from WithKey or, without it, from the
// AZURE_SPEECH_KEY environment variable; the endpoint from WithRegion or
// WithEndpoint or, without them, from the region in AZURE_SPEECH_REGION. New
// reads the environment once.
//
// An invalid argument, such as a missing key or region, an empty voice, a
// format the service does not produce or a malformed endpoint URL, does not
// make New fail: the engine remembers the first error and every call to
// Synthesize returns it, so that a configuration mistake surfaces at the
// first use instead of being ignored.
func New(opts ...Option) *Engine {
	e := &Engine{
		voice:        DefaultVoice,
		format:       engine.FormatMP3,
		outputFormat: outputFormats[engine.FormatMP3],
		timeout:      DefaultTimeout,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.key == "" {
		e.key = strings.TrimSpace(os.Getenv(EnvKey))
	}
	if e.key == "" {
		e.fail(errors.New("htgotts/azure: missing resource key: use WithKey or set " + EnvKey))
	}
	if e.endpoint == "" {
		if region := strings.TrimSpace(os.Getenv(EnvRegion)); region != "" {
			WithRegion(region)(e)
		} else {
			e.fail(errors.New("htgotts/azure: missing endpoint: use WithRegion or WithEndpoint, or set " + EnvRegion))
		}
	}
	if e.language == "" {
		e.language = languageOf(e.voice)
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

// WithKey authenticates the requests with the resource key instead of the
// value of the AZURE_SPEECH_KEY environment variable. White space around the
// key is dropped; an empty key means the environment variable, as without the
// option.
func WithKey(key string) Option {
	return func(e *Engine) { e.key = strings.TrimSpace(key) }
}

// WithRegion sends the requests to the regional endpoint of the Speech
// resource, https://<region>.tts.speech.microsoft.com/cognitiveservices/v1,
// for example "westeurope" or "eastus". It replaces an endpoint set earlier
// with WithEndpoint, and vice versa.
func WithRegion(region string) Option {
	return func(e *Engine) {
		region = strings.ToLower(strings.TrimSpace(region))
		if region == "" || !isRegion(region) {
			e.fail(fmt.Errorf("htgotts/azure: invalid region %q: expected an Azure region name such as westeurope", region))
			return
		}
		e.endpoint = "https://" + region + ".tts.speech.microsoft.com/" + endpointPath
	}
}

// isRegion reports whether s consists of ASCII letters and digits only, as
// Azure region names do.
func isRegion(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// WithEndpoint sends the requests to rawURL instead of the regional endpoint:
// to the endpoint of a custom voice, with its deploymentId query parameter,
// to a sovereign cloud, or to a test server. The URL is used as given, query
// parameters included.
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

// WithVoice selects the voice; the default is DefaultVoice. Without
// WithLanguage the language of the speech is the one in the voice's name,
// for example hu-HU for VoiceHuHUNoemi.
func WithVoice(voice Voice) Option {
	return func(e *Engine) {
		if strings.TrimSpace(string(voice)) == "" {
			e.fail(errors.New("htgotts/azure: empty voice"))
			return
		}
		e.voice = voice
	}
}

// WithLanguage sets the language of the SSML document, a code such as "en-US"
// or "de", instead of the language in the voice's name. It matters for a
// custom voice, whose name carries no language, and for a multilingual voice
// that should read the text in another language than its own.
func WithLanguage(language string) Option {
	return func(e *Engine) {
		language = strings.TrimSpace(language)
		if language == "" {
			e.fail(errors.New("htgotts/azure: empty language"))
			return
		}
		e.language = language
	}
}

// WithFormat selects the encoding of the audio, all sampled at 24 kHz:
// engine.FormatMP3, the default, at 48 kbit/s; engine.FormatWAV, 16-bit PCM
// in a RIFF container; engine.FormatOpus in an Ogg container; or
// engine.FormatPCM, raw 16-bit little-endian mono samples. The service does
// not produce AAC or FLAC. The native player of this module plays MP3 only;
// the other formats need a command-line player. WithOutputFormat selects any
// other format of the service.
func WithFormat(format engine.Format) Option {
	return func(e *Engine) {
		name, ok := outputFormats[format]
		if !ok {
			e.fail(fmt.Errorf("htgotts/azure: unsupported format %q", format))
			return
		}
		e.format = format
		e.outputFormat = name
	}
}

// WithOutputFormat requests the audio in any format of the service by the
// name the X-Microsoft-OutputFormat header takes, for example
// "audio-48khz-192kbitrate-mono-mp3" or "riff-16khz-16bit-mono-pcm", together
// with the engine.Format that names the encoding: the extension of saved and
// cached files, and the hint for choosing a player. The native player plays
// 24 kHz MP3 only; a 48 kHz MP3 fails in it with another sample rate.
func WithOutputFormat(name string, format engine.Format) Option {
	return func(e *Engine) {
		name = strings.TrimSpace(name)
		if name == "" {
			e.fail(errors.New("htgotts/azure: empty output format"))
			return
		}
		if format == "" {
			e.fail(fmt.Errorf("htgotts/azure: output format %q: empty engine format", name))
			return
		}
		e.outputFormat = name
		e.format = format
	}
}

// WithRate sets the speaking rate through the SSML prosody element: a
// multiplier such as "1.2" or "0.8", a relative change such as "+10%" or
// "-20%", or one of "x-slow", "slow", "medium", "fast" and "x-fast". The
// service keeps the result within half and twice the voice's own rate.
func WithRate(rate string) Option {
	return func(e *Engine) {
		rate = strings.TrimSpace(rate)
		if rate == "" {
			e.fail(errors.New("htgotts/azure: empty rate"))
			return
		}
		e.rate = rate
	}
}

// WithPitch sets the baseline pitch through the SSML prosody element: a
// relative change such as "+2st" (semitones), "-50Hz" or "+10%", an absolute
// value such as "200Hz", or one of "x-low", "low", "medium", "high" and
// "x-high".
func WithPitch(pitch string) Option {
	return func(e *Engine) {
		pitch = strings.TrimSpace(pitch)
		if pitch == "" {
			e.fail(errors.New("htgotts/azure: empty pitch"))
			return
		}
		e.pitch = pitch
	}
}

// WithStyle sets the speaking style through the mstts:express-as element,
// for example "cheerful", "sad", "newscast" or "whispering". Every voice has
// its own list of styles, given by the voices/list endpoint; a style the
// voice does not have is ignored by the service, which then speaks neutrally.
func WithStyle(style string) Option {
	return func(e *Engine) {
		style = strings.TrimSpace(style)
		if style == "" {
			e.fail(errors.New("htgotts/azure: empty style"))
			return
		}
		e.style = style
	}
}

// WithHTTPClient makes the engine send its requests with client instead of
// its own one, for example to use a proxy or to record the requests in a
// test. The engine's default timeout does not apply to such a client; an
// explicit WithTimeout does.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		if client == nil {
			e.fail(errors.New("htgotts/azure: nil HTTP client"))
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
			e.fail(fmt.Errorf("htgotts/azure: negative timeout %v", d))
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
		return nil, fmt.Errorf("htgotts/azure: invalid endpoint URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("htgotts/azure: invalid endpoint URL %q: missing host", u.Redacted())
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("htgotts/azure: invalid endpoint URL %q: unsupported scheme %q", u.Redacted(), u.Scheme)
}

// languageOf returns the language in a voice name such as
// "hu-HU-NoemiNeural", that is its first two parts, or "en-US" when the name
// does not start with a language and a region, as the name of a custom voice
// may not.
func languageOf(voice Voice) string {
	parts := strings.SplitN(string(voice), "-", 3)
	if len(parts) == 3 && isLanguage(parts[0]) && isRegionCode(parts[1]) {
		return parts[0] + "-" + parts[1]
	}
	return "en-US"
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

// Format reports the format set with WithFormat or WithOutputFormat,
// engine.FormatMP3 by default.
func (e *Engine) Format() engine.Format { return e.format }

// Fingerprint identifies the voice and every setting that changes the sound:
// "azure-<voice>", for example "azure-hu-HU-NoemiNeural", followed by "-" and
// the output format's name when WithOutputFormat chose one other than the
// default for the format, and by "-" and the first eight hex digits of the
// SHA-256 hash of the rate, the pitch and the style when any of them is set,
// so that the same text spoken differently gets a cache file of its own. A
// character that does not belong in a file name is replaced by "-". The
// language is not part of it, because the voice decides the sound, nor is the
// key or the endpoint.
func (e *Engine) Fingerprint() string {
	fp := "azure-" + string(e.voice)
	if e.outputFormat != outputFormats[e.format] {
		fp += "-" + e.outputFormat
	}
	if e.rate != "" || e.pitch != "" || e.style != "" {
		sum := sha256.Sum256([]byte(e.rate + "\x00" + e.pitch + "\x00" + e.style))
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
// The endpoint cuts the audio of one request at ten minutes, so text longer
// than a few thousand characters is split at sentence and clause boundaries,
// and the pieces are requested one after the other in the background while
// the caller reads: the first bytes arrive once the first piece has been
// received, and the audio of the pieces simply follows each other. That is
// sound for MP3 and raw PCM; with the container formats (WAV, Opus) every
// piece carries a header of its own, so keep the text short when using them.
// A failed request ends the stream with its error, wrapped with the index of
// the piece; an answer other than audio is an *engine.HTTPError whose Message
// is the message of the service's error document when there is one. The
// stream stops when ctx is done, with an error that wraps ctx.Err().
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
		return nil, fmt.Errorf("htgotts/azure: %w", err)
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
			return fmt.Errorf("htgotts/azure: chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// ssml builds the SSML document for one chunk: the speak root with the
// language, the voice, and inside it the optional style (mstts:express-as)
// and prosody (rate and pitch) elements around the escaped text.
func (e *Engine) ssml(text string) string {
	var b strings.Builder
	b.WriteString(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="https://www.w3.org/2001/mstts" xml:lang="`)
	escape(&b, e.language)
	b.WriteString(`"><voice name="`)
	escape(&b, string(e.voice))
	b.WriteString(`">`)
	if e.style != "" {
		b.WriteString(`<mstts:express-as style="`)
		escape(&b, e.style)
		b.WriteString(`">`)
	}
	prosody := e.rate != "" || e.pitch != ""
	if prosody {
		b.WriteString("<prosody")
		if e.rate != "" {
			b.WriteString(` rate="`)
			escape(&b, e.rate)
			b.WriteString(`"`)
		}
		if e.pitch != "" {
			b.WriteString(` pitch="`)
			escape(&b, e.pitch)
			b.WriteString(`"`)
		}
		b.WriteString(">")
	}
	escape(&b, text)
	if prosody {
		b.WriteString("</prosody>")
	}
	if e.style != "" {
		b.WriteString("</mstts:express-as>")
	}
	b.WriteString("</voice></speak>")
	return b.String()
}

// escape writes s to b with the characters that have a meaning in XML
// replaced by entities, so that it is safe both as text and as an attribute
// value.
func escape(b *strings.Builder, s string) {
	// Writing to a strings.Builder cannot fail.
	_ = xml.EscapeText(b, []byte(s))
}

// fetch requests the audio of one chunk and copies it to w.
func (e *Engine) fetch(ctx context.Context, text string, w io.Writer) error {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, strings.NewReader(e.ssml(text)))
	if err != nil {
		return err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", e.key)
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", e.outputFormat)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := e.client.Do(req)
	if err != nil {
		return orContextError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	// The service names the audio type for the usual formats; a raw format
	// may come as an octet stream or without a type.
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
	r.Body = io.NopCloser(strings.NewReader(string(raw)))
	err := httpx.CheckResponse(&r)
	var httpErr *engine.HTTPError
	if msg := errorMessage(raw); msg != "" && errors.As(err, &httpErr) {
		httpErr.Message = msg
	}
	return err
}

// errorMessage returns the message of an error document of the service, a
// JSON body of the form {"error": {"code": "...", "message": "..."}}, with
// its white space collapsed and its length bounded, or "" when raw is not
// such a document.
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
