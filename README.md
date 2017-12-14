# htgo-tts

###Requirements:
- wget
- mplayer

###Install
go get "github.com/hegedustibor/htgo-tts"

###Update
go get -u "github.com/hegedustibor/htgo-tts"

###Import
import "github.com/hegedustibor/htgo-tts"

###Use
speech := htgotts.Speech{Folder: "audio", Language: "hu"}
speech.Speak("Your sentence.")