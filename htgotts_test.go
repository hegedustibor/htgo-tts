package htgotts

import (
	"github.com/hegedustibor/htgo-tts/handlers"
	"github.com/hegedustibor/htgo-tts/voices"

	"fmt"
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

func TestSpeech_Speak_Native_Handler(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.English, Handler: &handlers.Native{}}
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

func TestSpeech_WithProxy(t *testing.T) {
	speech := Speech{
		Folder:   "audio",
		Language: voices.English,
		Proxy:    fmt.Sprintf("http://translate.google.com/translate_tts?ie=UTF-8&total=1&idx=0&textlen=32&client=tw-ob&q=%s&tl=%s", "Test", voices.English),
	}
	speech.Speak("Test")
}
