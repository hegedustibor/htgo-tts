package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// testKey is the API key of the tests; it must never show up in an error or
// a fingerprint.
const testKey = "sk_0123456789abcdef0123456789abcdef"

// defaultPath is the path of a speech request for DefaultVoice under the
// fake server's base URL, which ends in /v1 like the real one.
const defaultPath = "/v1/text-to-speech/" + string(DefaultVoice) + "/stream"

// recorder keeps what the fake endpoint received.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	method      string
	path        string
	escapedPath string
	query       url.Values
	header      http.Header
	body        string // the JSON document of the request
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

// decodeBody parses a request body as the generic JSON document that the
// service sees.
func decodeBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("body %q is not JSON: %v", raw, err)
	}
	return doc
}

// textOf returns the text field of a request body.
func textOf(raw []byte) (string, error) {
	var doc struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	return doc.Text, nil
}

// contentTypeFor is the Content-Type the fake endpoint answers for an output
// format: the one of the real service for MP3, an octet stream for raw PCM,
// as a service may answer it, and a generic audio type otherwise.
func contentTypeFor(outputFormat string) string {
	switch {
	case strings.HasPrefix(outputFormat, "mp3_"):
		return "audio/mpeg"
	case strings.HasPrefix(outputFormat, "pcm_"):
		return "application/octet-stream"
	case strings.HasPrefix(outputFormat, "opus_"):
		return "audio/ogg"
	}
	return "audio/basic"
}

// audioFor is the distinct audio that the fake endpoint answers for a text:
// its length in runes, so that the pieces of a long text can be told apart.
func audioFor(text string) string {
	return "audio[" + strconv.Itoa(utf8.RuneCountInString(text)) + "];"
}

