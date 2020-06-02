package main

import htgotts ".."

func main() {
	speech := htgotts.Speech{Folder: "audio", Language: "en"}
	speech.Speak("This is a test")
}
