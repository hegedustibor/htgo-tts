# htgo-tts

### Requirements:
- wget
- mplayer

### Install
```
go get "github.com/hegedustibor/htgo-tts"
```

### Update
```
go get -u "github.com/hegedustibor/htgo-tts"
```

### Remove
```
go clean -i "github.com/hegedustibor/htgo-tts"
```

### Import
```go
import "github.com/hegedustibor/htgo-tts"
```

### Use
```go
speech := htgotts.Speech{Folder: "audio", Language: "hu"}
speech.Speak("Your sentence.")
```
