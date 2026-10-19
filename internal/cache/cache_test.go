package cache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

const (
	text = "Test"

	// textMD5 is the MD5 of text in lowercase hexadecimal.
	textMD5 = "0cbc6611f5540bd0809a388dc95a615b"

	// v1Name is the file that htgotts v1 wrote for the English text: the
	// language code, an underscore and the MD5 of the text.
	v1Name = "en_" + textMD5 + ".mp3"
)

var errBoom = errors.New("boom")

// source is a fake engine reader: it serves data and then ends with io.EOF,
// or with err when that is set, and remembers whether it was closed.
type source struct {
	r      *strings.Reader
	err    error
	closed bool
}

func (s *source) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err == io.EOF && s.err != nil {
		return n, s.err
	}
	return n, err
}

func (s *source) Close() error {
	s.closed = true
	return nil
}

// fetcher stands in for an engine: fetch counts its calls and returns a
// fresh source each time, or err when that is set.
type fetcher struct {
	data    string
	err     error // returned by fetch itself
	readErr error // returned by the source after data
	calls   int
	last    *source
}

func (f *fetcher) fetch(context.Context) (io.ReadCloser, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.last = &source{r: strings.NewReader(f.data), err: f.readErr}
	return f.last, nil
}

// entries returns the names of the entries in dir, sorted.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

// wantEntries fails the test unless dir holds exactly the named entries, in
// any order.
func wantEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	want = slices.Clone(want)
	slices.Sort(want)
	if got := entries(t, dir); !slices.Equal(got, want) {
		t.Errorf("%s holds %q, want %q", dir, got, want)
	}
}

// tempFiles returns the ".tmp-*" entries of dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	var tmp []string
	for _, name := range entries(t, dir) {
		if strings.HasPrefix(name, ".tmp-") {
			tmp = append(tmp, name)
		}
	}
	return tmp
}

// readFile returns the content of path.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(b)
}

// open calls s.Open with the default fingerprint and format and fails the
// test when it errors.
func open(t *testing.T, s *Store, fetch func(context.Context) (io.ReadCloser, error)) io.ReadCloser {
	t.Helper()
	rc, err := s.Open(context.Background(), "hu", text, engine.FormatMP3, fetch)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return rc
}

func TestPath(t *testing.T) {
	s := &Store{Dir: "audio"}
	tests := []struct {
		name        string
		fingerprint string
		format      engine.Format
		want        string
	}{
		{"google, the v1 name", "en", engine.FormatMP3, v1Name},
		{"google slow", "en-slow", engine.FormatMP3, "en-slow_" + textMD5 + ".mp3"},
		{"another format", "openai-tts-1-nova", engine.FormatWAV, "openai-tts-1-nova_" + textMD5 + ".wav"},
		{"unsafe characters", `a/b\c d:e*f?g"h<i>j|k`, engine.FormatMP3, "a-b-c-d-e-f-g-h-i-j-k_" + textMD5 + ".mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := filepath.Join("audio", tt.want)
			if got := s.Path(tt.fingerprint, text, tt.format); got != want {
				t.Errorf("Path(%q, %q, %q) = %q, want %q", tt.fingerprint, text, tt.format, got, want)
			}
		})
	}

	other := s.Path("en", "Other", engine.FormatMP3)
	if other == s.Path("en", text, engine.FormatMP3) {
		t.Errorf("Path gives %q for two different texts", other)
	}
	if !strings.HasPrefix(filepath.Base(other), "en_") || !strings.HasSuffix(other, ".mp3") {
		t.Errorf("Path(en, Other, mp3) = %q, want en_<md5>.mp3", other)
	}
}

