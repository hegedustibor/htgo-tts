package htgotts

import "os/exec"
import "net/url"
import "os"
import "fmt"

/**
 * Required:
 * 	- wget
 *  - mplayer
 * 
 * Use:
 * 
 * speech := htgotts.Speech{Folder: "audio", Language: "en"}
 */

type Speech struct {
	Folder string
    Language string
}

func (speech *Speech) Speak(text string) {

	fileName := speech.Folder + "/" + text + ".mp3"

	speech.createFolderIfNotExists(speech.Folder)
	speech.downloadIfNotExists(fileName, text)
	speech.play(fileName)
	
}

/**
 * Create the folder if does not exists.
 */
func (speech *Speech) createFolderIfNotExists(folder string) {
	dir, err := os.Open(folder)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(folder, 0700)
	}
	defer dir.Close()
}

/**
 * Download the voice file if does not exists.
 */
func (speech *Speech) downloadIfNotExists(fileName string, text string) {
	f, err := os.Open(fileName)
	if os.IsNotExist(err) {
		fmt.Println("\n--- Downloading voice...")
		url := "http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=32&client=tw-ob&q=" + url.QueryEscape(text) + "&tl=" + speech.Language
		download:= exec.Command("wget", "-q", "-U", "Mozilla", "-O", fileName, url)
		download.Run()
		fmt.Println("--- Voice is downloaded.\n")
	}
	defer f.Close()
}

/**
 * Play voice file.
 */
func (speech *Speech) play(fileName string) {
	mplayer := exec.Command("mplayer", "-cache", "8092", "-", fileName)
	mplayer.Run()
}
