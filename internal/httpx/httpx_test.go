package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

func TestNewClientWithProxy(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy.example.com:3128")
	if err != nil {
		t.Fatal(err)
	}
	defaultProxy := reflect.ValueOf(http.DefaultTransport.(*http.Transport).Proxy).Pointer()

	client := NewClient(5*time.Second, proxyURL)

	if client.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", client.Transport)
	}
	req := httptest.NewRequest(http.MethodGet, "https://translate.google.com/translate_tts", nil)
	got, err := transport.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.String() != proxyURL.String() {
		t.Errorf("Proxy(req) = %v, want %v", got, proxyURL)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("TLS verification is disabled, want it on")
	}
	if transport == http.DefaultTransport {
		t.Error("NewClient returned http.DefaultTransport itself instead of a copy")
	}
	if reflect.ValueOf(http.DefaultTransport.(*http.Transport).Proxy).Pointer() != defaultProxy {
		t.Error("NewClient changed the proxy of http.DefaultTransport")
	}
}

func TestNewClientWithoutProxy(t *testing.T) {
	client := NewClient(0, nil)

	if client.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0", client.Timeout)
	}
	transport := client.Transport.(*http.Transport)
	got := reflect.ValueOf(transport.Proxy).Pointer()
	want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
	if got != want {
		t.Error("Proxy is not http.ProxyFromEnvironment")
	}
}

func TestNewClientSendsRequestsThroughTheProxy(t *testing.T) {
	var gotHost, gotPath string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy receives the absolute URL of the target.
		gotHost, gotPath = r.URL.Host, r.URL.Path
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := NewClient(5*time.Second, proxyURL)
	resp, err := client.Get("http://tts.example.invalid/translate_tts?q=hi")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "via proxy" {
		t.Errorf("body = %q, want %q", body, "via proxy")
	}
	if gotHost != "tts.example.invalid" || gotPath != "/translate_tts" {
		t.Errorf("the proxy received host %q path %q, want tts.example.invalid /translate_tts", gotHost, gotPath)
	}
}

func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantPrefix  string
		wantErr     bool
		wantMessage string
	}{
		{
			name:        "ok",
			status:      200,
			contentType: "audio/mpeg",
			body:        "ID3...",
			wantPrefix:  "audio/mpeg",
		},
		{
			name:        "content type with parameters",
			status:      200,
			contentType: "Audio/MPEG; charset=binary",
			body:        "ID3...",
			wantPrefix:  "audio/mpeg",
		},
		{
			name:        "any content type when none is required",
			status:      200,
			contentType: "text/html",
			body:        "<html>",
			wantPrefix:  "",
		},
		{
			name:        "server error",
			status:      500,
			contentType: "text/html",
			body:        "<html>\n  <body>Internal\tServer   Error</body>\n</html>",
			wantPrefix:  "audio/mpeg",
			wantErr:     true,
			wantMessage: "<html> <body>Internal Server Error</body> </html>",
		},
		{
			name:        "wrong content type",
			status:      200,
			contentType: "text/html; charset=utf-8",
			body:        "<!DOCTYPE html><title>Error</title>",
			wantPrefix:  "audio/mpeg",
			wantErr:     true,
			wantMessage: `unexpected content type "text/html; charset=utf-8": <!DOCTYPE html><title>Error</title>`,
		},
		{
			name:        "missing content type",
			status:      200,
			contentType: "",
			body:        "",
			wantPrefix:  "audio/mpeg",
			wantErr:     true,
			wantMessage: `unexpected content type "": `,
		},
		{
			name:        "long body is truncated at a rune boundary",
			status:      503,
			contentType: "text/plain",
			body:        strings.Repeat("ű", 150),
			wantPrefix:  "audio/mpeg",
			wantErr:     true,
			wantMessage: strings.Repeat("ű", 100) + "…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://user:secret@example.com/tts?q=hello&tl=hu#frag", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp := &http.Response{
				StatusCode: tt.status,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    req,
			}
			if tt.contentType != "" {
				resp.Header.Set("Content-Type", tt.contentType)
			}

			err = CheckResponse(resp, tt.wantPrefix)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckResponse returned %v, want nil", err)
				}
				rest, _ := io.ReadAll(resp.Body)
				if string(rest) != tt.body {
					t.Errorf("CheckResponse consumed the body: %q left of %q", rest, tt.body)
				}
				return
			}
			var httpErr *engine.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("CheckResponse returned %T %v, want *engine.HTTPError", err, err)
			}
			if httpErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, tt.status)
			}
			if httpErr.URL != "https://example.com/tts" {
				t.Errorf("URL = %q, want %q", httpErr.URL, "https://example.com/tts")
			}
			if httpErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", httpErr.Message, tt.wantMessage)
			}
			if len(httpErr.Message) > maxMessageBytes+len("…")+len(`unexpected content type "": `)+len(tt.contentType) {
				t.Errorf("Message is %d bytes long", len(httpErr.Message))
			}
		})
	}
}

func TestCheckResponseFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, "slow down")
	}))
	defer srv.Close()

	resp, err := NewClient(5*time.Second, nil).Get(srv.URL + "/tts?q=hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	err = CheckResponse(resp, "audio/mpeg")

	var httpErr *engine.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("CheckResponse returned %T %v, want *engine.HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", httpErr.StatusCode)
	}
	if httpErr.URL != srv.URL+"/tts" {
		t.Errorf("URL = %q, want %q", httpErr.URL, srv.URL+"/tts")
	}
	if httpErr.Message != "slow down" {
		t.Errorf("Message = %q, want %q", httpErr.Message, "slow down")
	}
	if want := srv.URL + "/tts: HTTP 429 Too Many Requests: slow down"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
