package player_test

import (
	"bytes"
	"context"
	"fmt"

	"github.com/hegedustibor/htgo-tts/v2/player"
)

// Any program that plays audio from its standard input can be a player; this
// one is SoX.
func ExampleCommand() {
	var mp3 []byte // the synthesized speech, for example from an engine
	sox := &player.Command{Name: "play", Args: []string{"-q", "-t", "mp3", "-"}}
	if err := sox.Play(context.Background(), bytes.NewReader(mp3)); err != nil {
		fmt.Println(err)
	}
}
