package player

import (
	"context"
	"io"
)

// Discard is a [Player] that reads the audio to its end without playing it.
// It serves tests and programs that only want to download or cache speech.
// It returns ctx.Err() when ctx is done before the audio ends.
var Discard Player = discard{}

type discard struct{}

func (discard) Play(ctx context.Context, audio io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := audio.Read(buf); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
