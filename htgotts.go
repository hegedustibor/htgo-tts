package htgotts

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/hegedustibor/htgo-tts/handlers"
)

/**
 * Required:
 * - mplayer
 *
 * Use:
 *
 * speech := htgotts.Speech{Folder: "audio", Language: "en", Handler: MPlayer}
 */

// Speech struct
type Speech struct {
	Folder   string
	Language string
	Handler  handlers.PlayerInterface
}

// Speak downloads speech and plays it using mplayer
func (speech *Speech) Speak(text string) error {

	var err error
	generatedHashName := speech.generateHashName(text)
	fileName := speech.Folder + "/" + generatedHashName + ".mp3"

	if err = speech.createFolderIfNotExists(speech.Folder); err != nil {
		return err
	}
	if err = speech.downloadIfNotExists(fileName, text); err != nil {
		return err
	}

	if speech.Handler == nil {
		mplayer := handlers.MPlayer{}
		return mplayer.Play(fileName)
	}

	return speech.Handler.Play(fileName)
}

/**
 * Create the folder if does not exists.
 */
func (speech *Speech) createFolderIfNotExists(folder string) error {
	dir, err := os.Open(folder)
	if os.IsNotExist(err) {
		return os.MkdirAll(folder, 0700)
	}

	dir.Close()
	return nil
}

/**
 * Download the voice file if does not exists.
 */
func (speech *Speech) downloadIfNotExists(fileName string, text string) error {
	f, err := os.Open(fileName)
	if err != nil {
		url := fmt.Sprintf("http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=32&client=tw-ob&q=%s&tl=%s", url.QueryEscape(text), speech.Language)
		response, err := http.Get(url)
		if err != nil {
			return err
		}
		defer response.Body.Close()

		output, err := os.Create(fileName)
		if err != nil {
			return err
		}

		_, err = io.Copy(output, response.Body)
		return err
	}

	f.Close()
	return nil
}

func (speech *Speech) generateHashName(name string) string {
	hash := md5.Sum([]byte(name))
	return hex.EncodeToString(hash[:])
}
