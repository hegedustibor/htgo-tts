# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.0.0] - 2026-10-20

A rewrite with the new import path `github.com/hegedustibor/htgo-tts/v2`. The v1 API stays available from the `v1.6` tag and the `v1` branch; [MIGRATION.md](MIGRATION.md) maps it to v2.

### Added

- The `engine.Engine` interface. The engine that does the synthesis is the first argument of `htgotts.New`, and any type with its three methods, `Synthesize`, `Format` and `Fingerprint`, can be used in place of the engines of the module, for example one that calls Azure Speech (#21). The README says which service the module speaks through (#20).
- `engine/openai`: an engine for the OpenAI speech API and any OpenAI-compatible endpoint, with a choice of model, voice, speed and output format and, with gpt-4o-mini-tts, instructions on how to speak. The API key comes from `WithAPIKey` or `OPENAI_API_KEY`, and `WithBaseURL` reaches Azure OpenAI and local servers. It brings the choice of voice that the Google Translate endpoint cannot offer (#5).
- `Speech.Synthesize` and `Speech.SynthesizeTo` return the audio in memory or write it to an `io.Writer`, and `Speech.Open` streams it, all without writing a file (#23).
- `Speech.Save` writes the audio to a path of the caller's choice, creating the folder and adding the engine's extension when the path has none (#30).
- A `context.Context` on every network and playback call: `Speak`, `Open`, `Synthesize`, `SynthesizeTo`, `Save`, `Play` and `PlayFile` stop when the context is done, and so can a player of your own (#26).
- The `player` package: `player.Player` receives the audio as an `io.Reader`; `player.Command` runs any program that plays from its standard input, with `player.MPlayer()` and `player.FFplay()` ready-made; `player.Discard` plays nothing.
- `player/native`: the in-process MP3 player, now in a package of its own, built on oto v3 and go-mp3 and free of cgo on every platform.
- `google.WithSlow`, `google.WithTimeout`, `google.WithHTTPClient` and `google.WithEndpoint`; `engine.HTTPError` and `engine.ErrEmptyText`; typed errors for a nil engine, a nil player, an empty folder and an empty path.
- `voices.Language`, a typed language code, and `voices.Bengali`, which was unexported.
- Tests that run without network, sound card or API key, and a GitHub Actions CI on Linux and macOS with gofmt, go vet, staticcheck, a cgo-free build and `go test -race`.
- Six runnable programs under `examples/` (#28), `MIGRATION.md` and this changelog.

### Changed

- The module path is `github.com/hegedustibor/htgo-tts/v2`, and the minimum Go version is 1.25 (v1 asked for Go 1.17).
- `htgotts.Speech` is created with `htgotts.New(engine, options...)` instead of a struct literal, and its fields are unexported. The language belongs to the Google engine, `google.New(voices.English)`; the player is set with `htgotts.WithPlayer` and the folder with `htgotts.WithFolder`.
- The cache folder is optional: without `WithFolder`, no file is written at all (#18). With it, the file names are the ones v1 wrote for the Google engine, `<language>_<md5>.mp3`, so an existing folder keeps serving.
- Players receive a stream instead of a file name: `Play(ctx context.Context, audio io.Reader) error`. mplayer and ffplay read it from standard input.
- The proxy of the Google engine is set with `google.WithProxy` and keeps TLS verification on; v1 turned it off. A client of your own, through `google.WithHTTPClient`, can turn it off.
- `voices.EnglishUK` is `en-GB`, the code the endpoint expects, instead of `en-UK`; its cache files are therefore named differently.
- The Google engine requests over HTTPS with one HTTP client, a timeout of 30 seconds per request and a check of the status and the content type of every answer.
- Errors are wrapped and typed, so `errors.Is` and `errors.As` tell a missing player, an empty text, an unexpected answer of the speech service and a done context apart (#14).
- The native player opens the sound card once per process, at the sample rate of the first MP3, and reuses it.

### Removed

- The `handlers` package: `handlers.PlayerInterface`, `handlers.MPlayer` and `handlers.Native`. `player.Player`, `player.MPlayer()` and `native.New()` take their place.
- `Speech.CreateSpeechFile`, `Speech.CreateSpeechBuff` and `Speech.PlaySpeechFile`. `Save`, `Synthesize`, `Open` and `PlayFile` take their place.
- The `Folder`, `Language`, `Proxy` and `Handler` fields of `Speech`.
- The dependency of the root package on the audio library: a program that only saves or synthesizes speech links no audio library, and the whole module builds with `CGO_ENABLED=0`, so building in Docker and on machines without ALSA headers works (#15, #27, #33).
- The `V1.0` tag, a duplicate of `v1.0` that broke `go get` on case-insensitive file systems (#35), is removed with this release.

### Fixed

- Text longer than about a hundred characters: the Google engine splits it at sentence and clause boundaries and fetches the pieces one after the other, so long text no longer comes back as an HTML error page saved as `.mp3` (#10). The OpenAI engine splits at 4096 characters.
- The native player crashed on the second playback in a process (#31).
- `CreateSpeechBuff` returned a drained reader; its replacements `Synthesize` and `Open` return the audio.
- The last character of a text was dropped when its length was one more than a multiple of 32.
- A failed download left a half-written file in the cache; files are now written atomically and appear only once complete.
- The HTTP status and content type of the endpoint's answer were not checked, so error pages were saved as audio (#14).
- The Google engine's proxy requests went out without TLS verification.
- Cache paths are built with `filepath.Join`, so a folder with a trailing separator and Windows paths work.
- mplayer was run with `-cache 8092 - <file>`, where `-` meant an unused standard input; it now reads the audio from standard input.

## [1.6] - 2024-09-12

The last release of the v1 API, which added chunked `CreateSpeechBuff` output for longer text (#32). v1 stays available from this tag and from the `v1` branch, which is for critical fixes only.

[2.0.0]: https://github.com/hegedustibor/htgo-tts/compare/v1.6...v2.0.0
[1.6]: https://github.com/hegedustibor/htgo-tts/compare/v1.5...v1.6