func TestOpenMissFetchesAndCaches(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	f := &fetcher{data: "mp3 bytes"}

	rc := open(t, s, f.fetch)
	if f.calls != 1 {
		t.Fatalf("fetch called %d times, want 1", f.calls)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != f.data {
		t.Errorf("read %q, want %q", got, f.data)
	}

	// Until Close, the audio lives in a temporary file only.
	path := s.Path("hu", text, engine.FormatMP3)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("before Close, Stat(%s) = %v, want not exist", path, err)
	}
	if tmp := tempFiles(t, dir); len(tmp) != 1 {
		t.Errorf("before Close, temporary files = %q, want one", tmp)
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !f.last.closed {
		t.Error("Close did not close the fetched reader")
	}
	wantEntries(t, dir, "hu_"+textMD5+".mp3")
	if got := readFile(t, path); got != f.data {
		t.Errorf("cached file holds %q, want %q", got, f.data)
	}

	// The next Open is a hit: no fetch, the audio comes from the file.
	if err := os.WriteFile(path, []byte("from the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc = open(t, s, f.fetch)
	got, err = io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll on a hit: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("fetch called %d times after a hit, want 1", f.calls)
	}
	if string(got) != "from the file" {
		t.Errorf("hit read %q, want the file content", got)
	}
}

func TestOpenHitIsTheFile(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	path := s.Path("hu", text, engine.FormatMP3)
	if err := os.WriteFile(path, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fetcher{data: "fetched"}

	rc := open(t, s, f.fetch)
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if f.calls != 0 {
		t.Errorf("fetch called %d times on a hit, want 0", f.calls)
	}
	if string(got) != "cached" {
		t.Errorf("read %q, want the cached file", got)
	}
	wantEntries(t, s.Dir, filepath.Base(path))
}

func TestOpenRecognizesV1Files(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Dir, v1Name), []byte("v1 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fetcher{data: "fetched"}

	rc, err := s.Open(context.Background(), "en", text, engine.FormatMP3, f.fetch)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if f.calls != 0 {
		t.Errorf("fetch called %d times for a file cached by v1, want 0", f.calls)
	}
	if string(got) != "v1 audio" {
		t.Errorf("read %q, want the v1 file", got)
	}
}

func TestOpenTreatsAnEmptyFileAsMissing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	path := s.Path("hu", text, engine.FormatMP3)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fetcher{data: "fetched"}

	rc := open(t, s, f.fetch)
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("fetch called %d times for an empty file, want 1", f.calls)
	}
	if string(got) != "fetched" {
		t.Errorf("read %q, want the fetched audio", got)
	}
	wantEntries(t, s.Dir, filepath.Base(path))
	if got := readFile(t, path); got != "fetched" {
		t.Errorf("the empty file now holds %q, want the fetched audio", got)
	}
}

func TestOpenKeepsEnginesAndFormatsApart(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	f := &fetcher{data: "audio"}
	keys := []struct {
		fingerprint string
		text        string
		format      engine.Format
	}{
		{"hu", text, engine.FormatMP3},
		{"hu-slow", text, engine.FormatMP3},
		{"hu", text, engine.FormatWAV},
		{"hu", "Other", engine.FormatMP3},
	}
	for _, k := range keys {
		rc, err := s.Open(context.Background(), k.fingerprint, k.text, k.format, f.fetch)
		if err != nil {
			t.Fatalf("Open(%q, %q, %q): %v", k.fingerprint, k.text, k.format, err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatalf("reading: %v", err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	if f.calls != len(keys) {
		t.Errorf("fetch called %d times, want %d: every key is a miss of its own", f.calls, len(keys))
	}
	wantEntries(t, s.Dir,
		"hu-slow_"+textMD5+".mp3",
		"hu_"+textMD5+".mp3",
		"hu_"+textMD5+".wav",
		filepath.Base(s.Path("hu", "Other", engine.FormatMP3)),
	)
}

func TestOpenSourceErrorLeavesNothing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	f := &fetcher{data: "partial", readErr: errBoom}

	rc := open(t, s, f.fetch)
	got, err := io.ReadAll(rc)
	if !errors.Is(err, errBoom) {
		t.Errorf("ReadAll error = %v, want the source's error", err)
	}
	if string(got) != "partial" {
		t.Errorf("read %q before the error, want %q", got, "partial")
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !f.last.closed {
		t.Error("Close did not close the fetched reader")
	}
	wantEntries(t, s.Dir)
}

func TestOpenCloseBeforeEOFLeavesNothing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	f := &fetcher{data: "0123456789"}

	rc := open(t, s, f.fetch)
	head := make([]byte, 4)
	if _, err := io.ReadFull(rc, head); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !f.last.closed {
		t.Error("Close did not close the fetched reader")
	}
	wantEntries(t, s.Dir)
	if _, err := rc.Read(head); err == nil {
		t.Error("Read after Close succeeded, want an error")
	}
}

func TestOpenCloseTwice(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	f := &fetcher{data: "audio"}

	rc := open(t, s, f.fetch)
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatalf("reading: %v", err)
	}
	for i := 1; i <= 2; i++ {
		if err := rc.Close(); err != nil {
			t.Errorf("Close call %d: %v", i, err)
		}
	}
	wantEntries(t, s.Dir, "hu_"+textMD5+".mp3")
}

func TestOpenFetchError(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "audio")}
	f := &fetcher{err: errBoom}

	rc, err := s.Open(context.Background(), "hu", text, engine.FormatMP3, f.fetch)
	if !errors.Is(err, errBoom) {
		t.Errorf("Open error = %v, want the fetch error", err)
	}
	if rc != nil {
		t.Errorf("Open returned a reader with the error")
	}
	wantEntries(t, s.Dir)
}

func TestOpenCreatesTheDirectory(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "cache", "audio")}
	f := &fetcher{data: "audio"}

	rc := open(t, s, f.fetch)
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantEntries(t, s.Dir, "hu_"+textMD5+".mp3")
}

