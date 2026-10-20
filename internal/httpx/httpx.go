// Package httpx holds the HTTP helpers that the engines share: a client with
// an optional proxy, and a response check that turns an unexpected answer
// into an *engine.HTTPError.
package httpx

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// maxMessageBytes bounds the part of a response body that goes into an
// engine.HTTPError.
const maxMessageBytes = 200

// NewClient returns an HTTP client with the given timeout, zero meaning no
// timeout, whose transport is a copy of http.DefaultTransport. When proxyURL
// is not nil every request goes through that proxy; otherwise the proxy comes
// from the environment (HTTP_PROXY, HTTPS_PROXY, NO_PROXY), as with the
// default transport. TLS verification stays on in both cases.
func NewClient(timeout time.Duration, proxyURL *url.URL) *http.Client {
	transport := defaultTransport()
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

// defaultTransport returns a copy of http.DefaultTransport, or a transport
// with the same settings when the default has been replaced by another type.
func defaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// CheckResponse returns nil when resp has status 200 OK and a Content-Type
// that starts with one of the given prefixes, compared case-insensitively; no
// prefix, or an empty one, accepts every Content-Type. Otherwise it reads the
// beginning of the body and returns an *engine.HTTPError that describes the
// answer. The body is left untouched when the response is accepted.
func CheckResponse(resp *http.Response, wantContentTypePrefixes ...string) error {
	if resp.StatusCode != http.StatusOK {
		return newHTTPError(resp, "")
	}
	contentType := resp.Header.Get("Content-Type")
	if len(wantContentTypePrefixes) == 0 || hasAnyPrefix(contentType, wantContentTypePrefixes) {
		return nil
	}
	return newHTTPError(resp, fmt.Sprintf("unexpected content type %q: ", contentType))
}

// hasAnyPrefix reports whether s starts with one of the prefixes, ignoring
// case; an empty prefix matches everything.
func hasAnyPrefix(s string, prefixes []string) bool {
	s = strings.ToLower(s)
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

func newHTTPError(resp *http.Response, messagePrefix string) *engine.HTTPError {
	return &engine.HTTPError{
		StatusCode: resp.StatusCode,
		URL:        requestURL(resp),
		Message:    messagePrefix + bodyPrefix(resp.Body),
	}
}

// requestURL returns the URL of the request that resp answers, without its
// query, user information and fragment.
func requestURL(resp *http.Response) string {
	if resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	u := *resp.Request.URL
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// bodyPrefix reads the beginning of body, collapses its white space and
// truncates it to maxMessageBytes without splitting a UTF-8 sequence, marking
// the cut with an ellipsis.
func bodyPrefix(body io.Reader) string {
	if body == nil {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(body, maxMessageBytes+1))
	truncated := len(raw) > maxMessageBytes
	s := strings.Join(strings.Fields(string(truncate(raw, maxMessageBytes))), " ")
	if truncated {
		s += "…"
	}
	return s
}

// truncate shortens b to at most n bytes, dropping an incomplete trailing
// UTF-8 sequence.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	b = b[:n]
	for len(b) > 0 {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}
