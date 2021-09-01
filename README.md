# htgo-tts
[https://hegedustibor.github.io/htgo-tts/](https://hegedustibor.github.io/htgo-tts/)

### Requirement:
- mplayer (optional)

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
import "github.com/hegedustibor/htgo-tts/voices"
```

### Use
```go
speech := htgotts.Speech{Folder: "audio", Language: voices.English}
speech.Speak("Your sentence.")
```

### Use with Handlers
```go
import (
    htgotts "github.com/hegedustibor/htgo-tts"
    handlers "github.com/hegedustibor/htgo-tts/handlers"
    voices "github.com/hegedustibor/htgo-tts/voices"
)

speech := htgotts.Speech{Folder: "audio", Language: voices.English, Handler: &handlers.MPlayer{}}
speech.Speak("Your sentence.")
```

Have Fun!