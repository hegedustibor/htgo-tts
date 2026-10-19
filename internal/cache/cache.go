// Package cache keeps synthesized audio in files, so that a speech service
// is asked for a given text only once, and writes audio files atomically.
//
// # File names
//
// A Store names the file of a text <fingerprint>_<md5>.<ext>: the engine's
// Fingerprint, an underscore, the lowercase hexadecimal MD5 of the text and
// the extension of the engine's Format, for example
// "hu_0cbc6611f5540bd0809a388dc95a615b.mp3". Characters that file names
// cannot contain on common systems are replaced in the fingerprint. The
// fingerprint of the Google Translate engine is the language code, which
// makes these the names that htgotts v1 wrote, so a folder filled by v1
// keeps serving hits to the v2 Google engine.
//
// # Atomic writes
//
// No audio is ever written under its final name directly. It goes to a
// temporary ".tmp-*" file in the same directory, which is renamed to the
// final name only once the audio is complete. A download that fails or is
// abandoned half-way leaves nothing behind, and an empty file counts as
// missing, so a file under its final name always holds a complete
// utterance.
package cache

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegedustibor/htgo-tts/v2/engine"
)

// tempPattern names the temporary files that hold audio while it is being
// written; see os.CreateTemp for the meaning of the star.
const tempPattern = ".tmp-*"

// unsafeInFileName holds the characters that Path replaces in a fingerprint:
// the path separators, the space, and the characters that Windows forbids in
// file names.
const unsafeInFileName = "/\\ :*?\"<>|"

// Store is a directory of cached audio files.
type Store struct {
	// Dir is the directory that holds the files. It is created, with its
	// parents, when the first file is written.
	Dir string
}

// Path returns the name of the file that holds text as spoken by the engine
// with the given fingerprint and format, whether or not the file exists:
// Dir joined with <fingerprint>_<md5>.<ext>, where every character of the
// fingerprint that is not safe in a file name is replaced by a hyphen.
func (s *Store) Path(fingerprint, text string, format engine.Format) string {
	// MD5 is a cache key here, not a security measure, and it keeps the
	// names compatible with htgotts v1.
	sum := md5.Sum([]byte(text))
	name := fileNameSafe(fingerprint) + "_" + hex.EncodeToString(sum[:]) + format.Ext()
	return filepath.Join(s.Dir, name)
}

// fileNameSafe returns s with every character of unsafeInFileName replaced
// by a hyphen.
func fileNameSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(unsafeInFileName, r) {
			return '-'
		}
		return r
	}, s)
}

// Open returns the audio of text as spoken by the engine with the given
// fingerprint and format: from the cache when a complete file is there,
// otherwise from fetch, whose audio is cached while it is read.
//
// A hit is a non-empty regular file under the name that Path returns; Open
// opens it and returns it. On a miss Open creates Dir if needed, prepares a
// temporary ".tmp-*" file in Dir, calls fetch(ctx) and returns a reader that
// delivers fetch's audio unchanged while also copying it to the temporary
// file, through an io.TeeReader. What becomes of that file is decided by
// Close:
//
//   - when the reader has been read to a clean io.EOF, Close renames the
//     temporary file to its final name, and the next Open of the same text
//     is a hit;
//   - when fetch's reader fails, when writing the temporary file fails, or
//     when Close is called before io.EOF, Close removes the temporary file
//     and nothing is cached.
//
// Close always closes fetch's reader as well, and calling it more than once
// is harmless. Errors from fetch and from the returned reader are passed on
// unchanged. Two callers that miss on the same text both fetch it; each
// writes its own temporary file, so the file that ends up under the final
// name is complete either way.
func (s *Store) Open(ctx context.Context, fingerprint, text string, format engine.Format, fetch func(context.Context) (io.ReadCloser, error)) (io.ReadCloser, error) {
	path := s.Path(fingerprint, text, format)
	if f := openComplete(path); f != nil {
		return f, nil
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("htgotts: cache: %w", err)
	}
	// The temporary file comes before fetch, so that a cache directory that
	// cannot be written fails before the speech service is called.
	tmp, err := os.CreateTemp(s.Dir, tempPattern)
	if err != nil {
		return nil, fmt.Errorf("htgotts: cache: %w", err)
	}
	src, err := fetch(ctx)
	if err != nil {
		discard(tmp)
		return nil, err
	}
	return &cachingReader{tee: io.TeeReader(src, tmp), src: src, tmp: tmp, path: path}, nil
}

// openComplete opens path when it is a non-empty regular file and returns
// nil otherwise, so that a missing, empty or odd entry counts as a miss.
func openComplete(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		f.Close()
		return nil
	}
	return f
}

// Write caches src as the audio of text spoken by the engine with the given
// fingerprint and format, replacing a cached file of the same name, and
// returns the path of the file. It writes atomically, like WriteFile.
func (s *Store) Write(fingerprint, text string, format engine.Format, src io.Reader) (string, error) {
	path := s.Path(fingerprint, text, format)
	if err := WriteFile(path, src); err != nil {
		return "", err
	}
	return path, nil
}

// WriteFile writes src to path atomically, creating the parent directory
// when needed. The data goes to a temporary ".tmp-*" file in the same
// directory, which replaces path only once src has been read to io.EOF, so
// nobody ever finds a partial file under path. When copying fails, the
// temporary file is removed and path is left as it was; the copy error,
// which comes from src or from writing the temporary file, is returned
// unchanged.
func WriteFile(path string, src io.Reader) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("htgotts: write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("htgotts: write %s: %w", path, err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		discard(tmp)
		return err
	}
	if err := commit(tmp, path); err != nil {
		return fmt.Errorf("htgotts: write %s: %w", path, err)
	}
	return nil
}

// cachingReader delivers fetched audio and copies it to a temporary file,
// which Close turns into the cached file once the audio is complete.
type cachingReader struct {
	tee    io.Reader     // src teed into tmp
	src    io.ReadCloser // the fetched audio
	tmp    *os.File      // the temporary file in the cache directory
	path   string        // the final name of the cached file
	eof    bool          // the audio was read to a clean io.EOF
	failed bool          // reading src or writing tmp failed
	closed bool
}

// Read reads from the fetched audio and records whether it ended cleanly.
func (r *cachingReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	n, err := r.tee.Read(p)
	switch {
	case err == nil:
	case err == io.EOF:
		r.eof = true
	default:
		r.failed = true
	}
	return n, err
}

// Close closes the fetched audio and keeps the temporary file as the cached
// file when the audio was complete, or removes it otherwise.
func (r *cachingReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	srcErr := r.src.Close()
	if !r.eof || r.failed {
		discard(r.tmp)
		return srcErr
	}
	if err := commit(r.tmp, r.path); err != nil {
		return fmt.Errorf("htgotts: cache: %w", err)
	}
	return srcErr
}

// commit closes tmp and renames it to path, replacing whatever is there.
// The temporary file is removed when that fails.
func commit(tmp *os.File, path string) error {
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// os.CreateTemp makes the file private (0600). Cached and saved audio
	// is ordinary data, so give it the mode that os.Create gives under the
	// usual umask.
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// discard closes and removes a temporary file that will not be kept.
func discard(tmp *os.File) {
	tmp.Close()
	os.Remove(tmp.Name())
}
