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

func TestSpeech_CreateSpeechBuff(t *testing.T) {
	speech := Speech{Folder: "audio", Language: voices.Chinese}
	_, err := speech.CreateSpeechBuff("AVAX的代幣經濟學在當今數位化的世界中，區塊鏈技術和代幣經濟學已成為金融創新的前沿。Avalanche（AVAX）作為一個領先的區塊鏈平台，其獨特的代幣經濟模型和共識機制引起了業界的廣泛關注。以下是對AVAX供應與分配、使用場景以及共識機制的深入探討。AVAX設定了7.2億枚的代幣供應上限，這個固定的數量是為了避免通貨膨脹，為持有者提供穩定的價值。AVAX的發行結合了初始發行和持續發行兩種方式", "test")
	if err != nil {
		t.Fatalf("CreateSpeechBuff fail %v", err)
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
