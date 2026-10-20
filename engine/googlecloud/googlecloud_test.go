package googlecloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// testKey is the API key of the tests; it must never show up in an error, a
// fingerprint or a URL.
const testKey = "AIzaSyTest0123456789abcdefghijklmnopqrs"

// endpointPath is the path of the real endpoint, which the fake one serves
// too.
const endpointPath = "/v1/text:synthesize"

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
	body   map[string]any // the JSON document of the request, nil when the body was not one
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

// textOf returns the text of a recorded request.
func textOf(body map[string]any) string {
	input, _ := body["input"].(map[string]any)
	text, _ := input["text"].(string)
	return text
}

// audioFor is the distinct audio that the fake endpoint answers for a text:
// its length in runes, so that the pieces of a long text can be told apart.
func audioFor(text string) string {
	return "audio[" + strconv.Itoa(utf8.RuneCountInString(text)) + "];"
}

// isEncoding reports whether name is one of the audioEncoding values that
// the engine sends.
func isEncoding(name string) bool {
	for _, n := range encodings {
		if n == name {
			return true
		}
	}
	return false
}

// serveAudio answers a synthesis request with audioFor of its text, the way
// the endpoint does: base64-encoded in a JSON document.
func serveAudio(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input struct {
			Text string `json:"text"`
		} `json:"input"`
		Voice struct {
			LanguageCode string `json:"languageCode"`
		} `json:"voice"`
		AudioConfig struct {
			AudioEncoding string `json:"audioEncoding"`
		} `json:"audioConfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Voice.LanguageCode == "" {
		http.Error(w, "missing languageCode", http.StatusBadRequest)
		return
	}
	if !isEncoding(req.AudioConfig.AudioEncoding) {
		http.Error(w, "unknown audioEncoding "+req.AudioConfig.AudioEncoding, http.StatusBadRequest)
		return
	}
	serveJSON(w, []byte(audioFor(req.Input.Text)))
}

// serveJSON answers with audio the way the endpoint does: base64-encoded in
// the audioContent field of a JSON document.
func serveJSON(w http.ResponseWriter, audio []byte) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	json.NewEncoder(w).Encode(map[string]any{"audioContent": audio})
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
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				body = nil
			}
			rec.add(recorded{
				method: r.Method,
				path:   r.URL.Path,
				query:  r.URL.RawQuery,
				header: r.Header.Clone(),
				body:   body,
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
	return srv.URL + endpointPath
}

// newEngine returns an engine that talks to the fake endpoint at srv with
// testKey, plus the given options.
func newEngine(srv *httptest.Server, opts ...Option) *Engine {
	return New(append([]Option{WithKey(testKey), WithEndpoint(endpointOf(srv))}, opts...)...)
}

// twoChunks returns a text that Synthesize sends in two requests, together
// with the two pieces: a sentence of exactly maxInputRunes runes and a short
// one.
func twoChunks() (string, []string) {
	first := strings.Repeat("a", maxInputRunes-1) + "."
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
		t.Errorf("method = %q, want POST", r.method)
	}
	if r.path != endpointPath {
		t.Errorf("path = %q, want %q", r.path, endpointPath)
	}
	if r.query != "" {
		t.Errorf("query = %q, want none: the key travels in a header", r.query)
	}
	if got := r.header.Get("X-Goog-Api-Key"); got != testKey {
		t.Errorf("x-goog-api-key = %q, want the key", got)
	}
	if got := r.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
	if got := r.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	want := map[string]any{
		"input":       map[string]any{"text": "Hello, world!"},
		"voice":       map[string]any{"languageCode": "en-US"},
		"audioConfig": map[string]any{"audioEncoding": "MP3", "sampleRateHertz": float64(DefaultSampleRate)},
	}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("body = %v, want %v (no voice name, gender, rate, pitch or volume gain)", r.body, want)
	}
}

func TestSynthesizeRequestWithOptions(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv,
		WithVoice(VoiceHuHUWavenetB),
		WithSpeakingRate(1.25),
		WithPitch(-2.5),
		WithVolumeGain(-6),
		WithFormat(engine.FormatWAV),
		WithSampleRate(16000),
	)

	if _, err := readAll(t, e, "Szia"); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"input": map[string]any{"text": "Szia"},
		"voice": map[string]any{"languageCode": "hu-HU", "name": "hu-HU-Wavenet-B"},
		"audioConfig": map[string]any{
			"audioEncoding":   "LINEAR16",
			"sampleRateHertz": float64(16000),
			"speakingRate":    1.25,
			"pitch":           -2.5,
			"volumeGainDb":    float64(-6),
		},
	}
	if got := rec.at(0).body; !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

func TestVoiceSelection(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want map[string]any
	}{
		{
			name: "defaults",
			want: map[string]any{"languageCode": "en-US"},
		},
		{
			name: "voice sets the language",
			opts: []Option{WithVoice(VoiceHuHUWavenetB)},
			want: map[string]any{"languageCode": "hu-HU", "name": "hu-HU-Wavenet-B"},
		},
		{
			name: "language overrides the voice's",
			opts: []Option{WithVoice(VoiceEnGBNeural2A), WithLanguage("de-DE")},
			want: map[string]any{"languageCode": "de-DE", "name": "en-GB-Neural2-A"},
		},
		{
			name: "language given before the voice",
			opts: []Option{WithLanguage("de-DE"), WithVoice(VoiceEnGBNeural2A)},
			want: map[string]any{"languageCode": "de-DE", "name": "en-GB-Neural2-A"},
		},
		{
			name: "language alone",
			opts: []Option{WithLanguage(" de-DE ")},
			want: map[string]any{"languageCode": "de-DE"},
		},
		{
			name: "language and gender",
			opts: []Option{WithLanguage("de-DE"), WithGender(GenderMale)},
			want: map[string]any{"languageCode": "de-DE", "ssmlGender": "MALE"},
		},
		{
			name: "gender alone",
			opts: []Option{WithGender(GenderFemale)},
			want: map[string]any{"languageCode": "en-US", "ssmlGender": "FEMALE"},
		},
		{
			name: "gender in lower case",
			opts: []Option{WithGender(" neutral ")},
			want: map[string]any{"languageCode": "en-US", "ssmlGender": "NEUTRAL"},
		},
		{
			name: "voice and gender",
			opts: []Option{WithVoice(VoiceEnUSNeural2C), WithGender(GenderMale)},
			want: map[string]any{"languageCode": "en-US", "name": "en-US-Neural2-C", "ssmlGender": "MALE"},
		},
		{
			name: "voice without a language in its name",
			opts: []Option{WithVoice("Kore")},
			want: map[string]any{"languageCode": "en-US", "name": "Kore"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := newEngine(srv, tt.opts...)

			if _, err := readAll(t, e, "Hello"); err != nil {
				t.Fatal(err)
			}

			if got := rec.at(0).body["voice"]; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("voice = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSynthesizeOmitsTheDefaults(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv, WithSpeakingRate(1), WithPitch(0), WithVolumeGain(0))

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"audioEncoding": "MP3", "sampleRateHertz": float64(DefaultSampleRate)}
	if got := rec.at(0).body["audioConfig"]; !reflect.DeepEqual(got, want) {
		t.Errorf("audioConfig = %v, want %v: the voice's own rate, pitch and volume are not sent", got, want)
	}
}

func TestFormats(t *testing.T) {
	for format, name := range encodings {
		t.Run(string(format), func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := newEngine(srv, WithFormat(format))

			got, err := readAll(t, e, "Hello")
			if err != nil {
				t.Fatal(err)
			}

			if got != audioFor("Hello") {
				t.Errorf("audio = %q, want %q", got, audioFor("Hello"))
			}
			if e.Format() != format {
				t.Errorf("Format() = %q, want %q", e.Format(), format)
			}
			config, _ := rec.at(0).body["audioConfig"].(map[string]any)
			if got := config["audioEncoding"]; got != name {
				t.Errorf("audioEncoding = %v, want %q", got, name)
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

func TestSynthesizeDecodesTheAudio(t *testing.T) {
	// Every byte value, so that the base64 round trip and the JSON transport
	// are proven on binary data.
	audio := make([]byte, 1024)
	for i := range audio {
		audio[i] = byte(i)
	}
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		serveJSON(w, audio)
	})
	e := newEngine(srv)

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal([]byte(got), audio) {
		t.Errorf("audio differs from the bytes the endpoint encoded: got %d bytes, want %d", len(got), len(audio))
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
		got := textOf(rec.at(i).body)
		if got != chunk {
			t.Errorf("request %d text = %.20q (%d runes), want piece %d (%d runes)", i, got, utf8.RuneCountInString(got), i, utf8.RuneCountInString(chunk))
		}
	}
}

func TestSplitKeepsThePiecesUnderTheByteLimit(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantPieces int // 0 when only the properties matter
	}{
		{"two-byte runes", strings.Repeat("ű", maxInputRunes), 1},
		{"three-byte runes", strings.Repeat("好", maxInputRunes), 1},
		{"four-byte runes", strings.Repeat("😀", maxInputRunes), 2},
		{"four-byte runes in sentences", strings.Repeat("😀😀😀😀😀😀😀😀😀. ", 300), 0},
		{"mixed", strings.Repeat("a😀", 2*maxInputRunes), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pieces := split(tt.text)

			if tt.wantPieces != 0 && len(pieces) != tt.wantPieces {
				t.Errorf("%d pieces, want %d", len(pieces), tt.wantPieces)
			}
			for i, p := range pieces {
				if p == "" || strings.TrimSpace(p) != p {
					t.Errorf("piece %d %q is empty or padded", i, p)
				}
				if len(p) > maxInputBytes {
					t.Errorf("piece %d is %d bytes, more than the endpoint's %d", i, len(p), maxInputBytes)
				}
				if n := utf8.RuneCountInString(p); n > maxInputRunes {
					t.Errorf("piece %d has %d runes, more than %d", i, n, maxInputRunes)
				}
			}
			if got, want := stripSpace(strings.Join(pieces, "")), stripSpace(tt.text); got != want {
				t.Errorf("the pieces lose or reorder runes")
			}
		})
	}
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func TestSynthesizeSplitsFourByteRunesByBytes(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	text := strings.Repeat("😀", maxInputRunes)
	e := newEngine(srv)

	got, err := readAll(t, e, text)
	if err != nil {
		t.Fatal(err)
	}

	if rec.count() != 2 {
		t.Fatalf("%d requests, want 2: %d emoji are %d bytes, more than the endpoint's %d", rec.count(), maxInputRunes, len(text), maxInputBytes)
	}
	for i := range 2 {
		if n := len(textOf(rec.at(i).body)); n > maxInputBytes {
			t.Errorf("request %d carries %d bytes of text, more than %d", i, n, maxInputBytes)
		}
	}
	first, second := textOf(rec.at(0).body), textOf(rec.at(1).body)
	if first+second != text {
		t.Errorf("the requests carry %d and %d runes, want the text in two pieces", utf8.RuneCountInString(first), utf8.RuneCountInString(second))
	}
	if want := audioFor(first) + audioFor(second); got != want {
		t.Errorf("audio = %q, want %q", got, want)
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
			name:        "invalid argument",
			status:      http.StatusBadRequest,
			contentType: "application/json; charset=UTF-8",
			body:        `{"error":{"code":400,"message":"Voice 'hu-HU-Nobody' does not exist. Is it misspelled?","status":"INVALID_ARGUMENT"}}`,
			wantMessage: "Voice 'hu-HU-Nobody' does not exist. Is it misspelled?",
		},
		{
			name:        "invalid API key",
			status:      http.StatusBadRequest,
			contentType: "application/json; charset=UTF-8",
			body:        `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com","metadata":{"service":"texttospeech.googleapis.com"}}]}}`,
			wantMessage: "API key not valid. Please pass a valid API key.",
		},
		{
			name:        "permission denied",
			status:      http.StatusForbidden,
			contentType: "application/json; charset=UTF-8",
			body:        `{"error":{"code":403,"message":"Cloud Text-to-Speech API has not been used in project 123 before or it is disabled.","status":"PERMISSION_DENIED"}}`,
			wantMessage: "Cloud Text-to-Speech API has not been used in project 123 before or it is disabled.",
		},
		{
			name:        "quota exceeded with white space in the message",
			status:      http.StatusTooManyRequests,
			contentType: "application/json; charset=UTF-8",
			body:        "{\"error\":{\"code\":429,\"message\":\"Quota exceeded for quota metric 'Requests'\\n  and limit\\t'Requests per minute'.\",\"status\":\"RESOURCE_EXHAUSTED\"}}",
			wantMessage: "Quota exceeded for quota metric 'Requests' and limit 'Requests per minute'.",
		},
		{
			name:        "JSON without an error object",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"detail": "input is required"}`,
			wantMessage: `{"detail": "input is required"}`,
		},
		{
			name:        "html",
			status:      http.StatusServiceUnavailable,
			contentType: "text/html",
			body:        "<html><body><h1>Service Unavailable</h1></body></html>",
			wantMessage: "<html><body><h1>Service Unavailable</h1></body></html>",
		},
		{
			name:        "empty body",
			status:      http.StatusBadGateway,
			contentType: "",
			body:        "",
			wantMessage: "",
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

			got, err := readAll(t, e, "Hello")

			if got != "" {
				t.Errorf("audio = %q, want none", got)
			}
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
				t.Errorf("error %q contains the API key", err)
			}
			if !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
			}
		})
	}
}

func TestSynthesizeLongErrorMessageIsBounded(t *testing.T) {
	long := strings.Repeat("ű", maxMessageBytes)
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 400, "message": long, "status": "INVALID_ARGUMENT"}})
	})
	e := newEngine(srv)

	_, err := readAll(t, e, "Hello")

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is %T (%v), want *engine.HTTPError", err, err)
	}
	if !utf8.ValidString(httpErr.Message) {
		t.Errorf("Message %q is not valid UTF-8", httpErr.Message)
	}
	if !strings.HasSuffix(httpErr.Message, "…") || len(httpErr.Message) > maxMessageBytes+len("…") {
		t.Errorf("Message is %d bytes long, want at most %d ending with an ellipsis", len(httpErr.Message), maxMessageBytes)
	}
}

func TestSynthesizeUnexpectedContentType(t *testing.T) {
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!DOCTYPE html><title>Sign in</title>")
	})
	e := newEngine(srv)

	got, err := readAll(t, e, "Hello")

	if got != "" {
		t.Errorf("audio = %q, want none", got)
	}
	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is %T (%v), want *engine.HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", httpErr.StatusCode)
	}
	if want := `unexpected content type "text/html; charset=utf-8": <!DOCTYPE html><title>Sign in</title>`; httpErr.Message != want {
		t.Errorf("Message = %q, want %q", httpErr.Message, want)
	}
}

func TestSynthesizeMalformedResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // a part of the error
	}{
		{"not JSON", "<html>Sign in</html>", "malformed response"},
		{"truncated JSON", `{"audioContent": "YWJj`, "malformed response"},
		{"no audioContent", `{}`, "no audioContent"},
		{"empty audioContent", `{"audioContent": ""}`, "no audioContent"},
		{"null audioContent", `{"audioContent": null}`, "no audioContent"},
		{"audioContent that is not base64", `{"audioContent": "not base64!"}`, "illegal base64"},
		{"audioContent that is a number", `{"audioContent": 42}`, "malformed response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=UTF-8")
				io.WriteString(w, tt.body)
			})
			e := newEngine(srv)

			got, err := readAll(t, e, "Hello")

			if got != "" {
				t.Errorf("audio = %q, want none", got)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one containing %q", err, tt.want)
			}
			if err != nil && !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
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

