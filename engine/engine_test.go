package engine

import (
	"errors"
	"testing"
)

func TestFormatExt(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{FormatMP3, ".mp3"},
		{FormatWAV, ".wav"},
		{FormatOpus, ".opus"},
		{FormatAAC, ".aac"},
		{FormatFLAC, ".flac"},
		{FormatPCM, ".pcm"},
	}
	for _, tt := range tests {
		if got := tt.format.Ext(); got != tt.want {
			t.Errorf("Format(%q).Ext() = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestHTTPErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  HTTPError
		want string
	}{
		{
			name: "status and message",
			err:  HTTPError{StatusCode: 500, URL: "https://example.com/tts", Message: "<html>boom</html>"},
			want: "https://example.com/tts: HTTP 500 Internal Server Error: <html>boom</html>",
		},
		{
			name: "without message",
			err:  HTTPError{StatusCode: 404, URL: "https://example.com/tts"},
			want: "https://example.com/tts: HTTP 404 Not Found",
		},
		{
			name: "without url",
			err:  HTTPError{StatusCode: 200, Message: `unexpected content type "text/html": <html>`},
			want: `HTTP 200 OK: unexpected content type "text/html": <html>`,
		},
		{
			name: "unknown status",
			err:  HTTPError{StatusCode: 799, URL: "https://example.com/tts"},
			want: "https://example.com/tts: HTTP 799",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPErrorAs(t *testing.T) {
	var httpErr *HTTPError
	err := errors.Join(errors.New("outer"), &HTTPError{StatusCode: 503})
	if !errors.As(err, &httpErr) {
		t.Fatal("errors.As did not find the *HTTPError in the wrapped error")
	}
	if httpErr.StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", httpErr.StatusCode)
	}
}

func TestErrEmptyText(t *testing.T) {
	if got, want := ErrEmptyText.Error(), "htgotts: empty text"; got != want {
		t.Errorf("ErrEmptyText = %q, want %q", got, want)
	}
}
