// Package google implements an engine.Engine on top of the unofficial Google
// Translate text-to-speech endpoint, the service that the v1 releases of
// htgotts used: free, without an API key, one language per engine, MP3
// output.
//
// The endpoint speaks about a hundred characters per request, so longer text
// is split at sentence and clause boundaries and fetched in several requests,
// one after the other, while the audio already streams to the caller. The
// endpoint is undocumented, so it may change or rate-limit without notice.
package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
	"github.com/hegedustibor/htgo-tts/v2/internal/chunk"
	"github.com/hegedustibor/htgo-tts/v2/internal/httpx"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

const (
	// DefaultEndpoint is the URL of the Google Translate text-to-speech
	// endpoint.
	DefaultEndpoint = "https://translate.google.com/translate_tts"

	// DefaultTimeout bounds each request to the endpoint when the engine
	// uses its own HTTP client.
	DefaultTimeout = 30 * time.Second

	// maxChunkRunes is the longest text the endpoint speaks in one request.
	maxChunkRunes = 100

	// slowSpeed is the ttsspeed value of the endpoint's slow mode.
	slowSpeed = "0.24"

	// userAgent mimics a desktop browser, because the endpoint serves
	// browsers.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// Engine speaks text in one language through the Google Translate endpoint.
// Create one with New. An Engine is safe for concurrent use.
type Engine struct {
	lang       voices.Language
	slow       bool
	endpoint   *url.URL
	client     *http.Client
	proxy      *url.URL
	timeout    time.Duration
	timeoutSet bool
	err        error // the first invalid argument, returned by every Synthesize
}

// Option configures an Engine; see the With functions.
type Option func(*Engine)

// New returns an engine that speaks lang. Options are applied in order.
//
// An invalid argument, such as an empty language or a malformed proxy URL,
// does not make New fail: the engine remembers the error and every call to
// Synthesize returns it, so that a configuration mistake surfaces at the
// first use instead of being ignored.
func New(lang voices.Language, opts ...Option) *Engine {
	e := &Engine{lang: lang, timeout: DefaultTimeout}
	e.endpoint, _ = url.Parse(DefaultEndpoint)
	for _, opt := range opts {
		opt(e)
	}
	if lang == "" {
		e.fail(errors.New("htgotts/google: empty language"))
	}
	if e.client == nil {
		// The timeout is applied per request in fetch, where it also covers
		// a client given with WithHTTPClient, so the client itself has none.
		e.client = httpx.NewClient(0, e.proxy)
		return e
	}
	if e.proxy != nil {
		e.fail(errors.New("htgotts/google: WithProxy cannot be combined with WithHTTPClient; configure the proxy on the client instead"))
	}
	if !e.timeoutSet {
		e.timeout = 0
	}
	return e
}

// WithSlow makes the engine speak slowly, in the endpoint's ttsspeed=0.24
// mode.
func WithSlow() Option {
	return func(e *Engine) { e.slow = true }
}

// WithProxy sends every request through the HTTP, HTTPS or SOCKS5 proxy at
// rawURL, for example "http://user:password@proxy.example.com:3128". TLS
// verification stays on; to turn it off, or to tune the proxy further, give
// the engine a client of your own with WithHTTPClient instead. An empty
// rawURL means no proxy, apart from the one the environment may set.
// WithProxy cannot be combined with WithHTTPClient.
func WithProxy(rawURL string) Option {
	return func(e *Engine) {
		if rawURL == "" {
			e.proxy = nil
			return
		}
		u, err := parseURL("proxy", rawURL, "http", "https", "socks5", "socks5h")
		if err != nil {
			e.fail(err)
			return
		}
		e.proxy = u
	}
}

// WithHTTPClient makes the engine send its requests with client instead of
// its own one, for example to disable TLS verification, to use a proxy with
// settings of its own, or to record the requests in a test. The engine's
// default timeout does not apply to such a client; an explicit WithTimeout
// does.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		if client == nil {
			e.fail(errors.New("htgotts/google: nil HTTP client"))
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
			e.fail(fmt.Errorf("htgotts/google: negative timeout %v", d))
			return
		}
		e.timeout = d
		e.timeoutSet = true
	}
}