func TestAPIKey(t *testing.T) {
	tests := []struct {
		name string
		env  string
		opts []Option
		want string
	}{
		{
			name: "from the environment",
			env:  "env-key",
			want: "env-key",
		},
		{
			name: "the option wins over the environment",
			env:  "env-key",
			opts: []Option{WithKey("option-key")},
			want: "option-key",
		},
		{
			name: "an empty option means the environment",
			env:  "env-key",
			opts: []Option{WithKey("")},
			want: "env-key",
		},
		{
			name: "white space around the key is dropped",
			env:  "",
			opts: []Option{WithKey("  option-key\n")},
			want: "option-key",
		},
		{
			name: "white space around the environment variable is dropped",
			env:  " env-key\n",
			want: "env-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvKey, tt.env)
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := New(append([]Option{WithEndpoint(endpointOf(srv))}, tt.opts...)...)

			if _, err := readAll(t, e, "Hello"); err != nil {
				t.Fatal(err)
			}

			r := rec.at(0)
			if got := r.header.Get("X-Goog-Api-Key"); got != tt.want {
				t.Errorf("x-goog-api-key = %q, want %q", got, tt.want)
			}
			if r.query != "" {
				t.Errorf("query = %q, want none: the key must not be in the URL", r.query)
			}
		})
	}
}

