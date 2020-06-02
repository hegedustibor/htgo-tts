# htgo-tts
[https://hegedustibor.github.io/htgo-tts/](https://hegedustibor.github.io/htgo-tts/)

### Install
```
go get "github.com/hegedustibor/htgo-tts"
go get "github.com/hajimehoshi/go-mp3"
go get "github.com/hajimehoshi/oto"
```

#### Setup

If you're using Linux see https://github.com/hajimehoshi/oto for setup instructions.

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
speech := htgotts.Speech{Folder: "audio", Language: "en"}
speech.Speak("Your sentence.")
```
Or run the example file via
``` 
go run example/main.go
```