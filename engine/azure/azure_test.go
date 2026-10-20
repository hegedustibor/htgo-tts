package azure

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// testKey is the resource key of the tests; it must never show up in an
// error or a fingerprint.
const testKey = "0123456789abcdef0123456789abcdef"

// recorder keeps what the fake endpoint received.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	method string
	path   string
	query  string
	header http.Header
	body   string // the SSML document of the request
}

func (rec *recorder) add(r recorded) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.reqs = append(rec.reqs, r)
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.reqs)
}

func (rec *recorder) at(i int) recorded {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.reqs[i]
}

// textOf returns the text of an SSML document: its character data, joined.
func textOf(ssml string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(ssml))
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		if cd, ok := tok.(xml.CharData); ok {
			b.Write(cd)
		}
	}
}

// contentTypes is the Content-Type the fake endpoint answers for the output
// formats of WithFormat; any other format gets a generic audio type.
var contentTypes = map[string]string{
	"audio-24khz-48kbitrate-mono-mp3": "audio/mpeg",
	"riff-24khz-16bit-mono-pcm":       "audio/x-wav",
	"ogg-24khz-16bit-mono-opus":       "audio/ogg",
	"raw-24khz-16bit-mono-pcm":        "audio/x-raw",
}

// audioFor is the distinct audio that the fake endpoint answers for a text:
// its length in runes, so that the pieces of a long text can be told apart.
func audioFor(text string) string {
	return "audio[" + strconv.Itoa(utf8.RuneCountInString(text)) + "];"
}

// serveAudio answers a speech request with audioFor of the text in its SSML,
// in the Content-Type of its output format.
func serveAudio(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text, err := textOf(string(raw))
	if err != nil {
		http.Error(w, "malformed SSML: "+err.Error(), http.StatusBadRequest)
		return
	}
	contentType, ok := contentTypes[r.Header.Get("X-Microsoft-OutputFormat")]
	if !ok {
		contentType = "audio/basic"
	}
	w.Header().Set("Content-Type", contentType)
	io.WriteString(w, audioFor(text))
}

