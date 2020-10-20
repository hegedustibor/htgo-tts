package htgotts

import "testing"
import "github.com/hegedustibor/htgo-tts/handlers"

func TestSpeech_Speak(t *testing.T) {
	speech := Speech{Folder: "audio", Language: "en"}
	speech.Speak("Test")
}

func TestSpeech_Speak_MPlayer_Handler(t *testing.T) {
	speech := Speech{Folder: "audio", Language: "en", Handler: &handlers.MPlayer{}}
	speech.Speak("Test")
}
