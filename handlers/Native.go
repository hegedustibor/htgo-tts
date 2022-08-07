package handlers

import (
	"bytes"
	"os"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/hajimehoshi/oto/v2"
)

type Native struct {
}

func (n *Native) Play(fileName string) error {
	// Read the mp3 file into memory
	fileBytes, err := os.ReadFile(fileName)
	if err != nil {
		return err
	}

	fileBytesReader := bytes.NewReader(fileBytes)

	// Decode file
	decodedMp3, err := mp3.NewDecoder(fileBytesReader)
	if err != nil {
		return err
	}

	numOfChannels := 2
	audioBitDepth := 2

	otoCtx, readyChan, err := oto.NewContext(decodedMp3.SampleRate(), numOfChannels, audioBitDepth)
	if err != nil {
		return err
	}
	<-readyChan

	player := otoCtx.NewPlayer(decodedMp3)

	player.Play()

	for player.IsPlaying() {
		time.Sleep(time.Millisecond)
	}

	return player.Close()
}
