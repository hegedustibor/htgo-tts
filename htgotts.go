package htgotts

import (
	"bytes"
	"crypto/md5"
	"crypto/tls"
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
	Proxy    string
	Handler  handlers.PlayerInterface
}

// Creates a speech file with a given name
func (speech *Speech) CreateSpeechFile(text string, fileName string) (string, error) {
	var err error

	f := speech.Folder + "/" + fileName + ".mp3"
	if err = speech.createFolderIfNotExists(speech.Folder); err != nil {
		return "", err
	}

	if err = speech.downloadIfNotExists(f, text); err != nil {
		return "", err
	}

	return f, nil
}

// Creates a speech message with a given name
func (speech *Speech) CreateSpeechBuff(text string, fileName string) (io.Reader, error) {
	data := []rune(text)

	chunkSize := len(data)
	if len(data) > 32 {
		chunkSize = 32
	}

	urls := make([]string, 0)
	for prev, i := 0, 0; i < len(data); i++ {
		if i%chunkSize == 0 && i != 0 {
			chunk := string(data[prev:i])
			url := fmt.Sprintf("http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=%d&client=tw-ob&q=%s&tl=%s", chunkSize, url.QueryEscape(chunk), speech.Language)
			urls = append(urls, url)
			prev = i
		} else if i == len(data)-1 {
			chunk := string(data[prev:])
			url := fmt.Sprintf("http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=%d&client=tw-ob&q=%s&tl=%s", chunkSize, url.QueryEscape(chunk), speech.Language)
			urls = append(urls, url)
			prev = i
		}
	}

	buf := new(bytes.Buffer)
	for _, url := range urls {
		r, err := http.Get(url)
		if err != nil {
			return nil, err
		}

		_, err = buf.ReadFrom(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body.Close()
	}

	f := speech.Folder + "/" + fileName + ".mp3"
	output, err := os.Create(f)
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(output, buf)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

// Plays an existent .mp3 file
func (speech *Speech) PlaySpeechFile(fileName string) error {
	if speech.Handler == nil {
		mplayer := handlers.MPlayer{}
		return mplayer.Play(fileName)
	}

	return speech.Handler.Play(fileName)
}

// Speak downloads speech and plays it using mplayer
func (speech *Speech) Speak(text string) error {

	var err error
	generatedHashName := speech.generateHashName(text)

	fileName, err := speech.CreateSpeechFile(text, generatedHashName)
	if err != nil {
		return err
	}

	return speech.PlaySpeechFile(fileName)
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
		dlURL := fmt.Sprintf("http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=32&client=tw-ob&q=%s&tl=%s", url.QueryEscape(text), speech.Language)

		response, err := speech.urlResponse(dlURL)

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
	return fmt.Sprintf("%s_%s", speech.Language, hex.EncodeToString(hash[:]))
}

func (speech *Speech) urlResponse(dlUrl string) (resp *http.Response, err error) {
	var (
		response *http.Response
	)

	if speech.Proxy != "" {
		var proxyURL *url.URL
		proxyURL, err = url.Parse(speech.Proxy)

		if err != nil {
			return response, err
		}

		httpCli := &http.Client{Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}

		return httpCli.Get(dlUrl)
	}

	return http.Get(dlUrl)
}
