package openai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// testKey is the API key of the tests; it must never show up in an error.
const testKey = "sk-test-0123456789abcdef"

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

// contentTypes is the Content-Type the fake endpoint answers for each
// response_format.
var contentTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"opus": "audio/ogg",
	"aac":  "audio/aac",
	"flac": "audio/flac",
	"wav":  "audio/wav",
	"pcm":  "audio/pcm",
}

// audioFor is the distinct audio that the fake endpoint answers for an input:
// its length in runes, so that the pieces of a long text can be told apart.
func audioFor(input string) string {
	return "audio[" + strconv.Itoa(utf8.RuneCountInString(input)) + "];"
}

// serveAudio answers a speech request with audioFor of its input, in the
// Content-Type of its response_format.
func serveAudio(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input          string `json:"input"`
		ResponseFormat string `json:"response_format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	contentType, ok := contentTypes[req.ResponseFormat]
	if !ok {
		http.Error(w, "unknown response_format "+req.ResponseFormat, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", contentType)
	io.WriteString(w, audioFor(req.Input))
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

// newEngine returns an engine that talks to the fake endpoint at srv under
// the base URL <srv>/v1 with testKey, plus the given options.
func newEngine(srv *httptest.Server, opts ...Option) *Engine {
	return New(append([]Option{WithAPIKey(testKey), WithBaseURL(srv.URL + "/v1")}, opts...)...)
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
		t.Errorf("method = %q, want POST", r.method)
	}
	if r.path != "/v1/audio/speech" {
		t.Errorf("path = %q, want /v1/audio/speech", r.path)
	}
	if r.query != "" {
		t.Errorf("query = %q, want none", r.query)
	}
	if got := r.header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+testKey)
	}
	if got := r.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	want := map[string]any{
		"model":           "tts-1",
		"input":           "Hello, world!",
		"voice":           "alloy",
		"response_format": "mp3",
	}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("body = %v, want %v (no speed, no instructions)", r.body, want)
	}
}

func TestSynthesizeRequestWithOptions(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv,
		WithModel(ModelGPT4oMiniTTS),
		WithVoice(VoiceNova),
		WithSpeed(1.5),
		WithInstructions("Speak slowly."),
		WithFormat(engine.FormatWAV),
	)

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"model":           "gpt-4o-mini-tts",
		"input":           "Hello",
		"voice":           "nova",
		"response_format": "wav",
		"speed":           1.5,
		"instructions":    "Speak slowly.",
	}
	if got := rec.at(0).body; !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

func TestSynthesizeOmitsTheDefaultSpeed(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := newEngine(srv, WithSpeed(1.0))

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	if body := rec.at(0).body; body["speed"] != nil {
		t.Errorf("body sends speed=%v for the default speed, want none", body["speed"])
	}
}

func TestFormats(t *testing.T) {
	formats := []engine.Format{
		engine.FormatMP3, engine.FormatOpus, engine.FormatAAC, engine.FormatFLAC, engine.FormatWAV, engine.FormatPCM,
	}
	for _, format := range formats {
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
			if got := rec.at(0).body["response_format"]; got != string(format) {
				t.Errorf("response_format = %v, want %q", got, format)
			}
		})
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
		got, _ := rec.at(i).body["input"].(string)
		if got != chunk {
			t.Errorf("request %d input = %.20q (%d runes), want piece %d (%d runes)", i, got, utf8.RuneCountInString(got), i, utf8.RuneCountInString(chunk))
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
			name:        "invalid API key",
			status:      http.StatusUnauthorized,
			contentType: "application/json",
			body:        `{"error": {"message": "Incorrect API key provided.", "type": "invalid_request_error", "param": null, "code": "invalid_api_key"}}`,
			wantMessage: "Incorrect API key provided.",
		},
		{
			name:        "rate limit with white space in the message",
			status:      http.StatusTooManyRequests,
			contentType: "application/json; charset=utf-8",
			body:        "{\"error\": {\"message\": \"Rate limit reached.\\n  Please try again\\tlater.\", \"type\": \"requests\"}}",
			wantMessage: "Rate limit reached. Please try again later.",
		},
		{
			name:        "server error without JSON",
			status:      http.StatusInternalServerError,
			contentType: "text/html",
			body:        "<html><body>Internal Server Error</body></html>",
			wantMessage: "<html><body>Internal Server Error</body></html>",
		},
		{
			name:        "JSON without an error message",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"detail": "input is required"}`,
			wantMessage: `{"detail": "input is required"}`,
		},
		{
			name:        "error that is a string",
			status:      http.StatusServiceUnavailable,
			contentType: "application/json",
			body:        `{"error": "overloaded"}`,
			wantMessage: `{"error": "overloaded"}`,
		},
		{
			name:        "empty body",
			status:      http.StatusBadGateway,
			contentType: "text/plain",
			body:        "",
			wantMessage: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
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
				t.Fatalf("error is %T %v, want *engine.HTTPError", err, err)
			}
			if httpErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, tt.status)
			}
			if want := srv.URL + "/v1/audio/speech"; httpErr.URL != want {
				t.Errorf("URL = %q, want %q", httpErr.URL, want)
			}
			if httpErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", httpErr.Message, tt.wantMessage)
			}
			if !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error %q leaks the API key", err)
			}
		})
	}
}

