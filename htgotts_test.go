package htgotts

import (
	"github.com/hegedustibor/htgo-tts/handlers"
	"github.com/hegedustibor/htgo-tts/voices"

	"testing"
)

func TestSpeech_Speak(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.English}
	speech.Speak("Test")
}

func TestSpeech_Speak_MPlayer_Handler(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.English, Handler: &handlers.MPlayer{}}
	speech.Speak("Test")
}

func TestSpeech_Speak_voice_UkEnglish(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.EnglishUK}
	speech.Speak("Lancaster")
}

func TestSpeech_Speak_voice_Japanese(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.Japanese}
	speech.Speak("Test")
}

func TestSpeech_CreateSpeechFile(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.English}
	_, err := speech.CreateSpeechFile("Test", "testfilename")
	if err != nil {
		t.Fatalf("CreateSpeechFile fail %v", err)
	}
}

func TestSpeech_(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.English}
	f, err := speech.CreateSpeechFile("Test", "testplay")
	if err != nil {
		t.Fatalf("CreateSpeechFile fail %v", err)
	}
	speech.PlaySpeechFile(f)
}
