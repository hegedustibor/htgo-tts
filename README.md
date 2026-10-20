![HTGO-TTS](https://banners.beyondco.de/HTGO-TTS.png?theme=light&packageManager=&packageName=go+get+github.com%2Fhegedustibor%2Fhtgo-tts%2Fv2&pattern=bamboo&style=style_1&description=Text+to+Speech+Package+for+GoLang&md=1&showWatermark=0&fontSize=100px&images=volume-up)

# htgo-tts

[![Go Reference](https://pkg.go.dev/badge/github.com/hegedustibor/htgo-tts/v2.svg)](https://pkg.go.dev/github.com/hegedustibor/htgo-tts/v2)
[![CI](https://github.com/hegedustibor/htgo-tts/actions/workflows/ci.yml/badge.svg)](https://github.com/hegedustibor/htgo-tts/actions/workflows/ci.yml)

htgo-tts turns text into speech from Go. It asks a text-to-speech engine for the audio of a text and plays it, saves it to a file, or hands it to you as a stream or as bytes in memory. The engine is yours to choose: the unofficial Google Translate endpoint, free and without an API key; the OpenAI speech API with its models and voices; or an engine of your own, written with three methods.

This is version 2, a rewrite with a new import path. Coming from v1? [MIGRATION.md](MIGRATION.md) maps the old API to the new one; v1 itself stays available as `github.com/hegedustibor/htgo-tts@v1.6.0` and from the `v1` branch.

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Engines](#engines)
  - [Google Translate](#google-translate)
  - [OpenAI](#openai)
  - [Azure Speech](#azure-speech)
  - [Google Cloud Text-to-Speech](#google-cloud-text-to-speech)
  - [ElevenLabs](#elevenlabs)
  - [A program on this machine](#a-program-on-this-machine)
  - [An engine of your own](#an-engine-of-your-own)
- [Players](#players)
- [Streams, cache and files](#streams-cache-and-files)
- [Errors](#errors)
- [Languages](#languages)
- [Limitations](#limitations)
- [Docker](#docker)
- [Examples](#examples)
- [Migrating from v1](#migrating-from-v1)
- [Support and contributions](#support-and-contributions)
- [License](#license)

## How it works

A `Speech` ties an engine to a player.

- The **engine** turns text into a stream of audio. The module ships two. `engine/google` speaks through the unofficial Google Translate endpoint: free, without an API key, one language per engine, as v1 did. `engine/openai` speaks through the OpenAI speech API, or any OpenAI-compatible endpoint, with a choice of model, voice, speed and format; it needs an API key, and every request is billed. Any type with the three methods of `engine.Engine` works in their place.
- The **player** plays the audio. By default it is mplayer, run as an external program that reads the audio from its standard input. ffplay, any other such command, or the native player, which decodes MP3 in-process and plays it through the sound card without an external program, can take its place.
- The **cache** is optional. Without a folder nothing is written to disk: the audio streams from the engine to the player, or to you. With one, the audio of every text is kept as a file and served from it the next time.

Every network and playback call takes a `context.Context`: a deadline or a cancellation stops the download and the playback alike. The full API is on [pkg.go.dev](https://pkg.go.dev/github.com/hegedustibor/htgo-tts/v2).

## Requirements

- Go 1.25 or newer.
- Network access for the engines that call a web service; the command engine runs a speech program on the machine and works offline.
- For the default player, mplayer in `PATH`; ffplay works the same way. The native player needs no external program and no cgo; on Linux, the machine that runs the program needs the ALSA shared library (package `libasound2`, or `libasound2t64` on Debian 13 and Ubuntu 24.04 and later). A program that only saves or synthesizes speech needs no player at all.
- For the OpenAI engine, an API key; every request is billed.

## Installation

```bash
go get github.com/hegedustibor/htgo-tts/v2
```

## Quick start

Speak an English sentence through Google Translate and mplayer, keeping the audio in the `audio` folder so that a sentence is downloaded only once:

```go
package main

import (
	"context"
	"log"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

func main() {
	tts, err := htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tts.Speak(ctx, "Hello, world!"); err != nil {
		log.Fatal(err)
	}
}
```

`New` validates its arguments and touches neither the network nor the file system. `Speak` downloads the audio, or reads it from the cache, plays it, and returns when the playback has finished or the context is done. Leave out `WithFolder` and no file is written.

## Engines

The engine is the first argument of `New`. Switching engines means changing the line that creates it; everything else stays as it is.

### Google Translate

`engine/google` uses the endpoint that v1 used, `https://translate.google.com/translate_tts`: free, without an API key, one language per engine, MP3 output. The endpoint speaks about a hundred characters per request, so longer text is split at sentence and clause boundaries and fetched in several requests, one after the other, while the audio already streams to the player.

```go
e := google.New(
	voices.Hungarian,
	google.WithSlow(),                                 // speak slowly
	google.WithTimeout(20*time.Second),                // per request; 30 s by default
	google.WithProxy("http://proxy.example.com:3128"), // HTTP, HTTPS or SOCKS5 proxy; TLS verification stays on
	google.WithEndpoint("https://example.com/tts"),    // another server with the same API, or a test server
)
```

The engine sends its requests with an HTTP client of its own. `google.WithHTTPClient(client)` replaces it, for example to turn TLS verification off for a proxy that inspects the traffic, or to record the requests in a test; `WithHTTPClient` and `WithProxy` exclude each other, so give such a client its proxy through its transport. An invalid option, such as a malformed proxy URL or an empty language, does not make `New` fail: the engine remembers the error, and the first `Speak`, `Open`, `Synthesize` or `Save` returns it.

The endpoint is undocumented and unofficial. Google may change it or rate-limit it without notice; the OpenAI engine, or an engine of your own, is the way out when that happens.

### OpenAI

`engine/openai` posts to `/v1/audio/speech`: a paid service that speaks any language with a choice of models and voices, at a chosen speed, in a chosen format and, with the gpt-4o-mini-tts model, in a tone described in words. The API key comes from `openai.WithAPIKey` or, without it, from the `OPENAI_API_KEY` environment variable; it only ever travels in the `Authorization` header and never appears in an error or a file name. Every request is billed to the key's account.

```go
e := openai.New(
	openai.WithAPIKey(key),                                     // or leave it out and set OPENAI_API_KEY
	openai.WithModel(openai.ModelGPT4oMiniTTS),                 // tts-1 (default), tts-1-hd or gpt-4o-mini-tts
	openai.WithVoice(openai.VoiceNova),                         // alloy (default), ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse, marin or cedar
	openai.WithSpeed(1.25),                                     // 0.25 to 4; 1 by default
	openai.WithInstructions("Speak in a calm, friendly tone."), // gpt-4o-mini-tts only
	openai.WithFormat(engine.FormatMP3),                        // mp3 (default), wav, opus, aac, flac or pcm
	openai.WithTimeout(30*time.Second),                         // per request; 60 s by default
)
```

`openai.Model` and `openai.Voice` are string types, so a model or a voice that the package has no constant for, such as a newer one or one of a compatible server, is `openai.Model("name")` or `openai.Voice("name")`. The endpoint accepts 4096 characters per request; longer text is split at sentence and clause boundaries and requested piece by piece. That is sound for MP3 and raw PCM; with the container formats (WAV, FLAC, AAC, Opus) every piece carries a header of its own, so keep the text within one request when using them.

**OpenAI-compatible servers.** `openai.WithBaseURL` sends the requests to another server that offers the same endpoint, at `<base URL>/audio/speech`. The engine still needs a key, even when the server ignores it:

```go
e := openai.New(
	openai.WithBaseURL("http://localhost:8000/v1"), // a speech server on this machine
	openai.WithAPIKey("none"),                      // the server ignores it; the engine insists on one
	openai.WithModel(openai.Model("my-model")),
	openai.WithVoice(openai.Voice("my-voice")),
)
```

**Azure OpenAI.** The base URL is the full deployment URL with its `api-version` query parameter, which the engine keeps on every request. Azure accepts a Microsoft Entra ID token as the bearer token; for its `api-key` header instead, give the engine a client whose transport adds that header with `openai.WithHTTPClient`.

```go
e := openai.New(
	openai.WithBaseURL("https://my-resource.openai.azure.com/openai/deployments/my-tts?api-version=2025-04-01-preview"),
	openai.WithAPIKey(entraToken),
)
```

### Azure Speech

`engine/azure` posts SSML to the Azure Speech text to speech endpoint, `https://<region>.tts.speech.microsoft.com/cognitiveservices/v1`: a paid service with a free monthly allowance, hundreds of neural voices in over a hundred languages, speaking styles for many of them, and a choice of rate, pitch and format. The resource key comes from `azure.WithKey` or the `AZURE_SPEECH_KEY` environment variable, the region from `azure.WithRegion` or `AZURE_SPEECH_REGION`; the key only ever travels in the `Ocp-Apim-Subscription-Key` header and never appears in an error or a file name. This is the Speech service, not Azure OpenAI: the OpenAI voices hosted on Azure go through the OpenAI engine above.

```go
e := azure.New(
	azure.WithKey(key),                    // or leave it out and set AZURE_SPEECH_KEY
	azure.WithRegion("westeurope"),        // or AZURE_SPEECH_REGION; azure.WithEndpoint for a custom voice or a sovereign cloud
	azure.WithVoice(azure.VoiceHuHUNoemi), // any voice of the service as azure.Voice("name"); the language follows the voice
	azure.WithStyle("cheerful"),           // a style the voice has, from the voices/list endpoint; ignored otherwise
	azure.WithRate("+10%"),                // "0.8", "-20%", "slow", "fast", ...
	azure.WithPitch("-2st"),               // "+50Hz", "high", "-10%", ...
	azure.WithFormat(engine.FormatMP3),    // mp3 (default), wav, opus or pcm, all at 24 kHz
	azure.WithTimeout(30*time.Second),     // per request; 60 s by default
)
```

`azure.WithLanguage` sets the language of the SSML document when the voice's name does not carry it, as the name of a custom voice does not, or when a multilingual voice should read another language. `azure.WithOutputFormat("audio-48khz-192kbitrate-mono-mp3", engine.FormatMP3)` requests any other format of the service by its name; the native player plays 24 kHz MP3 only. The service cuts the audio of one request at ten minutes, so longer text is split at sentence and clause boundaries and requested piece by piece, which is sound for MP3 and PCM, while WAV and Opus pieces each carry a header of their own.

### Google Cloud Text-to-Speech

`engine/googlecloud` posts JSON to the Google Cloud Text-to-Speech endpoint, `https://texttospeech.googleapis.com/v1/text:synthesize`: the official, paid counterpart of the Translate endpoint above, with a free monthly allowance, hundreds of voices in dozens of languages, among them the Standard, WaveNet, Neural2, Studio and Chirp 3: HD kinds, and a choice of speaking rate, pitch, volume, sample rate and format. The API key comes from `googlecloud.WithKey` or the `GOOGLE_CLOUD_API_KEY` environment variable; it only ever travels in the `x-goog-api-key` header, never in the URL, and never appears in an error or a file name.

```go
e := googlecloud.New(
	googlecloud.WithKey(key),                             // or leave it out and set GOOGLE_CLOUD_API_KEY
	googlecloud.WithVoice(googlecloud.VoiceHuHUWavenetB), // any voice of the service as googlecloud.Voice("name"); the language follows the voice
	googlecloud.WithSpeakingRate(1.1),                    // 0.25–2; 1 is the voice's own pace
	googlecloud.WithPitch(-2),                            // semitones, -20–20
	googlecloud.WithFormat(engine.FormatMP3),             // mp3 (default), wav, opus or pcm
	googlecloud.WithTimeout(30*time.Second),              // per request; 60 s by default
)
```

Without `googlecloud.WithVoice` the service chooses a voice for the language of `googlecloud.WithLanguage`, en-US by default, preferring the gender of `googlecloud.WithGender`. The audio is sampled at 24 kHz, the rate the native player plays, unless `googlecloud.WithSampleRate` asks for another; `googlecloud.WithVolumeGain` raises or lowers the volume in decibels. The endpoint accepts 5000 bytes of text per request, so longer text is split at sentence and clause boundaries and requested piece by piece, which is sound for MP3 and PCM, while WAV and Opus pieces each carry a header of their own. The Chirp 3: HD voices take no speaking rate and no pitch.

### ElevenLabs

`engine/elevenlabs` posts to the ElevenLabs text to speech API, `https://api.elevenlabs.io/v1/text-to-speech/<voice>/stream`: a paid service with a free monthly allowance, a library of thousands of voices plus cloned and designed voices of your own, and models that speak dozens of languages with a chosen stability, similarity, style, speed and output format. The API key comes from `elevenlabs.WithKey` or the `ELEVENLABS_API_KEY` environment variable; it only ever travels in the `xi-api-key` header and never appears in an error or a file name. Every request is billed, by characters, to the key's account.

```go
e := elevenlabs.New(
	elevenlabs.WithKey(key),                        // or leave it out and set ELEVENLABS_API_KEY
	elevenlabs.WithVoice(elevenlabs.VoiceSarah),    // a premade voice, or any voice of the account as elevenlabs.Voice("id")
	elevenlabs.WithModel(elevenlabs.ModelFlashV25), // eleven_multilingual_v2 (default), eleven_flash_v2_5, eleven_v3, eleven_v4, ...
	elevenlabs.WithLanguage("hu"),                  // ISO 639-1 code, enforced by the models that support it
	elevenlabs.WithStability(0.4),                  // 0 to 1
	elevenlabs.WithSimilarity(0.8),                 // 0 to 1
	elevenlabs.WithSpeed(1.1),                      // 0.7 to 1.2
	elevenlabs.WithFormat(engine.FormatMP3),        // mp3 (default, 44.1 kHz), pcm (24 kHz) or opus (48 kHz)
	elevenlabs.WithTimeout(30*time.Second),         // per request; 60 s by default
)
```

`elevenlabs.Voice` and `elevenlabs.Model` are string types: the constants are the premade voices that every account has and the documented models, and the ID of a voice of your own, from the `GET /v1/voices` endpoint or from the voice's page in the ElevenLabs app, is `elevenlabs.Voice("id")`. `elevenlabs.WithStyle` and `elevenlabs.WithSpeakerBoost` set the remaining two voice settings; the engine sends only the settings it was given. `elevenlabs.WithOutputFormat("mp3_22050_32", engine.FormatMP3)` requests any other format of the service by its name, and `elevenlabs.WithBaseURL("https://api.eu.residency.elevenlabs.io/v1")` reaches the data residency endpoints. The streaming endpoint produces no WAV, AAC or FLAC; PCM is WAV without its header.

**Sample rate.** The MP3 of this engine is sampled at 44.1 kHz, while the other web engines produce 24 kHz MP3, and the native player opens the sound card at the sample rate of the first MP3 it plays: one process cannot play this engine and the others through it, unless `elevenlabs.WithOutputFormat("mp3_24000_48", engine.FormatMP3)` asks for a 24 kHz MP3. Text longer than 2500 characters is split at sentence and clause boundaries and requested piece by piece, each piece with its neighbours as `previous_text` and `next_text` so that the speech stays continuous across the cut; that is sound for MP3 and PCM, while every Opus piece is a stream of its own.

### A program on this machine

`engine/command` runs a speech program installed on the machine and needs neither an API key nor a network connection, so speech works offline: the `say` command of macOS, espeak-ng, piper, or any other program that turns text into audio. Three engines are ready-made, all producing WAV:

```go
say := command.Say("Samantha")                     // macOS: say -v Samantha -o <file> --file-format=WAVE --data-format=LEI16@22050
espeak := command.ESpeakNG("en-gb")                // Linux, macOS, Windows: espeak-ng -v en-gb --stdin --stdout
piper := command.Piper("en_US-lessac-medium.onnx") // Linux, macOS, Windows: piper --model <model> --output_file <file>
```

An empty voice leaves the choice to the program. Any other program is a `command.New` with the format it produces, its name and its arguments. By default the text goes to the program's standard input and the audio streams from its standard output, so playback starts before the program has finished. Two placeholders change that: `{text}` passes the text as an argument instead, and `{out}` is the path of a temporary file, with the format's extension, that the program writes the audio to; the engine then waits for the program to exit, reads the file and deletes it afterwards. pico2wave, for example, takes the text as its last argument and writes a `.wav` file:

```go
e := command.New(engine.FormatWAV, "pico2wave", "-l", "en-GB", "-w", "{out}", "{text}")
```

`With` applies the options to a copy of the engine: `command.WithDir` sets the working directory, `command.WithEnv` adds environment variables, such as `ESPEAK_DATA_PATH` for espeak-ng, and `command.WithTimeout` bounds the run of the program.

```go
e := command.ESpeakNG("hu").With(command.WithTimeout(30 * time.Second))
```

A program that is not installed fails with `command.ErrProgramNotFound` and its name; a non-zero exit comes back as an error ending with the program's last standard error output; a done context interrupts the program. mplayer and ffplay play the WAV, and `Save` writes it; the native player plays MP3 only. `say` speaks with the default voice when the voice is not installed, without reporting it. espeak-ng and piper have not been run while writing this: their arguments come from their documentation.

### An engine of your own

An engine is any type with these three methods:

```go
type Engine interface {
	// Synthesize returns the audio of text as a stream, in the format that
	// Format reports, and stops when ctx is done. The caller closes it.
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
	// Format reports the encoding of the audio: the extension of saved and
	// cached files, and the hint for choosing a player.
	Format() engine.Format
	// Fingerprint names the engine and the settings that change the sound,
	// such as the language or the voice. It becomes part of cache file
	// names, so it is short and holds no secret and no URL.
	Fingerprint() string
}
```

A minimal engine that asks an HTTP speech server for the audio, with the text in a query parameter:

```go
type myEngine struct{ client *http.Client }

func (e *myEngine) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if strings.TrimSpace(text) == "" {
		return nil, engine.ErrEmptyText
	}
	u := "http://localhost:5002/api/tts?text=" + url.QueryEscape(text)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err // wraps ctx.Err() when ctx is done
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &engine.HTTPError{StatusCode: resp.StatusCode, URL: "http://localhost:5002/api/tts"}
	}
	return resp.Body, nil // streams to the player, which closes it
}

func (e *myEngine) Format() engine.Format { return engine.FormatWAV }
func (e *myEngine) Fingerprint() string   { return "my-engine" }
```

```go
tts, err := htgotts.New(&myEngine{client: http.DefaultClient})
```

`Synthesize` returns `engine.ErrEmptyText` for empty text, an error wrapping `ctx.Err()` when the context is done, and `*engine.HTTPError` when the service answers with something other than audio, so that the errors of your engine read like those of the built-in ones. An engine must be safe for concurrent use. [examples/custom-engine](examples/custom-engine/main.go) is the complete version, with flags for the URL and the format.

## Players

The player is set with `htgotts.WithPlayer`; without it, mplayer plays.

```go
tts, err := htgotts.New(e, htgotts.WithPlayer(player.FFplay()))
```

| Player | What it does |
|---|---|
| `player.MPlayer()` | The default. Runs `mplayer -really-quiet -cache 8092 -`, the audio on its standard input; mplayer plays every format the engines produce. |
| `player.FFplay()` | Runs `ffplay -nodisp -autoexit -loglevel error -`, part of FFmpeg. Not yet checked against a running ffplay: if yours does not play, replace the final argument with `pipe:0`. |
| `native.New()` | From `player/native`: decodes MP3 with go-mp3 and plays it through the sound card with oto, in-process, without an external program and without cgo. MP3 only; the sound card is opened once per process, at the sample rate of the first MP3, and both engines produce 24 kHz MP3. On Linux, the ALSA shared library must be installed. |
| `&player.Command{Name: "play", Args: []string{"-q", "-t", "mp3", "-"}}` | Any program that plays audio from its standard input, here SoX. The program's last standard error output is part of the error when it exits with a non-zero status. |
| `player.Discard` | Reads the audio without playing it: for tests, and for programs that only download or cache speech. |

A player of your own is any type with one method, `Play(ctx context.Context, audio io.Reader) error`, which plays the audio to its end and returns early with an error wrapping `ctx.Err()` when the context is done first. A player does not know the format of the audio: pairing it with an engine whose output it understands is your job.

When the player's program is not installed, `Play` fails with `player.ErrPlayerNotFound` and the program's name. `Speak` synthesizes first and plays second, so a missing player is noticed after the download; install the player before relying on it. The audio of such a run is not cached, because the player did not read it to the end.

## Streams, cache and files

Without `WithFolder`, no file is ever written: the audio goes to the player, or to you.

```go
tts, err := htgotts.New(google.New(voices.English))
if err != nil {
	log.Fatal(err)
}

// The whole audio in memory, as MP3 here.
mp3, err := tts.Synthesize(ctx, "Hello, world!")

// Written to any io.Writer, for example an HTTP response.
err = tts.SynthesizeTo(ctx, "Hello, world!", w)

// As a stream: the first bytes are readable before the whole text has been
// synthesized. Close it when done, also after reading it to the end.
audio, err := tts.Open(ctx, "Hello, world!")
if err != nil {
	log.Fatal(err)
}
defer audio.Close()
_, err = io.Copy(w, audio)
```

With `WithFolder`, the audio of every text is kept in the folder under the name `<fingerprint>_<md5 of the text>.<format>`, and a text spoken before is served from its file instead of the engine. The fingerprint is the engine's. For the Google engine it is the language code, so `en_<md5>.mp3` is exactly the name v1 wrote, and a folder filled by v1 keeps serving hits (`en-slow_<md5>.mp3` with `WithSlow`). For the OpenAI engine it holds the model, the voice and the speed, `openai-tts-1-nova-1_<md5>.mp3`, so the same sentence in another voice gets a file of its own. The folder is created with the first file. A file appears under its final name only once its audio has been read to the end, so an interrupted download or playback leaves nothing behind, and a failed download never leaves an error page saved as audio.

```go
tts, err := htgotts.New(google.New(voices.English), htgotts.WithFolder("audio"))
```

`Save` writes the audio of a text to a path of your choice, with or without a cache folder. The parent folder is created when needed, the engine's extension is added when the path has none, and the file is written atomically:

```go
path, err := tts.Save(ctx, "Hello, world!", "out/hello") // out/hello.mp3
if err != nil {
	log.Fatal(err)
}
err = tts.PlayFile(ctx, path)
```

`Play` plays audio you already have, in a format the player understands:

```go
err = tts.Play(ctx, bytes.NewReader(mp3))
```

## Errors

Errors are wrapped, so `errors.Is` and `errors.As` work on them, and the errors of the engine and the player come through `Speech` unchanged.

```go
err := tts.Speak(ctx, text)

var httpErr *engine.HTTPError
switch {
case err == nil:
	// spoken
case errors.Is(err, engine.ErrEmptyText):
	// the text was empty or white space only
case errors.As(err, &httpErr):
	// the speech service answered with something other than audio:
	// httpErr.StatusCode, httpErr.URL (without the query) and the beginning
	// of the answer in httpErr.Message
case errors.Is(err, player.ErrPlayerNotFound):
	// mplayer, or the program of your player.Command, is not installed
case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
	// the context ended the download or the playback
default:
	// no network, a file system error, or the player's exit error
}
```

`New` fails with `htgotts.ErrNilEngine`, `htgotts.ErrNilPlayer` or `htgotts.ErrEmptyFolder`, and `Save` with `htgotts.ErrEmptyPath`. The native player reports audio that is not MP3 with `native.ErrUnsupportedFormat` and a second sample rate in the same process with `native.ErrSampleRate`. An HTTP status other than 200, or an HTML page where audio was expected, is an `*engine.HTTPError`, not a corrupt file. Without a network connection, the error is the HTTP client's, wrapped.

## Languages

The Google Translate engine takes one of the `voices` constants, a typed `voices.Language`; the OpenAI engine speaks any language and takes no language setting. British English is `voices.EnglishUK`, the code `en-GB` that the endpoint expects (v1 sent `en-UK`), and Norwegian is `no`, the code the endpoint accepts in place of `nb`. A code that the list lacks is `voices.Language("xx")`.

| Constant | Code | Constant | Code | Constant | Code |
|---|---|---|---|---|---|
| `voices.Afrikaans` | `af` | `voices.German` | `de` | `voices.Polish` | `pl` |
| `voices.Albanian` | `sq` | `voices.Greek` | `el` | `voices.Portuguese` | `pt` |
| `voices.Arabic` | `ar` | `voices.Gujarati` | `gu` | `voices.Romanian` | `ro` |
| `voices.Armenian` | `hy` | `voices.Hindi` | `hi` | `voices.Russian` | `ru` |
| `voices.Bengali` | `bn` | `voices.Hungarian` | `hu` | `voices.Serbian` | `sr` |
| `voices.Bosnian` | `bs` | `voices.Icelandic` | `is` | `voices.Sinhala` | `si` |
| `voices.Bulgarian` | `bg` | `voices.Indonesian` | `id` | `voices.Slovak` | `sk` |
| `voices.Burmese` | `my` | `voices.Italian` | `it` | `voices.Spanish` | `es` |
| `voices.Catalan` | `ca` | `voices.Japanese` | `ja` | `voices.Sundanese` | `su` |
| `voices.Chinese` | `zh` | `voices.Javanese` | `jv` | `voices.Swahili` | `sw` |
| `voices.Croatian` | `hr` | `voices.Kannada` | `kn` | `voices.Swedish` | `sv` |
| `voices.Czech` | `cs` | `voices.Khmer` | `km` | `voices.Tagalog` | `tl` |
| `voices.Danish` | `da` | `voices.Korean` | `ko` | `voices.Tamil` | `ta` |
| `voices.Dutch` | `nl` | `voices.Latin` | `la` | `voices.Telugu` | `te` |
| `voices.English` | `en` | `voices.Latvian` | `lv` | `voices.Thai` | `th` |
| `voices.EnglishAU` | `en-AU` | `voices.Macedonian` | `mk` | `voices.Turkish` | `tr` |
| `voices.EnglishUK` | `en-GB` | `voices.Malay` | `ms` | `voices.Ukrainian` | `uk` |
| `voices.Esperanto` | `eo` | `voices.Malayalam` | `ml` | `voices.Urdu` | `ur` |
| `voices.Estonian` | `et` | `voices.Marathi` | `mr` | `voices.Vietnamese` | `vi` |
| `voices.Finnish` | `fi` | `voices.Nepali` | `ne` | `voices.Welsh` | `cy` |
| `voices.French` | `fr` | `voices.Norwegian` | `no` | | |

## Limitations

- The Google Translate endpoint is unofficial and undocumented: it may change, break or rate-limit without notice, and nothing in this module can prevent that. It offers no choice of voice, only the language and the slow mode; voices are an OpenAI feature.
- Text is spoken in pieces: the Google engine fetches about a hundred characters per request, the OpenAI engine 4096. The audio of the pieces is simply concatenated, which mplayer, ffplay and the native player play through, with a short pause audible at a boundary now and then. The Google engine produces 24 kHz MP3 only, and the OpenAI engine's container formats should be kept within one request.
- The native player plays MP3 only, at one sample rate per process, and needs the ALSA shared library on Linux at run time.
- Every engine of the module except `engine/command` needs network access, and the OpenAI engine costs money per request.
- `player.FFplay()` has not been checked against a running ffplay.

## Docker

The whole module builds without cgo, so the build stage needs no C compiler and no ALSA headers. What the run stage needs depends on the player: mplayer for the default player, the ALSA shared library for the native player, nothing for a program that only saves or synthesizes speech. Playing sound from a container also needs the host's sound device, for example with `--device /dev/snd`.

```dockerfile
FROM golang:1.25 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /app ./cmd/myapp

FROM debian:bookworm-slim
# mplayer for the default player. For the native player install libasound2
# instead (libasound2t64 on Debian 13 and Ubuntu 24.04 and later); a program
# that only saves or synthesizes speech needs neither.
RUN apt-get update && apt-get install -y --no-install-recommends mplayer && rm -rf /var/lib/apt/lists/*
COPY --from=build /app /app
ENTRYPOINT ["/app"]
```

## Examples

The programs under [examples/](examples/) run from a clone of the repository and keep their audio in the `audio` folder of the current directory:

```bash
go run ./examples/speak -lang hu "Szia, világ!"
```

| Program | Shows |
|---|---|
| [speak](examples/speak/main.go) | Google Translate and mplayer, with a time limit on download and playback |
| [save](examples/save/main.go) | writing an MP3 file without playing it, so no player is needed |
| [openai](examples/openai/main.go) | the OpenAI engine with model and voice flags: a WAV saved, an MP3 played |
| [native](examples/native/main.go) | the native player speaking several sentences in one process |
| [proxy](examples/proxy/main.go) | the Google engine through a proxy, with TLS verification on or off |
| [custom-engine](examples/custom-engine/main.go) | an engine of your own that calls an HTTP speech server |
| [azure](examples/azure/main.go) | the Azure Speech engine with voice, style, rate and pitch flags: an MP3 played, or a WAV saved |
| [googlecloud](examples/googlecloud/main.go) | the Google Cloud Text-to-Speech engine with voice, rate and pitch flags: an MP3 played, or a WAV saved |
| [elevenlabs](examples/elevenlabs/main.go) | the ElevenLabs engine with voice, model, language and speed flags: an MP3 played, or saved |
| [command](examples/command/main.go) | a speech program on this machine, offline: say on macOS, espeak-ng, piper or any command with `{text}` and `{out}`; a WAV played with mplayer, or saved |

## Migrating from v1

v2 changes the import path, replaces the `Speech` struct literal with `New` and options, moves the language into the Google engine, adds a context to every call, makes the folder optional and hands players a stream instead of a file name. [MIGRATION.md](MIGRATION.md) has the table from the v1 API to the v2 API and the list of behaviour that changed; [CHANGELOG.md](CHANGELOG.md) lists every change.

## Support and contributions

If you encounter issues using htgo-tts or would like to suggest improvements to the source code, you can create an issue on the ["Issues"](https://github.com/hegedustibor/htgo-tts/issues) tab. If you'd like to contribute to the htgo-tts source code, please submit a pull request.

## License

htgo-tts is free software and is available under the MIT license. For more information, please see the [LICENSE](LICENSE) file in the source code repository.

Have fun!
