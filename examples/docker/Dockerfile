# An example image for programs built on htgo-tts, in two stages: the build
# stage compiles the save and speak programs of the examples without cgo,
# and the run stage carries the static binaries, the CA certificates for the
# engines' TLS connections and the player chosen at build time, nothing else.
# For a program of your own, replace the two go build lines with yours.
#
# The build context is the root of the repository, so build from there:
#
#   docker build -f examples/docker/Dockerfile -t htgo-tts-example .
#
# Without arguments the image prints the usage of save, which writes speech
# into the /audio volume without playing it:
#
#   docker run --rm -v "$PWD/audio:/audio" htgo-tts-example -lang hu "Szia, világ!"
#
# To play speech instead, build the image with mplayer and give the
# container the host's sound device:
#
#   docker build -f examples/docker/Dockerfile --build-arg PLAYER=mplayer -t htgo-tts-example .
#   docker run --rm --device /dev/snd --entrypoint speak htgo-tts-example -lang hu "Szia, világ!"
#
# README.md next to this file has the details.

# The module builds without cgo, so this stage needs no C compiler and no
# ALSA headers, and the binaries are static: they run on any Linux image.
FROM golang:1.25 AS build
WORKDIR /src

# The dependencies first, in a layer that is reused until go.mod or go.sum
# changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/save ./examples/save \
 && CGO_ENABLED=0 go build -o /out/speak ./examples/speak

FROM debian:bookworm-slim

# PLAYER chooses what this stage installs for the player of the program:
#   none     nothing, for a program that only saves or synthesizes speech,
#            such as save (the default);
#   mplayer  mplayer, the default player of htgo-tts, which speak uses;
#   native   the ALSA shared library for a program that plays with
#            player/native: libasound2 on Debian 12, which this image is,
#            libasound2t64 on Debian 13 and Ubuntu 24.04 and later.
# The CA certificates are needed in every case: the engines that call a web
# service verify its TLS certificate against them, and the slim image has
# none.
ARG PLAYER=none
RUN set -eu; \
    case "$PLAYER" in \
        none) player='' ;; \
        mplayer) player='mplayer' ;; \
        native) player='libasound2' ;; \
        *) echo "PLAYER must be none, mplayer or native, not '$PLAYER'" >&2; exit 1 ;; \
    esac; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates $player; \
    rm -rf /var/lib/apt/lists/*

COPY --from=build /out/save /out/speak /usr/local/bin/

# The programs run as an unprivileged user, a member of the audio group so
# that it may open the host's sound device. They keep their audio in the
# audio folder of the working directory, which with / as the working
# directory is the /audio volume: a folder of the host mounted there keeps
# the files.
RUN useradd --uid 1000 --user-group --groups audio --create-home htgo \
 && mkdir /audio && chown htgo:htgo /audio
USER htgo
WORKDIR /
VOLUME /audio

# save by default, speak with --entrypoint speak; both print their usage
# with --help, so the image starts without any argument.
ENTRYPOINT ["save"]
CMD ["--help"]