// newEndpoint starts a fake endpoint that records every request in rec and
// answers with handler, or with serveAudio when handler is nil.
func newEndpoint(t *testing.T, rec *recorder, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	if handler == nil {
		handler = serveAudio
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if rec != nil {
			rec.add(recorded{
				method: r.Method,
				path:   r.URL.Path,
				query:  r.URL.RawQuery,
				header: r.Header.Clone(),
				body:   string(raw),
			})
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// endpointOf is the endpoint URL of the fake server, with the path of the
// real one.
func endpointOf(srv *httptest.Server) string {
	return srv.URL + "/" + endpointPath
}

// newEngine returns an engine that talks to the fake endpoint at srv with
// testKey, plus the given options.
func newEngine(srv *httptest.Server, opts ...Option) *Engine {
	return New(append([]Option{WithKey(testKey), WithEndpoint(endpointOf(srv))}, opts...)...)
}

// twoChunks returns a text that Synthesize sends in two requests, together
// with the two pieces: a sentence of exactly maxInputChars runes and a short
// one.
func twoChunks() (string, []string) {
	first := strings.Repeat("a", maxInputChars-1) + "."
	second := "Second sentence."
	return first + " " + second, []string{first, second}
}

func readAll(t *testing.T, e *Engine, text string) (string, error) {
	t.Helper()
	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

// closeWithin fails the test when Close does not return in time, which means
// that the download goroutine is stuck.
func closeWithin(t *testing.T, rc io.Closer, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		rc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("Close did not return: the download goroutine is still running")
	}
}

const ssmlHead = `<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="https://www.w3.org/2001/mstts" xml:lang="`

func TestSynthesizeRequest(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv)

	got, err := readAll(t, e, "Hello, world!")
	if err != nil {
		t.Fatal(err)
	}

	if want := audioFor("Hello, world!"); got != want {
		t.Errorf("audio = %q, want %q", got, want)
	}
	if rec.count() != 1 {
		t.Fatalf("%d requests, want 1", rec.count())
	}
	r := rec.at(0)
	if r.method != http.MethodPost {
		t.Errorf("method = %s, want POST", r.method)
	}
	if r.path != "/"+endpointPath {
		t.Errorf("path = %q, want %q", r.path, "/"+endpointPath)
	}
	for header, want := range map[string]string{
		"Ocp-Apim-Subscription-Key": testKey,
		"Content-Type":              "application/ssml+xml",
		"X-Microsoft-OutputFormat":  "audio-24khz-48kbitrate-mono-mp3",
		"User-Agent":                UserAgent,
	} {
		if got := r.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := r.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none: the key travels in Ocp-Apim-Subscription-Key", got)
	}
	want := ssmlHead + `en-US"><voice name="en-US-JennyNeural">Hello, world!</voice></speak>`
	if r.body != want {
		t.Errorf("body =\n%s\nwant\n%s", r.body, want)
	}
}

func TestSSML(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "defaults",
			want: ssmlHead + `en-US"><voice name="en-US-JennyNeural">Szia!</voice></speak>`,
		},
		{
			name: "voice sets the language",
			opts: []Option{WithVoice(VoiceHuHUNoemi)},
			want: ssmlHead + `hu-HU"><voice name="hu-HU-NoemiNeural">Szia!</voice></speak>`,
		},
		{
			name: "language overrides the voice's",
			opts: []Option{WithVoice(VoiceEnGBSonia), WithLanguage("de-DE")},
			want: ssmlHead + `de-DE"><voice name="en-GB-SoniaNeural">Szia!</voice></speak>`,
		},
		{
			name: "style",
			opts: []Option{WithStyle("cheerful")},
			want: ssmlHead + `en-US"><voice name="en-US-JennyNeural"><mstts:express-as style="cheerful">Szia!</mstts:express-as></voice></speak>`,
		},
		{
			name: "rate",
			opts: []Option{WithRate("+10%")},
			want: ssmlHead + `en-US"><voice name="en-US-JennyNeural"><prosody rate="+10%">Szia!</prosody></voice></speak>`,
		},
		{
			name: "pitch",
			opts: []Option{WithPitch("-2st")},
			want: ssmlHead + `en-US"><voice name="en-US-JennyNeural"><prosody pitch="-2st">Szia!</prosody></voice></speak>`,
		},
		{
			name: "everything",
			opts: []Option{WithVoice(VoiceHuHUNoemi), WithStyle("sad"), WithRate("0.9"), WithPitch("low")},
			want: ssmlHead + `hu-HU"><voice name="hu-HU-NoemiNeural"><mstts:express-as style="sad"><prosody rate="0.9" pitch="low">Szia!</prosody></mstts:express-as></voice></speak>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(append([]Option{WithKey(testKey), WithRegion("westeurope")}, tt.opts...)...)
			if e.err != nil {
				t.Fatalf("unexpected option error: %v", e.err)
			}
			if got := e.ssml("Szia!"); got != tt.want {
				t.Errorf("ssml =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestSSMLEscapesSpecialCharacters(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	const text = `Tom & Jerry <3 "quotes" and 'apostrophes'`
	e := newEngine(srv, WithVoice(`x"y`), WithStyle("a&b"))

	got, err := readAll(t, e, text)
	if err != nil {
		t.Fatal(err)
	}

	// The fake endpoint decodes the XML, so the audio proves that the
	// escaped text reads back as the original.
	if want := audioFor(text); got != want {
		t.Errorf("audio = %q, want %q", got, want)
	}
	body := rec.at(0).body
	for _, want := range []string{
		`Tom &amp; Jerry &lt;3 &#34;quotes&#34; and &#39;apostrophes&#39;`,
		`<voice name="x&#34;y">`,
		`<mstts:express-as style="a&amp;b">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q does not contain %q", body, want)
		}
	}
	for _, raw := range []string{"&J", "<3", `"quotes"`} {
		if strings.Contains(body, raw) {
			t.Errorf("body %q contains the unescaped %q", body, raw)
		}
	}
}

func TestLanguageOf(t *testing.T) {
	tests := map[Voice]string{
		"hu-HU-NoemiNeural":                "hu-HU",
		"en-US-JennyNeural":                "en-US",
		"en-US-Ava:DragonHDLatestNeural":   "en-US",
		"zh-CN-XiaoxiaoMultilingualNeural": "zh-CN",
		"wuu-CN-XiaotongNeural":            "wuu-CN",
		"sr-Latn-RS-NicholasNeural":        "sr-Latn",
		"my-custom-voice":                  "en-US",
		"JennyNeural":                      "en-US",
	}
	for voice, want := range tests {
		if got := languageOf(voice); got != want {
			t.Errorf("languageOf(%q) = %q, want %q", voice, got, want)
		}
	}
}

func TestFormats(t *testing.T) {
	for format, name := range outputFormats {
		t.Run(string(format), func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := newEngine(srv, WithFormat(format))

			if got := e.Format(); got != format {
				t.Errorf("Format() = %q, want %q", got, format)
			}
			if _, err := readAll(t, e, "Hello"); err != nil {
				t.Fatal(err)
			}
			if got := rec.at(0).header.Get("X-Microsoft-OutputFormat"); got != name {
				t.Errorf("X-Microsoft-OutputFormat = %q, want %q", got, name)
			}
		})
	}
	for _, format := range []engine.Format{engine.FormatAAC, engine.FormatFLAC, "ogg", ""} {
		t.Run("unsupported "+string(format), func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := newEngine(srv, WithFormat(format))

			_, err := e.Synthesize(context.Background(), "Hello")

			if err == nil || !strings.Contains(err.Error(), "unsupported format") {
				t.Errorf("error = %v, want one about an unsupported format", err)
			}
			if rec.count() != 0 {
				t.Errorf("%d requests were sent", rec.count())
			}
		})
	}
}

func TestWithOutputFormat(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	const name = "audio-48khz-192kbitrate-mono-mp3"
	e := newEngine(srv, WithOutputFormat(name, engine.FormatMP3))

	if got := e.Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want mp3", got)
	}
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(0).header.Get("X-Microsoft-OutputFormat"); got != name {
		t.Errorf("X-Microsoft-OutputFormat = %q, want %q", got, name)
	}
	if fp := e.Fingerprint(); !strings.Contains(fp, name) {
		t.Errorf("Fingerprint() = %q does not name the output format %q", fp, name)
	}
	// The default name of a format leaves the fingerprint alone.
	plain := newEngine(srv, WithOutputFormat(outputFormats[engine.FormatWAV], engine.FormatWAV))
	if got, want := plain.Fingerprint(), newEngine(srv, WithFormat(engine.FormatWAV)).Fingerprint(); got != want {
		t.Errorf("Fingerprint() = %q with the default name, want %q", got, want)
	}
}

func TestSynthesizeSplitsLongText(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	text, chunks := twoChunks()
	e := newEngine(srv)

	got, err := readAll(t, e, text)
	if err != nil {
		t.Fatal(err)
	}

	if want := audioFor(chunks[0]) + audioFor(chunks[1]); got != want {
		t.Errorf("audio = %q, want the pieces in order %q", got, want)
	}
	if rec.count() != len(chunks) {
		t.Fatalf("%d requests, want %d", rec.count(), len(chunks))
	}
	for i, chunk := range chunks {
		got, err := textOf(rec.at(i).body)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if got != chunk {
			t.Errorf("request %d text = %.20q (%d runes), want piece %d (%d runes)", i, got, utf8.RuneCountInString(got), i, utf8.RuneCountInString(chunk))
		}
	}
}

func TestSynthesizeStreamsBeforeTheNextChunk(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	text, chunks := twoChunks()
	var secondServed atomic.Bool
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte(chunks[1])) {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			secondServed.Store(true)
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		serveAudio(w, r)
	})
	e := newEngine(srv)

	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	first := make([]byte, len(audioFor(chunks[0])))
	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(rc, first)
		readErr <- err
	}()
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("reading the first piece: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first piece is not readable while the second request is pending")
	}
	if string(first) != audioFor(chunks[0]) {
		t.Errorf("first piece = %q, want %q", first, audioFor(chunks[0]))
	}
	if secondServed.Load() {
		t.Error("the second piece was served before the first one was read")
	}

	unblock()
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if want := audioFor(chunks[1]); string(rest) != want {
		t.Errorf("rest = %q, want %q", rest, want)
	}
}

func TestSynthesizeAPIError(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantMessage string
	}{
		{
			name:        "error document",
			status:      http.StatusUnauthorized,
			contentType: "application/json",
			body:        `{"error":{"code":"401","message":"Access denied due to invalid subscription key or wrong API endpoint. Make sure to provide a valid key for an active subscription."}}`,
			wantMessage: "Access denied due to invalid subscription key or wrong API endpoint. Make sure to provide a valid key for an active subscription.",
		},
		{
			name:        "plain text",
			status:      http.StatusBadRequest,
			contentType: "text/plain",
			body:        "Voice not found: en-US-NobodyNeural",
			wantMessage: "Voice not found: en-US-NobodyNeural",
		},
		{
			name:        "empty body",
			status:      http.StatusTooManyRequests,
			contentType: "",
			body:        "",
			wantMessage: "",
		},
		{
			name:        "html",
			status:      http.StatusServiceUnavailable,
			contentType: "text/html",
			body:        "<html><body><h1>Service Unavailable</h1></body></html>",
			wantMessage: "<html><body><h1>Service Unavailable</h1></body></html>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			})
			e := newEngine(srv)

			_, err := readAll(t, e, "Hello")

			var httpErr *engine.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("error is %T (%v), want *engine.HTTPError", err, err)
			}
			if httpErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, tt.status)
			}
			if httpErr.URL != endpointOf(srv) {
				t.Errorf("URL = %q, want %q", httpErr.URL, endpointOf(srv))
			}
			if httpErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", httpErr.Message, tt.wantMessage)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error %q contains the resource key", err)
			}
			if !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
			}
		})
	}
}

func TestSynthesizeUnexpectedContentType(t *testing.T) {
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<html>Sign in</html>")
	})
	e := newEngine(srv)

	_, err := readAll(t, e, "Hello")

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is %T (%v), want *engine.HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Message, `unexpected content type "text/html; charset=utf-8"`) {
		t.Errorf("Message = %q, want one about the content type", httpErr.Message)
	}
}

func TestSynthesizeAcceptsOtherAudioContentTypes(t *testing.T) {
	for name, contentType := range map[string]string{
		"octet stream": "application/octet-stream",
		"x-wav":        "audio/x-wav",
		"none":         "",
	} {
		t.Run(name, func(t *testing.T) {
			srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
				if contentType == "" {
					// Keep the server from sniffing a type for the body.
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", contentType)
				}
				io.WriteString(w, "audio")
			})
			e := newEngine(srv)

			got, err := readAll(t, e, "Hello")
			if err != nil {
				t.Fatalf("Synthesize failed for Content-Type %q: %v", contentType, err)
			}
			if got != "audio" {
				t.Errorf("audio = %q, want %q", got, "audio")
			}
		})
	}
}

func TestSynthesizeContextCancelled(t *testing.T) {
	text, chunks := twoChunks()
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte(chunks[1])) {
			<-r.Context().Done()
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		serveAudio(w, r)
	})
	e := newEngine(srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rc, err := e.Synthesize(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len(audioFor(chunks[0])))
	if _, err := io.ReadFull(rc, first); err != nil {
		t.Fatalf("reading the first piece: %v", err)
	}

	cancel()
	rest, err := io.ReadAll(rc)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error is %v, want one that wraps context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "chunk 2 of 2") {
		t.Errorf("error %q does not name the second chunk", err)
	}
	if len(rest) != 0 {
		t.Errorf("read %q after the cancellation, want nothing", rest)
	}
	closeWithin(t, rc, 5*time.Second)
}

func TestSynthesizeCancelledBeforeStart(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rc, err := e.Synthesize(ctx, "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error is %v, want one that wraps context.Canceled", err)
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent for a cancelled context", rec.count())
	}
}

func TestSynthesizeTimeout(t *testing.T) {
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	e := newEngine(srv, WithTimeout(50*time.Millisecond))

	_, err := readAll(t, e, "Hello")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error is %v, want one that wraps context.DeadlineExceeded", err)
	}
}

func TestCloseStopsFurtherRequests(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	text, _ := twoChunks()
	e := newEngine(srv)

	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(rc, first); err != nil {
		t.Fatalf("reading the first byte: %v", err)
	}

	closeWithin(t, rc, 5*time.Second)

	if rec.count() != 1 {
		t.Errorf("%d requests were sent, want only the first one before Close", rec.count())
	}
	if _, err := rc.Read(make([]byte, 1)); err == nil {
		t.Error("Read after Close returned no error")
	}
	// A second Close is harmless.
	closeWithin(t, rc, 5*time.Second)
}

func TestKeyFromEnvironment(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	t.Setenv(EnvKey, "  env-key  ")

	e := New(WithEndpoint(endpointOf(srv)))
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(0).header.Get("Ocp-Apim-Subscription-Key"); got != "env-key" {
		t.Errorf("key = %q, want the trimmed environment variable", got)
	}

	// The option wins over the environment.
	e = New(WithEndpoint(endpointOf(srv)), WithKey(" option-key "))
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(1).header.Get("Ocp-Apim-Subscription-Key"); got != "option-key" {
		t.Errorf("key = %q, want the trimmed option", got)
	}
}

func TestMissingKey(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	t.Setenv(EnvKey, "")
	e := New(WithEndpoint(endpointOf(srv)))

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Error("Synthesize returned a reader without a key")
	}
	if err == nil || !strings.Contains(err.Error(), "WithKey") || !strings.Contains(err.Error(), EnvKey) {
		t.Errorf("error = %v, want one that names WithKey and %s", err, EnvKey)
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent without a key", rec.count())
	}
}

func TestRegion(t *testing.T) {
	const want = "https://westeurope.tts.speech.microsoft.com/cognitiveservices/v1"
	t.Run("option", func(t *testing.T) {
		e := New(WithKey(testKey), WithRegion(" WestEurope "))
		if e.err != nil {
			t.Fatal(e.err)
		}
		if e.endpoint != want {
			t.Errorf("endpoint = %q, want %q", e.endpoint, want)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv(EnvRegion, "westeurope")
		e := New(WithKey(testKey))
		if e.err != nil {
			t.Fatal(e.err)
		}
		if e.endpoint != want {
			t.Errorf("endpoint = %q, want %q", e.endpoint, want)
		}
	})
	t.Run("missing", func(t *testing.T) {
		t.Setenv(EnvRegion, "")
		e := New(WithKey(testKey))
		_, err := e.Synthesize(context.Background(), "Hello")
		if err == nil || !strings.Contains(err.Error(), "WithRegion") || !strings.Contains(err.Error(), EnvRegion) {
			t.Errorf("error = %v, want one that names WithRegion and %s", err, EnvRegion)
		}
	})
	t.Run("the later option wins", func(t *testing.T) {
		const custom = "https://example.com/tts?deploymentId=abc"
		e := New(WithKey(testKey), WithRegion("eastus"), WithEndpoint(custom))
		if e.endpoint != custom {
			t.Errorf("endpoint = %q after WithEndpoint, want %q", e.endpoint, custom)
		}
		e = New(WithKey(testKey), WithEndpoint(custom), WithRegion("westeurope"))
		if e.endpoint != want {
			t.Errorf("endpoint = %q after WithRegion, want %q", e.endpoint, want)
		}
	})
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want string
	}{
		{"region with a space", WithRegion("West Europe"), "invalid region"},
		{"empty region", WithRegion(""), "invalid region"},
		{"empty voice", WithVoice("  "), "empty voice"},
		{"empty language", WithLanguage(""), "empty language"},
		{"aac", WithFormat(engine.FormatAAC), "unsupported format"},
		{"empty output format", WithOutputFormat("", engine.FormatMP3), "empty output format"},
		{"output format without an engine format", WithOutputFormat("audio-48khz-96kbitrate-mono-mp3", ""), "empty engine format"},
		{"empty rate", WithRate(""), "empty rate"},
		{"empty pitch", WithPitch(" "), "empty pitch"},
		{"empty style", WithStyle(""), "empty style"},
		{"nil client", WithHTTPClient(nil), "nil HTTP client"},
		{"negative timeout", WithTimeout(-time.Second), "negative timeout"},
		{"endpoint scheme", WithEndpoint("ftp://example.com/tts"), "unsupported scheme"},
		{"endpoint without host", WithEndpoint("/cognitiveservices/v1"), "missing host"},
		{"endpoint not a URL", WithEndpoint("http://[::1"), "invalid endpoint URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := newEngine(srv, tt.opt)

			rc, err := e.Synthesize(context.Background(), "Hello")

			if rc != nil {
				rc.Close()
				t.Error("Synthesize returned a reader for an invalid option")
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one containing %q", err, tt.want)
			}
			if rec.count() != 0 {
				t.Errorf("%d requests were sent", rec.count())
			}
		})
	}

	t.Run("the first error wins", func(t *testing.T) {
		srv := newEndpoint(t, nil, nil)
		e := newEngine(srv, WithVoice(""), WithRate(""))
		_, err := e.Synthesize(context.Background(), "Hello")
		if err == nil || !strings.Contains(err.Error(), "empty voice") {
			t.Errorf("error = %v, want the first one, about the voice", err)
		}
	})
}

func TestWithHTTPClient(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	e := newEngine(srv, WithHTTPClient(client))

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	if calls.Load() != 1 {
		t.Errorf("the client was used %d times, want 1", calls.Load())
	}
	if rec.count() != 1 {
		t.Errorf("%d requests arrived, want 1", rec.count())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSynthesizeEmptyText(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv)

	for _, text := range []string{"", "   ", "\n\t "} {
		rc, err := e.Synthesize(context.Background(), text)
		if rc != nil {
			rc.Close()
			t.Errorf("Synthesize(%q) returned a reader", text)
		}
		if !errors.Is(err, engine.ErrEmptyText) {
			t.Errorf("Synthesize(%q) error is %v, want engine.ErrEmptyText", text, err)
		}
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent for empty text", rec.count())
	}
}

func TestFormatDefault(t *testing.T) {
	if got := New(WithKey(testKey), WithRegion("westeurope")).Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want mp3", got)
	}
}

func TestFingerprint(t *testing.T) {
	base := []Option{WithKey(testKey), WithRegion("westeurope")}
	fingerprint := func(opts ...Option) string {
		return New(append(append([]Option{}, base...), opts...)...).Fingerprint()
	}

	if got, want := fingerprint(), "azure-en-US-JennyNeural"; got != want {
		t.Errorf("default Fingerprint() = %q, want %q", got, want)
	}
	if got, want := fingerprint(WithVoice(VoiceHuHUNoemi)), "azure-hu-HU-NoemiNeural"; got != want {
		t.Errorf("Fingerprint() = %q, want %q", got, want)
	}
	// The format is the extension of the cached file, not part of the name.
	if got, want := fingerprint(WithFormat(engine.FormatWAV)), fingerprint(); got != want {
		t.Errorf("Fingerprint() = %q with WAV, want %q", got, want)
	}
	// The language does not change the sound of a voice.
	if got, want := fingerprint(WithLanguage("de-DE")), fingerprint(); got != want {
		t.Errorf("Fingerprint() = %q with a language, want %q", got, want)
	}

	styled := fingerprint(WithRate("+10%"), WithPitch("-2st"), WithStyle("cheerful"))
	if !strings.HasPrefix(styled, "azure-en-US-JennyNeural-") || len(styled) != len("azure-en-US-JennyNeural-")+8 {
		t.Errorf("Fingerprint() = %q, want the voice followed by an eight-digit hash", styled)
	}
	if again := fingerprint(WithRate("+10%"), WithPitch("-2st"), WithStyle("cheerful")); again != styled {
		t.Errorf("Fingerprint() = %q, want the same %q for the same settings", again, styled)
	}
	for name, other := range map[string]string{
		"rate":  fingerprint(WithRate("-10%"), WithPitch("-2st"), WithStyle("cheerful")),
		"pitch": fingerprint(WithRate("+10%"), WithPitch("+2st"), WithStyle("cheerful")),
		"style": fingerprint(WithRate("+10%"), WithPitch("-2st"), WithStyle("sad")),
		"none":  fingerprint(),
	} {
		if other == styled {
			t.Errorf("Fingerprint() = %q with another %s, want a different one", other, name)
		}
	}

	custom := fingerprint(WithVoice("my voice/1"), WithOutputFormat("riff-16khz-16bit-mono-pcm", engine.FormatWAV))
	if want := "azure-my-voice-1-riff-16khz-16bit-mono-pcm"; custom != want {
		t.Errorf("Fingerprint() = %q, want %q", custom, want)
	}
	for _, fp := range []string{styled, custom} {
		if strings.Contains(fp, testKey) || strings.Contains(fp, "westeurope") {
			t.Errorf("Fingerprint() = %q carries the key or the region", fp)
		}
	}
}
