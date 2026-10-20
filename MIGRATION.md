# Migrating from v1 to v2

htgo-tts v2 is a rewrite with a new import path, `github.com/hegedustibor/htgo-tts/v2`, and a new API: you choose the engine that does the synthesis, players read the audio from a stream instead of a file, every call takes a `context.Context`, and nothing is written to disk unless you ask for a cache folder. This guide maps the v1 API to v2 and lists what behaves differently.

v1 is not going away. `go get github.com/hegedustibor/htgo-tts@v1.6.0` keeps serving the v1 API as it is: the `v1.6.0` tag marks the same commit as the older `v1.6` tag, which Go cannot resolve because it is not a semantic version. The `v1` branch is where a critical fix for v1 would land; v1 gets no new features.

## Import path

```bash
go get github.com/hegedustibor/htgo-tts/v2
```

```go
import (
	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/player"
	"github.com/hegedustibor/htgo-tts/v2/player/native"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)
```

The `handlers` package is gone; `player` and `player/native` take its place. Both versions can live in one program during a transition, because their import paths differ.

## Before and after

v1:

```go
speech := htgotts.Speech{Folder: "audio", Language: voices.English, Handler: &handlers.MPlayer{}}
if err := speech.Speak("Hello, world!"); err != nil {
	log.Fatal(err)
}
```

v2:

```go
tts, err := htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"))
if err != nil {
	log.Fatal(err)
}
if err := tts.Speak(context.Background(), "Hello, world!"); err != nil {
	log.Fatal(err)
}
```

## API

| v1 | v2 |
|---|---|
| `htgotts.Speech{Folder: "audio", Language: voices.English, Handler: &handlers.MPlayer{}}` | `htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"), htgotts.WithPlayer(player.MPlayer()))`; `New` returns an error for a nil engine or an invalid option |
| `Language: voices.English`, a `string` field of `Speech` | `google.New(voices.English)`: the language is a setting of the Google engine, and the constants have the type `voices.Language` |
| `Folder: "audio"`, required | `htgotts.WithFolder("audio")`, optional; without it no file is written |
| `Handler: &handlers.MPlayer{}`, or no handler | `htgotts.WithPlayer(player.MPlayer())`, the default when no player is given |
| `Handler: &handlers.Native{}` | `htgotts.WithPlayer(native.New())`, from the `player/native` package |
| `Proxy: "http://…"` | `google.WithProxy("http://…")`, with TLS verification on |
| `speech.Speak(text)` | `tts.Speak(ctx, text)` |
| `speech.CreateSpeechFile(text, name)`, which wrote `<Folder>/<name>.mp3` and returned the path | `tts.Save(ctx, text, "audio/name.mp3")`: the path is yours to choose, the `.mp3` extension is added when the path has none, the folder is created, and the path is returned |
| `speech.CreateSpeechBuff(text, name)`, an `io.Reader` | `tts.Synthesize(ctx, text)` for the bytes, or `tts.Open(ctx, text)` for a stream; neither writes a file unless there is a cache folder |
| `speech.PlaySpeechFile(path)` | `tts.PlayFile(ctx, path)` |
| `handlers.PlayerInterface`, `Play(fileName string) error` | `player.Player`, `Play(ctx context.Context, audio io.Reader) error`: the player reads the audio from the stream and stops when `ctx` is done |
| a handler of your own that runs a program on the file | `&player.Command{Name: "play", Args: []string{"-q", "-t", "mp3", "-"}}`: any program that plays from its standard input |
| no equivalent | `htgotts.New(openai.New(openai.WithVoice(openai.VoiceNova)))`: the OpenAI engine, with model, voice, speed, format and instructions |
| no equivalent | an engine of your own: any type with the three methods of `engine.Engine` |
| no equivalent | `tts.SynthesizeTo(ctx, text, w)` and `tts.Play(ctx, audio)` |

## Behaviour

- **No file unless you ask.** v1 always wrote the audio into `Folder`. v2 writes nothing without `WithFolder`; with it, the audio of every text is kept under `<fingerprint>_<md5 of the text>.<format>`, and a text spoken before is served from its file. For the Google engine the fingerprint is the language code, so the names are the ones v1 wrote, `en_<md5>.mp3`, and a folder filled by v1 keeps serving hits. A file appears under its name only once its audio has been read to the end, so an interrupted download leaves nothing behind, where v1 could leave a half-written file or an HTML error page named `.mp3`.
- **Long text works.** The Google endpoint speaks about a hundred characters per request; v1 sent the whole text in one request and got an error page back above about two hundred characters. v2 splits the text at sentence and clause boundaries and fetches the pieces one after the other while the audio already plays.
- **Every call takes a context.** `Speak` returns when the playback has finished or when the context is done, and a done context stops the download and the player alike. Pass `context.Background()` where you have no deadline.
- **Errors are typed and wrapped.** `errors.Is` and `errors.As` tell `engine.ErrEmptyText`, `*engine.HTTPError` (the speech service answered with something other than audio), `player.ErrPlayerNotFound` (the program is not installed) and a done context apart. v1 reported a missing network connection and an error page alike, with a broken `.mp3` file.
- **The proxy verifies TLS.** v1 turned certificate verification off whenever `Proxy` was set. v2 keeps it on; to turn it off, or to tune the proxy, give the engine an `http.Client` of your own with `google.WithHTTPClient`, which cannot be combined with `google.WithProxy`.
- **British English is `en-GB`.** `voices.EnglishUK` was `en-UK`, which is not the code the endpoint expects. Its cache files are named `en-GB_<md5>.mp3` now, so British sentences cached by v1 are fetched again.
- **Players read a stream.** `player.Player` receives an `io.Reader`, not a file name, so a player of your own must be rewritten; `player.Command` covers any program that plays from its standard input. mplayer runs as `mplayer -really-quiet -cache 8092 -`.
- **The native player lives in `player/native` and needs no cgo.** It no longer crashes on the second playback, plays MP3 only, and opens the sound card once per process at the sample rate of the first MP3; Google Translate and OpenAI both produce 24 kHz MP3. On Linux the machine that runs the program needs the ALSA shared library (package `libasound2`, or `libasound2t64` on Debian 13 and Ubuntu 24.04 and later); no headers and no C compiler are needed to build. A program that does not import `player/native` links no audio library at all, so it builds with `CGO_ENABLED=0` anywhere, in Docker included.
- **Go 1.25 or newer.** v1 asked for Go 1.17; v2 needs 1.25 because of the audio library of the native player.
- **The options validate.** `New` fails on a nil engine, a nil player and an empty folder. An invalid engine option, such as a malformed proxy URL or a missing OpenAI API key, is reported by the first `Speak`, `Open`, `Synthesize` or `Save`, not by `google.New` or `openai.New`.