func TestSynthesizeLongErrorMessageIsBounded(t *testing.T) {
	long := strings.Repeat("ű", maxMessageBytes)
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": long}})
	})
	e := newEngine(srv)

	_, err := readAll(t, e, "Hello")

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is %T %v, want *engine.HTTPError", err, err)
	}
	if !utf8.ValidString(httpErr.Message) {
		t.Errorf("Message %q is not valid UTF-8", httpErr.Message)
	}
	if !strings.HasSuffix(httpErr.Message, "…") || len(httpErr.Message) > maxMessageBytes+len("…") {
		t.Errorf("Message is %d bytes and ends with %q, want at most %d bytes ending with an ellipsis", len(httpErr.Message), httpErr.Message[len(httpErr.Message)-3:], maxMessageBytes)
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
		t.Fatalf("error is %T %v, want *engine.HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", httpErr.StatusCode)
	}
	if want := `unexpected content type "text/html; charset=utf-8": <!DOCTYPE html><title>Sign in</title>`; httpErr.Message != want {
		t.Errorf("Message = %q, want %q", httpErr.Message, want)
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
			env:  "sk-env",
			want: "Bearer sk-env",
		},
		{
			name: "the option wins over the environment",
			env:  "sk-env",
			opts: []Option{WithAPIKey("sk-option")},
			want: "Bearer sk-option",
		},
		{
			name: "an empty option means the environment",
			env:  "sk-env",
			opts: []Option{WithAPIKey("")},
			want: "Bearer sk-env",
		},
		{
			name: "white space around the key is dropped",
			env:  "",
			opts: []Option{WithAPIKey("  sk-option\n")},
			want: "Bearer sk-option",
		},
		{
			name: "white space around the environment variable is dropped",
			env:  " sk-env\n",
			want: "Bearer sk-env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvAPIKey, tt.env)
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := New(append([]Option{WithBaseURL(srv.URL)}, tt.opts...)...)

			if _, err := readAll(t, e, "Hello"); err != nil {
				t.Fatal(err)
			}

			if got := rec.at(0).header.Get("Authorization"); got != tt.want {
				t.Errorf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(WithBaseURL(srv.URL))

	rc, err := e.Synthesize(context.Background(), "Hello")

	if rc != nil {
		rc.Close()
		t.Fatal("Synthesize returned a reader without an API key")
	}
	if err == nil || !strings.Contains(err.Error(), "missing API key") || !strings.Contains(err.Error(), EnvAPIKey) {
		t.Errorf("error = %v, want one that names the missing API key and %s", err, EnvAPIKey)
	}
	if rec.count() != 0 {
		t.Errorf("%d requests were sent without an API key", rec.count())
	}
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name        string
		opts        []Option
		wantContain string
		wantMissing string
	}{
		{
			name:        "speed above the maximum",
			opts:        []Option{WithSpeed(5.0)},
			wantContain: "speed 5 is not between 0.25 and 4",
		},
		{
			name:        "speed below the minimum",
			opts:        []Option{WithSpeed(0.1)},
			wantContain: "speed 0.1 is not between 0.25 and 4",
		},
		{
			name:        "speed that is not a number",
			opts:        []Option{WithSpeed(math.NaN())},
			wantContain: "speed NaN is not between",
		},
		{
			name:        "malformed base URL",
			opts:        []Option{WithBaseURL("://bad")},
			wantContain: "invalid base URL",
		},
		{
			name:        "base URL without host",
			opts:        []Option{WithBaseURL("http://")},
			wantContain: "missing host",
		},
		{
			name:        "base URL without scheme",
			opts:        []Option{WithBaseURL("api.example.com/v1")},
			wantContain: "missing host",
		},
		{
			name:        "base URL with unsupported scheme",
			opts:        []Option{WithBaseURL("ftp://api.example.com/v1")},
			wantContain: `unsupported scheme "ftp"`,
		},
		{
			name:        "base URL error hides the password",
			opts:        []Option{WithBaseURL("http://user:secret@bad host/v1")},
			wantContain: "invalid base URL",
			wantMissing: "secret",
		},
		{
			name:        "unsupported format",
			opts:        []Option{WithFormat("ogg")},
			wantContain: `unsupported format "ogg"`,
		},
		{
			name:        "empty format",
			opts:        []Option{WithFormat("")},
			wantContain: `unsupported format ""`,
		},
		{
			name:        "empty model",
			opts:        []Option{WithModel("")},
			wantContain: "empty model",
		},
		{
			name:        "empty voice",
			opts:        []Option{WithVoice("")},
			wantContain: "empty voice",
		},
		{
			name:        "nil client",
			opts:        []Option{WithHTTPClient(nil)},
			wantContain: "nil HTTP client",
		},
		{
			name:        "negative timeout",
			opts:        []Option{WithTimeout(-time.Second)},
			wantContain: "negative timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(append([]Option{WithAPIKey(testKey)}, tt.opts...)...)

			rc, err := e.Synthesize(context.Background(), "Hello")

			if rc != nil {
				rc.Close()
				t.Fatal("Synthesize returned a reader for an invalid engine")
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantContain)
			}
			if tt.wantMissing != "" && err != nil && strings.Contains(err.Error(), tt.wantMissing) {
				t.Errorf("error %q leaks %q", err, tt.wantMissing)
			}
		})
	}
}

func TestWithBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		base      string // appended to the URL of the fake endpoint
		wantPath  string
		wantQuery string
	}{
		{name: "bare host", base: "", wantPath: "/audio/speech"},
		{name: "with path", base: "/v1", wantPath: "/v1/audio/speech"},
		{name: "with trailing slash", base: "/v1/", wantPath: "/v1/audio/speech"},
		{
			name:      "with query, like an Azure OpenAI deployment",
			base:      "/openai/deployments/tts?api-version=2025-03-01-preview",
			wantPath:  "/openai/deployments/tts/audio/speech",
			wantQuery: "api-version=2025-03-01-preview",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			srv := newEndpoint(t, rec, nil)
			e := New(WithAPIKey(testKey), WithBaseURL(srv.URL+tt.base))

			if _, err := readAll(t, e, "Hello"); err != nil {
				t.Fatal(err)
			}

			r := rec.at(0)
			if r.path != tt.wantPath {
				t.Errorf("path = %q, want %q", r.path, tt.wantPath)
			}
			if r.query != tt.wantQuery {
				t.Errorf("query = %q, want %q", r.query, tt.wantQuery)
			}
		})
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
	if got := New(WithAPIKey(testKey)).Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want %q", got, engine.FormatMP3)
	}
}

func TestFingerprint(t *testing.T) {
	instructions := "Speak in a calm, friendly tone."
	sum := sha256.Sum256([]byte(instructions))
	hash := hex.EncodeToString(sum[:4])

	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "defaults",
			want: "openai-tts-1-alloy-1",
		},
		{
			name: "model, voice and speed",
			opts: []Option{WithModel(ModelGPT4oMiniTTS), WithVoice(VoiceNova), WithSpeed(1.25)},
			want: "openai-gpt-4o-mini-tts-nova-1.25",
		},
		{
			name: "slow speed",
			opts: []Option{WithSpeed(0.5)},
			want: "openai-tts-1-alloy-0.5",
		},
		{
			name: "with instructions",
			opts: []Option{WithVoice(VoiceCoral), WithInstructions(instructions)},
			want: "openai-tts-1-coral-1-" + hash,
		},
		{
			name: "names that do not fit a file name",
			opts: []Option{WithModel("my model/v1.0"), WithVoice("voice_1234:ő")},
			want: "openai-my-model-v1.0-voice_1234---1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(append([]Option{WithAPIKey(testKey)}, tt.opts...)...).Fingerprint(); got != tt.want {
				t.Errorf("Fingerprint() = %q, want %q", got, tt.want)
			}
		})
	}

	withInstructions := New(WithAPIKey(testKey), WithInstructions(instructions)).Fingerprint()
	if strings.Contains(withInstructions, "calm") {
		t.Errorf("Fingerprint() = %q contains the instructions", withInstructions)
	}
	if strings.Contains(withInstructions, testKey) {
		t.Errorf("Fingerprint() = %q contains the API key", withInstructions)
	}
	other := New(WithAPIKey(testKey), WithInstructions("Shout.")).Fingerprint()
	if other == withInstructions {
		t.Errorf("Fingerprint() = %q for different instructions, want a different one", other)
	}
}