// WithEndpoint sends the requests to rawURL instead of DefaultEndpoint, for
// example to a test server or to a mirror of the service. Query parameters
// in rawURL are kept, and the engine adds its own to them.
func WithEndpoint(rawURL string) Option {
	return func(e *Engine) {
		u, err := parseURL("endpoint", rawURL, "http", "https")
		if err != nil {
			e.fail(err)
			return
		}
		e.endpoint = u
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
		return nil, fmt.Errorf("htgotts/google: invalid %s URL: %w", what, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("htgotts/google: invalid %s URL %q: missing host", what, u.Redacted())
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("htgotts/google: invalid %s URL %q: unsupported scheme %q", what, u.Redacted(), u.Scheme)
}

func (e *Engine) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Format reports engine.FormatMP3: the endpoint always answers with MP3.
func (e *Engine) Format() engine.Format { return engine.FormatMP3 }

// Fingerprint returns the language code, for example "hu", or "hu-slow" when
// WithSlow is set. Audio files cached by htgotts v1, which are named
// "<language>_<md5>.mp3", therefore stay valid.
func (e *Engine) Fingerprint() string {
	if e.slow {
		return string(e.lang) + "-slow"
	}
	return string(e.lang)
}

// Synthesize fetches the speech for text and returns it as an MP3 stream.
//
// Text longer than the endpoint speaks in one request is split at sentence
// and clause boundaries, and the pieces are fetched one after the other in
// the background while the caller reads: the first bytes arrive once the
// first piece has been fetched, and the MP3 frames of the pieces simply
// follow each other. A failed request ends the stream with its error,
// wrapped with the index of the piece; an unexpected answer is an
// *engine.HTTPError. The stream stops when ctx is done, with an error that
// wraps ctx.Err().
//
// Close on the returned reader cancels the request in flight, prevents any
// further one and returns once the background work has stopped. Call it even
// after reading the stream to its end.
func (e *Engine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if e.err != nil {
		return nil, e.err
	}
	chunks := chunk.Split(text, maxChunkRunes)
	if len(chunks) == 0 {
		return nil, engine.ErrEmptyText
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("htgotts/google: %w", err)
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

// download fetches the chunks in order and writes their audio to w.
func (e *Engine) download(ctx context.Context, chunks []string, w io.Writer) error {
	for i, text := range chunks {
		if err := e.fetch(ctx, text, i, len(chunks), w); err != nil {
			return fmt.Errorf("htgotts/google: chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// fetch requests the audio of one chunk and copies it to w.
func (e *Engine) fetch(ctx context.Context, text string, idx, total int, w io.Writer) error {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.requestURL(text, idx, total), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := e.client.Do(req)
	if err != nil {
		return orContextError(ctx, err)
	}
	defer resp.Body.Close()
	if err := httpx.CheckResponse(resp, "audio/mpeg"); err != nil {
		return err
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return orContextError(ctx, err)
	}
	return nil
}

// requestURL builds the URL of the request for one chunk: the endpoint with
// the parameters the endpoint expects from its browser client.
func (e *Engine) requestURL(text string, idx, total int) string {
	u := *e.endpoint
	q := u.Query()
	q.Set("ie", "UTF-8")
	q.Set("client", "tw-ob")
	q.Set("tl", string(e.lang))
	q.Set("q", text)
	q.Set("textlen", strconv.Itoa(utf8.RuneCountInString(text)))
	q.Set("idx", strconv.Itoa(idx))
	q.Set("total", strconv.Itoa(total))
	if e.slow {
		q.Set("ttsspeed", slowSpeed)
	}
	u.RawQuery = q.Encode()
	return u.String()
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
