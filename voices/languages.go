// Package voices lists the languages that the Google Translate text-to-speech
// engine can speak.
//
// Every constant is a Language: the code that the Google Translate
// translate_tts endpoint expects in its tl parameter. Pass one of them wherever
// a Language is required instead of a plain string, so that the compiler
// catches typos. The list follows the languages that Google Translate supports.
//
// Full tables of language codes:
//   - http://www.lingoes.net/en/translator/langcode.htm
//   - https://www.science.co.il/language/Codes.php
//   - https://cloud.google.com/text-to-speech/docs/voices
package voices

// Language is a language code accepted by the Google Translate text-to-speech
// endpoint, for example "en" or "en-GB".
type Language string

// String returns the raw language code.
func (l Language) String() string { return string(l) }

const (
	English    Language = "en"    // English US
	EnglishUK  Language = "en-GB" // the Google endpoint expects the BCP 47 code, not "en-UK"
	EnglishAU  Language = "en-AU"
	Japanese   Language = "ja"
	German     Language = "de"
	Spanish    Language = "es"
	Russian    Language = "ru"
	Arabic     Language = "ar"
	Bengali    Language = "bn"
	Czech      Language = "cs"
	Danish     Language = "da"
	Dutch      Language = "nl"
	Finnish    Language = "fi"
	Greek      Language = "el"
	Hindi      Language = "hi"
	Hungarian  Language = "hu"
	Indonesian Language = "id"
	Khmer      Language = "km"
	Latin      Language = "la"
	Italian    Language = "it"
	Norwegian  Language = "no" // the Google endpoint accepts "no", not the ISO 639 "nb" (Bokmål)
	Polish     Language = "pl"
	Slovak     Language = "sk"
	Swedish    Language = "sv"
	Thai       Language = "th"
	Turkish    Language = "tr"
	Ukrainian  Language = "uk"
	Vietnamese Language = "vi"
	Afrikaans  Language = "af"
	Bulgarian  Language = "bg"
	Catalan    Language = "ca"
	Welsh      Language = "cy"
	Estonian   Language = "et"
	French     Language = "fr"
	Gujarati   Language = "gu"
	Icelandic  Language = "is"
	Javanese   Language = "jv"
	Kannada    Language = "kn"
	Korean     Language = "ko"
	Latvian    Language = "lv"
	Malayalam  Language = "ml"
	Marathi    Language = "mr"
	Malay      Language = "ms"
	Nepali     Language = "ne"
	Portuguese Language = "pt"
	Romanian   Language = "ro"
	Sinhala    Language = "si"
	Serbian    Language = "sr"
	Sundanese  Language = "su"
	Tamil      Language = "ta"
	Telugu     Language = "te"
	Tagalog    Language = "tl"
	Urdu       Language = "ur"
	Chinese    Language = "zh"
	Swahili    Language = "sw"
	Albanian   Language = "sq"
	Burmese    Language = "my" // not with english
	Macedonian Language = "mk"
	Armenian   Language = "hy"
	Croatian   Language = "hr"
	Esperanto  Language = "eo"
	Bosnian    Language = "bs"
)