func TestOpenStreamsWhileFetching(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	pr, pw := io.Pipe()
	fetch := func(context.Context) (io.ReadCloser, error) { return pr, nil }
	release := make(chan struct{})
	go func() {
		pw.Write([]byte("first,"))
		<-release
		pw.Write([]byte("second"))
		pw.Close()
	}()

	rc := open(t, s, fetch)
	buf := make([]byte, 64)
	n, err := rc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != "first," {
		t.Errorf("first Read = %q, want %q before the rest was fetched", got, "first,")
	}
	close(release)
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(rest) != "second" {
		t.Errorf("rest = %q, want %q", rest, "second")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := readFile(t, s.Path("hu", text, engine.FormatMP3)); got != "first,second" {
		t.Errorf("cached file holds %q, want %q", got, "first,second")
	}
}

func TestCachedFileIsReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not apply on Windows")
	}
	s := &Store{Dir: t.TempDir()}
	f := &fetcher{data: "audio"}

	rc := open(t, s, f.fetch)
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(s.Path("hu", text, engine.FormatMP3))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("cached file mode = %o, want 644", got)
	}
}

func TestWrite(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "audio")}

	path, err := s.Write("hu", text, engine.FormatMP3, strings.NewReader("audio"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := s.Path("hu", text, engine.FormatMP3); path != want {
		t.Errorf("Write returned %q, want %q", path, want)
	}
	if got := readFile(t, path); got != "audio" {
		t.Errorf("written file holds %q, want %q", got, "audio")
	}
	wantEntries(t, s.Dir, filepath.Base(path))

	f := &fetcher{data: "fetched"}
	rc := open(t, s, f.fetch)
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if f.calls != 0 || string(got) != "audio" {
		t.Errorf("after Write, Open fetched %d times and read %q, want a hit with %q", f.calls, got, "audio")
	}
}

func TestWriteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out", "nested")
	path := filepath.Join(dir, "hello.mp3")

	if err := WriteFile(path, strings.NewReader("audio")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := readFile(t, path); got != "audio" {
		t.Errorf("file holds %q, want %q", got, "audio")
	}
	wantEntries(t, dir, "hello.mp3")

	if err := WriteFile(path, strings.NewReader("replaced")); err != nil {
		t.Fatalf("WriteFile over an existing file: %v", err)
	}
	if got := readFile(t, path); got != "replaced" {
		t.Errorf("file holds %q after the second write, want %q", got, "replaced")
	}
	wantEntries(t, dir, "hello.mp3")
}

func TestWriteFileSourceErrorKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.mp3")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := WriteFile(path, &source{r: strings.NewReader("new"), err: errBoom})
	if !errors.Is(err, errBoom) {
		t.Errorf("WriteFile error = %v, want the source's error", err)
	}
	if got := readFile(t, path); got != "old" {
		t.Errorf("file holds %q after a failed write, want %q", got, "old")
	}
	wantEntries(t, dir, "hello.mp3")
}

func TestWriteFileSourceErrorCreatesNoFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	path := filepath.Join(dir, "hello.mp3")

	err := WriteFile(path, &source{r: strings.NewReader("new"), err: errBoom})
	if !errors.Is(err, errBoom) {
		t.Errorf("WriteFile error = %v, want the source's error", err)
	}
	wantEntries(t, dir)
}