func TestMissingKey(t *testing.T) {
	t.Setenv(EnvKey, "")
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(WithEndpoint(endpointOf(srv)))

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Fatal("Synthesize returned a reader without an API key")
	}
	if err == nil || !strings.Contains(err.Error(), "WithKey") || !strings.Contains(err.Error(), EnvKey) {
		t.Errorf("error = %v, want one that names WithKey and %s", err, EnvKey)
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent without a key", rec.count())
	}
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name        string
		opt         Option
		wantContain string
		wantMissing string
	}{
		{"speaking rate above the maximum", WithSpeakingRate(2.5), "speaking rate 2.5 is not between 0.25 and 2", ""},
		{"speaking rate below the minimum", WithSpeakingRate(0.1), "speaking rate 0.1 is not between 0.25 and 2", ""},
		{"speaking rate that is not a number", WithSpeakingRate(math.NaN()), "speaking rate NaN is not between", ""},
		{"pitch above the maximum", WithPitch(20.5), "pitch 20.5 is not between -20 and 20", ""},
		{"pitch below the minimum", WithPitch(-21), "pitch -21 is not between -20 and 20", ""},
		{"pitch that is infinite", WithPitch(math.Inf(1)), "pitch +Inf is not between", ""},
		{"volume gain above the maximum", WithVolumeGain(16.5), "volume gain 16.5 is not between -96 and 16", ""},
		{"volume gain below the minimum", WithVolumeGain(-100), "volume gain -100 is not between -96 and 16", ""},
		{"empty voice", WithVoice("  "), "empty voice", ""},
		{"empty language", WithLanguage(""), "empty language", ""},
		{"unknown gender", WithGender("other"), `unknown gender "other"`, ""},
		{"empty gender", WithGender(""), `unknown gender ""`, ""},
		{"aac", WithFormat(engine.FormatAAC), `unsupported format "aac"`, ""},
		{"flac", WithFormat(engine.FormatFLAC), `unsupported format "flac"`, ""},
		{"zero sample rate", WithSampleRate(0), "invalid sample rate 0 Hz", ""},
		{"negative sample rate", WithSampleRate(-8000), "invalid sample rate -8000 Hz", ""},
		{"nil client", WithHTTPClient(nil), "nil HTTP client", ""},
		{"negative timeout", WithTimeout(-time.Second), "negative timeout", ""},
		{"endpoint scheme", WithEndpoint("ftp://example.com/v1/text:synthesize"), `unsupported scheme "ftp"`, ""},
		{"endpoint without host", WithEndpoint("/v1/text:synthesize"), "missing host", ""},
		{"endpoint not a URL", WithEndpoint("http://[::1"), "invalid endpoint URL", ""},
		{"endpoint error hides the password", WithEndpoint("http://user:secret@bad host/v1"), "invalid endpoint URL", "secret"},
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
			if err == nil || !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantContain)
			}
			if tt.wantMissing != "" && err != nil && strings.Contains(err.Error(), tt.wantMissing) {
				t.Errorf("error %q leaks %q", err, tt.wantMissing)
			}
			if rec.count() != 0 {
				t.Errorf("%d requests were sent", rec.count())
			}
		})
	}

	t.Run("the first error wins", func(t *testing.T) {
		srv := newEndpoint(t, nil, nil)
		e := newEngine(srv, WithVoice(""), WithPitch(99))
		_, err := e.Synthesize(context.Background(), "Hello")
		if err == nil || !strings.Contains(err.Error(), "empty voice") {
			t.Errorf("error = %v, want the first one, about the voice", err)
		}
	})
}

