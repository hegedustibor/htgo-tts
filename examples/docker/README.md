# Docker

[Dockerfile](Dockerfile) builds an image of the [save](../save/main.go) and [speak](../speak/main.go) programs in two stages, to copy for a program of your own. The build stage compiles them without cgo, so it needs no C compiler and no ALSA headers; the run stage, `debian:bookworm-slim`, carries the static binaries, the CA certificates for the engines' TLS connections and the player chosen at build time, nothing else. CI builds the image on every change.

## Build

The build context is the root of the repository, so build from there; the root `.dockerignore` keeps the `.git` and `audio` folders out of the context:

```bash
docker build -f examples/docker/Dockerfile -t htgo-tts-example .
```

The `PLAYER` build argument says what the run stage installs for the player:

| `PLAYER` | Installs | For |
|---|---|---|
| `none`, the default | nothing | a program that only saves or synthesizes speech, such as `save` |
| `mplayer` | mplayer | the default player of htgo-tts, which `speak` uses |
| `native` | libasound2, the ALSA shared library | a program that plays with `player/native`; the package is `libasound2t64` on Debian 13 and Ubuntu 24.04 and later |

## Save speech

Without arguments the image prints the usage of `save`. The programs keep their audio in the `/audio` volume; mount a folder of the host there to keep the files, and create the folder first, because a folder that Docker creates belongs to root while the programs run as an unprivileged user with uid 1000:

```bash
mkdir -p audio
docker run --rm -v "$PWD/audio:/audio" htgo-tts-example -lang hu -out audio/szia "Szia, világ!"
```

This writes `audio/szia.mp3` on the host, just as `go run ./examples/save` would from the root of the repository. If uid 1000 may not write to your folder, add `--user "$(id -u):$(id -g)"` to the run.

## Play speech

Playing needs the mplayer variant of the image and the host's sound device, which Docker on Linux passes with `--device /dev/snd`; Docker Desktop on macOS and Windows has no sound device to pass:

```bash
docker build -f examples/docker/Dockerfile --build-arg PLAYER=mplayer -t htgo-tts-example .
docker run --rm --device /dev/snd -v "$PWD/audio:/audio" --entrypoint speak htgo-tts-example -lang hu "Szia, világ!"
```

## Network and keys

The Google Translate engine of both programs calls a web service, so the container needs network access. A program that uses one of the keyed engines takes its key from the environment: pass it with `-e OPENAI_API_KEY` (or `AZURE_SPEECH_KEY` and `AZURE_SPEECH_REGION`, `GOOGLE_CLOUD_API_KEY`, `ELEVENLABS_API_KEY`) rather than baking it into the image.

## Your own program

Copy the Dockerfile, replace the two `go build` lines with your program and keep the `PLAYER` that matches its player; the sound device is needed only when it plays.