// serveAudio answers a speech request with audioFor of the text in its body,
// in the Content-Type of its output format.
func serveAudio(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text, err := textOf(raw)
	if err != nil {
		http.Error(w, "malformed JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(r.URL.Query().Get("output_format")))
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
				method:      r.Method,
				path:        r.URL.Path,
				escapedPath: r.URL.EscapedPath(),
				query:       r.URL.Query(),
				header:      r.Header.Clone(),
				body:        string(raw),
			})
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// baseURLOf is the base URL of the fake server, with the path of the real
// one.
func baseURLOf(srv *httptest.Server) string {
	return srv.URL + "/v1"
}

// newEngine returns an engine that talks to the fake endpoint at srv with
// testKey, plus the given options.
func newEngine(srv *httptest.Server, opts ...Option) *Engine {
	return New(append([]Option{WithKey(testKey), WithBaseURL(baseURLOf(srv))}, opts...)...)
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
	if r.path != defaultPath {
		t.Errorf("path = %q, want %q", r.path, defaultPath)
	}
	wantQuery := url.Values{"output_format": {"mp3_44100_128"}}
	if !reflect.DeepEqual(r.query, wantQuery) {
		t.Errorf("query = %v, want %v", r.query, wantQuery)
	}
	for header, want := range map[string]string{
		"Xi-Api-Key":   testKey,
		"Content-Type": "application/json",
		"Accept":       "audio/mpeg",
	} {
		if got := r.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := r.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none: the key travels in xi-api-key", got)
	}
	want := map[string]any{"text": "Hello, world!", "model_id": "eleven_multilingual_v2"}
	if body := decodeBody(t, r.body); !reflect.DeepEqual(body, want) {
		t.Errorf("body = %v, want %v", body, want)
	}
}

func TestBody(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want map[string]any
	}{
		{
			name: "defaults",
			want: map[string]any{"text": "Szia!", "model_id": "eleven_multilingual_v2"},
		},
		{
			name: "voice, model and language",
			opts: []Option{WithVoice(VoiceSarah), WithModel(ModelFlashV25), WithLanguage("hu")},
			want: map[string]any{"text": "Szia!", "model_id": "eleven_flash_v2_5", "language_code": "hu"},
		},
		{
			name: "one setting",
			opts: []Option{WithSpeed(1.1)},
			want: map[string]any{"text": "Szia!", "model_id": "eleven_multilingual_v2", "voice_settings": map[string]any{"speed": 1.1}},
		},
		{
			name: "zero and false are sent when set",
			opts: []Option{WithStyle(0), WithSpeakerBoost(false)},
			want: map[string]any{"text": "Szia!", "model_id": "eleven_multilingual_v2", "voice_settings": map[string]any{"style": 0.0, "use_speaker_boost": false}},
		},
		{
			name: "everything",
			opts: []Option{WithModel(ModelV4), WithLanguage("de"), WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(0.9)},
			want: map[string]any{
				"text":          "Szia!",
				"model_id":      "eleven_v4",
				"language_code": "de",
				"voice_settings": map[string]any{
					"stability":         0.4,
					"similarity_boost":  0.8,
					"style":             0.2,
					"use_speaker_boost": true,
					"speed":             0.9,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(append([]Option{WithKey(testKey)}, tt.opts...)...)
			if e.err != nil {
				t.Fatalf("unexpected option error: %v", e.err)
			}
			raw, err := e.body("Szia!", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeBody(t, string(raw)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("body = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("context", func(t *testing.T) {
		e := New(WithKey(testKey))
		raw, err := e.body("Second.", "First.", "Third.")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"text": "Second.", "model_id": "eleven_multilingual_v2", "previous_text": "First.", "next_text": "Third."}
		if got := decodeBody(t, string(raw)); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %v, want %v", got, want)
		}
	})
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
			got, err := readAll(t, e, "Hello")
			if err != nil {
				t.Fatal(err)
			}
			if want := audioFor("Hello"); got != want {
				t.Errorf("audio = %q, want %q", got, want)
			}
			r := rec.at(0)
			if got := r.query.Get("output_format"); got != name {
				t.Errorf("output_format = %q, want %q", got, name)
			}
			accept := r.header.Get("Accept")
			if format == engine.FormatMP3 && accept != "audio/mpeg" {
				t.Errorf("Accept = %q for MP3, want audio/mpeg", accept)
			}
			if format != engine.FormatMP3 && accept != "" {
				t.Errorf("Accept = %q for %s, want none", accept, format)
			}
		})
	}
	for _, format := range []engine.Format{engine.FormatWAV, engine.FormatAAC, engine.FormatFLAC, "ogg", ""} {
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
	const name = "mp3_22050_32"
	e := newEngine(srv, WithOutputFormat(name, engine.FormatMP3))

	if got := e.Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want mp3", got)
	}
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(0).query.Get("output_format"); got != name {
		t.Errorf("output_format = %q, want %q", got, name)
	}
	if fp := e.Fingerprint(); !strings.Contains(fp, name) {
		t.Errorf("Fingerprint() = %q does not name the output format %q", fp, name)
	}
	// The default name of a format leaves the fingerprint alone.
	plain := newEngine(srv, WithOutputFormat(outputFormats[engine.FormatPCM], engine.FormatPCM))
	if got, want := plain.Fingerprint(), newEngine(srv, WithFormat(engine.FormatPCM)).Fingerprint(); got != want {
		t.Errorf("Fingerprint() = %q with the default name, want %q", got, want)
	}
	// A format of the service that the engine package has no constant for.
	ulaw := newEngine(srv, WithOutputFormat("ulaw_8000", engine.Format("ulaw")))
	if got := ulaw.Format(); got != "ulaw" {
		t.Errorf("Format() = %q, want ulaw", got)
	}
	if _, err := readAll(t, ulaw, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(1).query.Get("output_format"); got != "ulaw_8000" {
		t.Errorf("output_format = %q, want ulaw_8000", got)
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
		body := decodeBody(t, rec.at(i).body)
		if got, _ := body["text"].(string); got != chunk {
			t.Errorf("request %d text = %.20q (%d runes), want piece %d (%d runes)", i, got, utf8.RuneCountInString(got), i, utf8.RuneCountInString(chunk))
		}
	}
	// Each piece carries its neighbours, and only them, for continuity.
	first, second := decodeBody(t, rec.at(0).body), decodeBody(t, rec.at(1).body)
	if got, ok := first["next_text"]; !ok || got != chunks[1] {
		t.Errorf("first request next_text = %.20q, want the second piece", got)
	}
	if _, ok := first["previous_text"]; ok {
		t.Error("first request carries a previous_text")
	}
	if got, ok := second["previous_text"]; !ok || got != chunks[0] {
		t.Errorf("second request previous_text = %.20q, want the first piece", got)
	}
	if _, ok := second["next_text"]; ok {
		t.Error("second request carries a next_text")
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
		if text, _ := textOf(raw); text == chunks[1] {
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
			body:        `{"detail":{"type":"authentication_error","code":"invalid_api_key","message":"Invalid API key provided.","status":"invalid_api_key","request_id":"3c807fc4c3a1705f9638ecc764a91c01"}}`,
			wantMessage: "invalid_api_key: Invalid API key provided.",
		},
		{
			name:        "error document with the legacy status field",
			status:      http.StatusTooManyRequests,
			contentType: "application/json",
			body:        `{"detail":{"status":"too_many_concurrent_requests","message":"You have exceeded the concurrent request limit of your plan."}}`,
			wantMessage: "too_many_concurrent_requests: You have exceeded the concurrent request limit of your plan.",
		},
		{
			name:        "validation error",
			status:      http.StatusUnprocessableEntity,
			contentType: "application/json",
			body:        `{"detail":[{"loc":["body","voice_settings","speed"],"msg":"Input should be less than or equal to 1.2","type":"less_than_equal"}]}`,
			wantMessage: "body.voice_settings.speed: Input should be less than or equal to 1.2",
		},
		{
			name:        "string detail",
			status:      http.StatusNotFound,
			contentType: "application/json",
			body:        `{"detail":"Voice not found"}`,
			wantMessage: "Voice not found",
		},
		{
			name:        "plain text",
			status:      http.StatusInternalServerError,
			contentType: "text/plain",
			body:        "Internal Server Error",
			wantMessage: "Internal Server Error",
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
			if want := srv.URL + defaultPath; httpErr.URL != want {
				t.Errorf("URL = %q, want %q without the query", httpErr.URL, want)
			}
			if httpErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", httpErr.Message, tt.wantMessage)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error %q contains the API key", err)
			}
			if !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"not JSON", "<html>", ""},
		{"no detail", `{"error":"x"}`, ""},
		{"empty detail object", `{"detail":{}}`, ""},
		{"code without a message", `{"detail":{"code":"quota_exceeded"}}`, "quota_exceeded"},
		{"message without a code", `{"detail":{"message":"Something\n  went\twrong"}}`, "Something went wrong"},
		{"code wins over status", `{"detail":{"code":"invalid_api_key","status":"legacy","message":"m"}}`, "invalid_api_key: m"},
		{"several validation errors", `{"detail":[{"loc":["body","text"],"msg":"Field required","type":"missing"},{"loc":["query","output_format"],"msg":"Input should be one of the formats","type":"enum"}]}`, "body.text: Field required; query.output_format: Input should be one of the formats"},
		{"validation error with an index", `{"detail":[{"loc":["body","pronunciation_dictionary_locators",1,"pronunciation_dictionary_id"],"msg":"Field required","type":"missing"}]}`, "body.pronunciation_dictionary_locators.1.pronunciation_dictionary_id: Field required"},
		{"validation error without a location", `{"detail":[{"loc":[],"msg":"Field required","type":"missing"}]}`, "Field required"},
		{"empty list", `{"detail":[]}`, ""},
		{"detail is a number", `{"detail":42}`, ""},
		{"long message is truncated at a rune boundary", `{"detail":{"message":"` + strings.Repeat("ű", 300) + `"}}`, strings.Repeat("ű", maxMessageBytes/2) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorMessage([]byte(tt.raw)); got != tt.want {
				t.Errorf("errorMessage(%q) = %q, want %q", tt.raw, got, tt.want)
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
		"pcm":          "audio/pcm",
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
			e := newEngine(srv, WithFormat(engine.FormatPCM))

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
		if text, _ := textOf(raw); text == chunks[1] {
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

	e := New(WithBaseURL(baseURLOf(srv)))
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(0).header.Get("Xi-Api-Key"); got != "env-key" {
		t.Errorf("key = %q, want the trimmed environment variable", got)
	}

	// The option wins over the environment.
	e = New(WithBaseURL(baseURLOf(srv)), WithKey(" option-key "))
	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}
	if got := rec.at(1).header.Get("Xi-Api-Key"); got != "option-key" {
		t.Errorf("key = %q, want the trimmed option", got)
	}
}

func TestMissingKey(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	t.Setenv(EnvKey, "")
	e := New(WithBaseURL(baseURLOf(srv)))

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

func TestBaseURL(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		e := New(WithKey(testKey))
		if e.err != nil {
			t.Fatal(e.err)
		}
		want := "https://api.elevenlabs.io/v1/text-to-speech/JBFqnCBsd6RMkjVDRZzb/stream?output_format=mp3_44100_128"
		if got := e.endpoint(); got != want {
			t.Errorf("endpoint = %q, want %q", got, want)
		}
	})
	t.Run("data residency", func(t *testing.T) {
		e := New(WithKey(testKey), WithBaseURL("https://api.eu.residency.elevenlabs.io/v1"), WithVoice(VoiceSarah), WithFormat(engine.FormatPCM))
		want := "https://api.eu.residency.elevenlabs.io/v1/text-to-speech/EXAVITQu4vr4xnSDxMaL/stream?output_format=pcm_24000"
		if got := e.endpoint(); got != want {
			t.Errorf("endpoint = %q, want %q", got, want)
		}
	})
	t.Run("trailing slash and query are kept apart", func(t *testing.T) {
		rec := &recorder{}
		srv := newEndpoint(t, rec, nil)
		e := New(WithKey(testKey), WithBaseURL(srv.URL+"/v1/?tenant=acme"))

		if _, err := readAll(t, e, "Hello"); err != nil {
			t.Fatal(err)
		}

		r := rec.at(0)
		if r.path != defaultPath {
			t.Errorf("path = %q, want %q", r.path, defaultPath)
		}
		want := url.Values{"tenant": {"acme"}, "output_format": {"mp3_44100_128"}}
		if !reflect.DeepEqual(r.query, want) {
			t.Errorf("query = %v, want %v", r.query, want)
		}
	})
	t.Run("voice is one path segment", func(t *testing.T) {
		rec := &recorder{}
		srv := newEndpoint(t, rec, nil)
		e := newEngine(srv, WithVoice("my voice/1"))

		if _, err := readAll(t, e, "Hello"); err != nil {
			t.Fatal(err)
		}

		if got, want := rec.at(0).escapedPath, "/v1/text-to-speech/my%20voice%2F1/stream"; got != want {
			t.Errorf("escaped path = %q, want %q", got, want)
		}
	})
	t.Run("the later option wins", func(t *testing.T) {
		e := New(WithKey(testKey), WithBaseURL("https://api.us.elevenlabs.io/v1"), WithBaseURL("http://localhost:8000/v1"))
		if got, want := e.baseURL.String(), "http://localhost:8000/v1"; got != want {
			t.Errorf("baseURL = %q, want %q", got, want)
		}
	})
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want string
	}{
		{"empty voice", WithVoice("  "), "empty voice"},
		{"empty model", WithModel(""), "empty model"},
		{"empty language", WithLanguage(" "), "empty language"},
		{"stability above one", WithStability(1.5), "stability 1.5 out of range"},
		{"negative stability", WithStability(-0.1), "stability -0.1 out of range"},
		{"similarity above one", WithSimilarity(2), "similarity 2 out of range"},
		{"negative style", WithStyle(-1), "style -1 out of range"},
		{"slow speed", WithSpeed(0.5), "speed 0.5 out of range"},
		{"fast speed", WithSpeed(1.3), "speed 1.3 out of range"},
		{"speed is not a number", WithSpeed(math.NaN()), "speed NaN out of range"},
		{"wav", WithFormat(engine.FormatWAV), "unsupported format"},
		{"aac", WithFormat(engine.FormatAAC), "unsupported format"},
		{"flac", WithFormat(engine.FormatFLAC), "unsupported format"},
		{"empty output format", WithOutputFormat("", engine.FormatMP3), "empty output format"},
		{"output format without an engine format", WithOutputFormat("mp3_22050_32", ""), "empty engine format"},
		{"nil client", WithHTTPClient(nil), "nil HTTP client"},
		{"negative timeout", WithTimeout(-time.Second), "negative timeout"},
		{"base URL scheme", WithBaseURL("ftp://example.com/v1"), "unsupported scheme"},
		{"base URL without host", WithBaseURL("/v1"), "missing host"},
		{"base URL not a URL", WithBaseURL("http://[::1"), "invalid base URL"},
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
		e := newEngine(srv, WithVoice(""), WithSpeed(5))
		_, err := e.Synthesize(context.Background(), "Hello")
		if err == nil || !strings.Contains(err.Error(), "empty voice") {
			t.Errorf("error = %v, want the first one, about the voice", err)
		}
	})

	t.Run("the ends of a range are valid", func(t *testing.T) {
		e := New(WithKey(testKey), WithStability(0), WithSimilarity(1), WithStyle(1), WithSpeed(0.7))
		if e.err != nil {
			t.Errorf("unexpected error: %v", e.err)
		}
		e = New(WithKey(testKey), WithSpeed(1.2))
		if e.err != nil {
			t.Errorf("unexpected error: %v", e.err)
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
	if got := New(WithKey(testKey)).Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want mp3", got)
	}
}

func TestFingerprint(t *testing.T) {
	fingerprint := func(opts ...Option) string {
		return New(append([]Option{WithKey(testKey), WithBaseURL("https://api.eu.residency.elevenlabs.io/v1")}, opts...)...).Fingerprint()
	}

	plain := fingerprint()
	if want := "elevenlabs-JBFqnCBsd6RMkjVDRZzb-eleven_multilingual_v2"; plain != want {
		t.Errorf("default Fingerprint() = %q, want %q", plain, want)
	}
	if got, want := fingerprint(WithVoice(VoiceSarah), WithModel(ModelFlashV25)), "elevenlabs-EXAVITQu4vr4xnSDxMaL-eleven_flash_v2_5"; got != want {
		t.Errorf("Fingerprint() = %q, want %q", got, want)
	}
	// The format is the extension of the cached file, not part of the name.
	if got := fingerprint(WithFormat(engine.FormatPCM)); got != plain {
		t.Errorf("Fingerprint() = %q with PCM, want %q", got, plain)
	}

	tuned := fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("hu"))
	if !strings.HasPrefix(tuned, plain+"-") || len(tuned) != len(plain)+1+8 {
		t.Errorf("Fingerprint() = %q, want the voice and the model followed by an eight-digit hash", tuned)
	}
	if again := fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("hu")); again != tuned {
		t.Errorf("Fingerprint() = %q, want the same %q for the same settings", again, tuned)
	}
	for name, other := range map[string]string{
		"stability":     fingerprint(WithStability(0.5), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("hu")),
		"similarity":    fingerprint(WithStability(0.4), WithSimilarity(0.9), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("hu")),
		"style":         fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.3), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("hu")),
		"speaker boost": fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(false), WithSpeed(1.1), WithLanguage("hu")),
		"speed":         fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.2), WithLanguage("hu")),
		"language":      fingerprint(WithStability(0.4), WithSimilarity(0.8), WithStyle(0.2), WithSpeakerBoost(true), WithSpeed(1.1), WithLanguage("de")),
		"none":          plain,
	} {
		if other == tuned {
			t.Errorf("Fingerprint() = %q with another %s, want a different one", other, name)
		}
	}
	// A single setting, even a false or a zero one, changes the name.
	for name, fp := range map[string]string{
		"speaker boost off": fingerprint(WithSpeakerBoost(false)),
		"zero style":        fingerprint(WithStyle(0)),
		"language":          fingerprint(WithLanguage("hu")),
	} {
		if fp == plain || !strings.HasPrefix(fp, plain+"-") {
			t.Errorf("Fingerprint() = %q with %s, want %q plus a hash", fp, name, plain)
		}
	}

	custom := fingerprint(WithVoice("my voice/1"), WithOutputFormat("mp3_24000_48", engine.FormatMP3))
	if want := "elevenlabs-my-voice-1-eleven_multilingual_v2-mp3_24000_48"; custom != want {
		t.Errorf("Fingerprint() = %q, want %q", custom, want)
	}
	for _, fp := range []string{tuned, custom} {
		if strings.Contains(fp, testKey) || strings.Contains(fp, "residency") {
			t.Errorf("Fingerprint() = %q carries the key or the base URL", fp)
		}
	}
}
