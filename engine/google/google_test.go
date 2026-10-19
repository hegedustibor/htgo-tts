package google

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

// recorder keeps what the fake endpoint received.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	path      string
	query     url.Values
	userAgent string
}

func (rec *recorder) add(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.reqs = append(rec.reqs, recorded{
		path:      r.URL.Path,
		query:     r.URL.Query(),
		userAgent: r.Header.Get("User-Agent"),
	})
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

// audioFor is the distinct audio that the fake endpoint answers for a chunk.
func audioFor(idx int) string { return "audio-" + strconv.Itoa(idx) + ";" }

func serveAudio(w http.ResponseWriter, r *http.Request) {
	idx, _ := strconv.Atoi(r.URL.Query().Get("idx"))
	w.Header().Set("Content-Type", "audio/mpeg")
	io.WriteString(w, audioFor(idx))
}

// newEndpoint starts a fake endpoint that records every request in rec and
// answers with handler, or with serveAudio when handler is nil.
func newEndpoint(t *testing.T, rec *recorder, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	if handler == nil {
		handler = serveAudio
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rec != nil {
			rec.add(r)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// threeSentences returns a text that Synthesize sends in three chunks of 60
// runes, together with the chunks.
func threeSentences() (string, []string) {
	s := strings.Repeat("ű", 59) + "."
	return s + " " + s + " " + s, []string{s, s, s}
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

func TestSynthesizeRequests(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	text, chunks := threeSentences()
	e := New(voices.Hungarian, WithEndpoint(srv.URL+"/translate_tts"))

	got, err := readAll(t, e, text)
	if err != nil {
		t.Fatal(err)
	}

	if want := audioFor(0) + audioFor(1) + audioFor(2); got != want {
		t.Errorf("audio = %q, want the chunks in order %q", got, want)
	}
	if rec.count() != len(chunks) {
		t.Fatalf("%d requests, want %d", rec.count(), len(chunks))
	}
	for i, chunk := range chunks {
		r := rec.at(i)
		if r.path != "/translate_tts" {
			t.Errorf("request %d path = %q, want /translate_tts", i, r.path)
		}
		want := url.Values{
			"ie":      {"UTF-8"},
			"client":  {"tw-ob"},
			"tl":      {"hu"},
			"q":       {chunk},
			"textlen": {"60"},
			"idx":     {strconv.Itoa(i)},
			"total":   {"3"},
		}
		for key, values := range want {
			if got := r.query[key]; len(got) != 1 || got[0] != values[0] {
				t.Errorf("request %d %s = %q, want %q", i, key, got, values[0])
			}
		}
		if r.query.Has("ttsspeed") {
			t.Errorf("request %d sends ttsspeed=%q without WithSlow", i, r.query.Get("ttsspeed"))
		}
		if !strings.HasPrefix(r.userAgent, "Mozilla/5.0") {
			t.Errorf("request %d User-Agent = %q, want a browser-like one", i, r.userAgent)
		}
	}
}

func TestSynthesizeSlow(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(voices.Hungarian, WithSlow(), WithEndpoint(srv.URL))

	if _, err := readAll(t, e, "Szia"); err != nil {
		t.Fatal(err)
	}

	if got := rec.at(0).query.Get("ttsspeed"); got != "0.24" {
		t.Errorf("ttsspeed = %q, want 0.24", got)
	}
	if got := e.Fingerprint(); got != "hu-slow" {
		t.Errorf("Fingerprint() = %q, want hu-slow", got)
	}
}

func TestSynthesizeStreamsBeforeTheNextChunk(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var secondServed atomic.Bool
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("idx") == "1" {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			secondServed.Store(true)
		}
		serveAudio(w, r)
	})
	text, _ := threeSentences()
	e := New(voices.Hungarian, WithEndpoint(srv.URL))

	rc, err := e.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	first := make([]byte, len(audioFor(0)))
	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(rc, first)
		readErr <- err
	}()
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("reading the first chunk: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first chunk is not readable while the second request is pending")
	}
	if string(first) != audioFor(0) {
		t.Errorf("first chunk = %q, want %q", first, audioFor(0))
	}
	if secondServed.Load() {
		t.Error("the second chunk was served before the first one was read")
	}

	unblock()
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if want := audioFor(1) + audioFor(2); string(rest) != want {
		t.Errorf("rest = %q, want %q", rest, want)
	}
}

func TestSynthesizeHTTPError(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantMessage string
	}{
		{
			name:        "server error",
			status:      http.StatusInternalServerError,
			contentType: "text/html",
			body:        "<html><body>Server Error</body></html>",
			wantMessage: "<html><body>Server Error</body></html>",
		},
		{
			name:        "html with status 200",
			status:      http.StatusOK,
			contentType: "text/html; charset=utf-8",
			body:        "<!DOCTYPE html><title>Sorry</title>",
			wantMessage: `unexpected content type "text/html; charset=utf-8": <!DOCTYPE html><title>Sorry</title>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			})
			e := New(voices.Hungarian, WithEndpoint(srv.URL+"/translate_tts"))

			got, err := readAll(t, e, "Szia")

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
			if want := srv.URL + "/translate_tts"; httpErr.URL != want {
				t.Errorf("URL = %q, want %q", httpErr.URL, want)
			}
			if httpErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", httpErr.Message, tt.wantMessage)
			}
			if !strings.Contains(err.Error(), "chunk 1 of 1") {
				t.Errorf("error %q does not name the chunk", err)
			}
		})
	}
}

func TestSynthesizeContextCancelled(t *testing.T) {
	srv := newEndpoint(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("idx") == "1" {
			<-r.Context().Done()
			return
		}
		serveAudio(w, r)
	})
	text, _ := threeSentences()
	e := New(voices.Hungarian, WithEndpoint(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rc, err := e.Synthesize(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len(audioFor(0)))
	if _, err := io.ReadFull(rc, first); err != nil {
		t.Fatalf("reading the first chunk: %v", err)
	}

	cancel()
	rest, err := io.ReadAll(rc)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error is %v, want one that wraps context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "chunk 2 of 3") {
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
	e := New(voices.Hungarian, WithEndpoint(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rc, err := e.Synthesize(ctx, "Szia")

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
	e := New(voices.Hungarian, WithEndpoint(srv.URL), WithTimeout(50*time.Millisecond))

	_, err := readAll(t, e, "Szia")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error is %v, want one that wraps context.DeadlineExceeded", err)
	}
}

func TestCloseStopsFurtherRequests(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	text, _ := threeSentences()
	e := New(voices.Hungarian, WithEndpoint(srv.URL))

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

func TestWithProxy(t *testing.T) {
	var gotHost, gotAuth string
	var gotAbsolute bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy receives the absolute URL of the target.
		gotHost, gotAbsolute, gotAuth = r.URL.Host, r.URL.IsAbs(), r.Header.Get("Proxy-Authorization")
		serveAudio(w, r)
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL.User = url.UserPassword("user", "pass")
	e := New(voices.English, WithProxy(proxyURL.String()), WithEndpoint("http://tts.example.invalid/translate_tts"))

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}

	if got != audioFor(0) {
		t.Errorf("audio = %q, want %q", got, audioFor(0))
	}
	if !gotAbsolute || gotHost != "tts.example.invalid" {
		t.Errorf("the proxy received host %q (absolute: %v), want tts.example.invalid", gotHost, gotAbsolute)
	}
	if gotAuth != "Basic dXNlcjpwYXNz" {
		t.Errorf("Proxy-Authorization = %q, want the basic credentials of the proxy URL", gotAuth)
	}
}

func TestWithHTTPClient(t *testing.T) {
	srv := newEndpoint(t, nil, nil)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	e := New(voices.English, WithHTTPClient(client), WithEndpoint(srv.URL))

	got, err := readAll(t, e, "Hello")
	if err != nil {
		t.Fatal(err)
	}

	if got != audioFor(0) {
		t.Errorf("audio = %q, want %q", got, audioFor(0))
	}
	if calls.Load() != 1 {
		t.Errorf("the client was used %d times, want 1", calls.Load())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWithEndpointKeepsItsQuery(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(voices.English, WithEndpoint(srv.URL+"/tts?key=abc"))

	if _, err := readAll(t, e, "Hello"); err != nil {
		t.Fatal(err)
	}

	r := rec.at(0)
	if r.path != "/tts" {
		t.Errorf("path = %q, want /tts", r.path)
	}
	if r.query.Get("key") != "abc" || r.query.Get("tl") != "en" || r.query.Get("q") != "Hello" {
		t.Errorf("query = %v, want key=abc together with the engine's parameters", r.query)
	}
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name        string
		lang        voices.Language
		opts        []Option
		wantContain string
		wantMissing string
	}{
		{
			name:        "malformed proxy URL",
			lang:        voices.English,
			opts:        []Option{WithProxy("://bad")},
			wantContain: "invalid proxy URL",
		},
		{
			name:        "proxy without host",
			lang:        voices.English,
			opts:        []Option{WithProxy("http://")},
			wantContain: "missing host",
		},
		{
			name:        "proxy with unsupported scheme",
			lang:        voices.English,
			opts:        []Option{WithProxy("ftp://proxy.example.com:21")},
			wantContain: `unsupported scheme "ftp"`,
		},
		{
			name:        "proxy error hides the password",
			lang:        voices.English,
			opts:        []Option{WithProxy("http://user:secret@bad host:3128")},
			wantContain: "invalid proxy URL",
			wantMissing: "secret",
		},
		{
			name:        "proxy with a custom client",
			lang:        voices.English,
			opts:        []Option{WithProxy("http://proxy.example.com:3128"), WithHTTPClient(http.DefaultClient)},
			wantContain: "WithHTTPClient",
		},
		{
			name:        "malformed endpoint URL",
			lang:        voices.English,
			opts:        []Option{WithEndpoint("not a url")},
			wantContain: "invalid endpoint URL",
		},
		{
			name:        "nil client",
			lang:        voices.English,
			opts:        []Option{WithHTTPClient(nil)},
			wantContain: "nil HTTP client",
		},
		{
			name:        "negative timeout",
			lang:        voices.English,
			opts:        []Option{WithTimeout(-time.Second)},
			wantContain: "negative timeout",
		},
		{
			name:        "empty language",
			lang:        "",
			wantContain: "empty language",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.lang, tt.opts...)

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

func TestSynthesizeEmptyText(t *testing.T) {
	rec := &recorder{}
	srv := newEndpoint(t, rec, nil)
	e := New(voices.Hungarian, WithEndpoint(srv.URL))

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

func TestFormatAndFingerprint(t *testing.T) {
	if got := New(voices.Hungarian).Format(); got != engine.FormatMP3 {
		t.Errorf("Format() = %q, want %q", got, engine.FormatMP3)
	}
	tests := []struct {
		lang voices.Language
		opts []Option
		want string
	}{
		{voices.Hungarian, nil, "hu"},
		{voices.Hungarian, []Option{WithSlow()}, "hu-slow"},
		{voices.EnglishUK, nil, "en-GB"},
	}
	for _, tt := range tests {
		if got := New(tt.lang, tt.opts...).Fingerprint(); got != tt.want {
			t.Errorf("Fingerprint() = %q, want %q", got, tt.want)
		}
	}
}