func TestWithEndpoint(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(WithKey(testKey), WithEndpoint(srv.URL+"/custom/text:synthesize?alt=json"))

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	r := rec.at(0)
	if r.path != "/custom/text:synthesize" {
		t.Errorf("path = %q, want the path of the endpoint", r.path)
	}
	if r.query != "alt=json" {
		t.Errorf("query = %q, want the query of the endpoint and nothing else", r.query)
	}
}

func TestWithHTTPClient(t *testing.T) {
	srv := newEndpoint(t, nil, nil)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	e := newEngine(srv, WithHTTPClient(client))

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}

	if got != audioFor("Hello") {
		t.Errorf("audio = %q, want %q", got, audioFor("Hello"))
	}
	if calls.Load() != 1 {
		t.Errorf("the client was used %d times, want 1", calls.Load())
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
			t.Errorf("Synthesize(%q) error = %v, want engine.ErrEmptyText", text, err)
		}
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent for empty text", rec.count())
	}
}

func TestFormatDefault(t *testing.T) {
	if got := New(WithKey(testKey)).Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want %q", got, engine.FormatMP3)
	}
}

func TestFingerprint(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "defaults",
			want: "googlecloud-en-US-1-0",
		},
		{
			name: "voice",
			opts: []Option{WithVoice(VoiceHuHUWavenetB)},
			want: "googlecloud-hu-HU-Wavenet-B-1-0",
		},
		{
			name: "language and gender without a voice",
			opts: []Option{WithLanguage("de-DE"), WithGender(GenderMale)},
			want: "googlecloud-de-DE-male-1-0",
		},
		{
			name: "gender alone",
			opts: []Option{WithGender(GenderFemale)},
			want: "googlecloud-en-US-female-1-0",
		},
		{
			name: "voice decides over the language and the gender",
			opts: []Option{WithVoice(VoiceEnUSNeural2C), WithLanguage("de-DE"), WithGender(GenderMale)},
			want: "googlecloud-en-US-Neural2-C-1-0",
		},
		{
			name: "rate and pitch",
			opts: []Option{WithSpeakingRate(1.25), WithPitch(-2.5)},
			want: "googlecloud-en-US-1.25--2.5",
		},
		{
			name: "sample rate",
			opts: []Option{WithSampleRate(48000)},
			want: "googlecloud-en-US-1-0-48000",
		},
		{
			name: "volume gain",
			opts: []Option{WithVolumeGain(-6)},
			want: "googlecloud-en-US-1-0-g-6",
		},
		{
			name: "zero volume gain is the voice's own",
			opts: []Option{WithVolumeGain(0)},
			want: "googlecloud-en-US-1-0",
		},
		{
			name: "negative zero pitch is the voice's own",
			opts: []Option{WithPitch(math.Copysign(0, -1))},
			want: "googlecloud-en-US-1-0",
		},
		{
			name: "everything",
			opts: []Option{WithVoice(VoiceDeDEWavenetH), WithSpeakingRate(0.8), WithPitch(2), WithSampleRate(16000), WithVolumeGain(3.5)},
			want: "googlecloud-de-DE-Wavenet-H-0.8-2-16000-g3.5",
		},
		{
			name: "the format is the extension, not part of the name",
			opts: []Option{WithFormat(engine.FormatWAV)},
			want: "googlecloud-en-US-1-0",
		},
		{
			name: "names that do not fit a file name",
			opts: []Option{WithVoice("my voice/1:ő")},
			want: "googlecloud-my-voice-1---1-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(append([]Option{WithKey(testKey), WithEndpoint("https://eu-texttospeech.googleapis.com/v1/text:synthesize")}, tt.opts...)...)
			got := e.Fingerprint()
			if got != tt.want {
				t.Errorf("Fingerprint() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, testKey) || strings.Contains(got, "googleapis") {
				t.Errorf("Fingerprint() = %q carries the key or the endpoint", got)
			}
		})
	}
}

func TestLanguageOf(t *testing.T) {
	tests := map[Voice]string{
		"hu-HU-Wavenet-B":          "hu-HU",
		"en-US-Neural2-C":          "en-US",
		"en-GB-Chirp3-HD-Aoede":    "en-GB",
		"cmn-CN-Standard-A":        "cmn-CN",
		"ar-XA-Wavenet-A":          "ar-XA",
		"Kore":                     DefaultLanguage,
		"Wavenet-A":                DefaultLanguage,
		"English-US-Wavenet-A":     DefaultLanguage,
		"en-United States-Wavenet": DefaultLanguage,
	}
	for voice, want := range tests {
		if got := languageOf(voice); got != want {
			t.Errorf("languageOf(%q) = %q, want %q", voice, got, want)
		}
	}
}
