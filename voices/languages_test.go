package voices_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"testing"

	"github.com/hegedustibor/htgo-tts/v2/voices"
)

// languages lists every constant of the package by hand, because constants
// cannot be enumerated at run time. TestLanguagesListIsComplete checks the
// list against languages.go, so a new constant has to be added here as well.
var languages = []struct {
	name string
	lang voices.Language
}{
	{"English", voices.English},
	{"EnglishUK", voices.EnglishUK},
	{"EnglishAU", voices.EnglishAU},
	{"Japanese", voices.Japanese},
	{"German", voices.German},
	{"Spanish", voices.Spanish},
	{"Russian", voices.Russian},
	{"Arabic", voices.Arabic},
	{"Bengali", voices.Bengali},
	{"Czech", voices.Czech},
	{"Danish", voices.Danish},
	{"Dutch", voices.Dutch},
	{"Finnish", voices.Finnish},
	{"Greek", voices.Greek},
	{"Hindi", voices.Hindi},
	{"Hungarian", voices.Hungarian},
	{"Indonesian", voices.Indonesian},
	{"Khmer", voices.Khmer},
	{"Latin", voices.Latin},
	{"Italian", voices.Italian},
	{"Norwegian", voices.Norwegian},
	{"Polish", voices.Polish},
	{"Slovak", voices.Slovak},
	{"Swedish", voices.Swedish},
	{"Thai", voices.Thai},
	{"Turkish", voices.Turkish},
	{"Ukrainian", voices.Ukrainian},
	{"Vietnamese", voices.Vietnamese},
	{"Afrikaans", voices.Afrikaans},
	{"Bulgarian", voices.Bulgarian},
	{"Catalan", voices.Catalan},
	{"Welsh", voices.Welsh},
	{"Estonian", voices.Estonian},
	{"French", voices.French},
	{"Gujarati", voices.Gujarati},
	{"Icelandic", voices.Icelandic},
	{"Javanese", voices.Javanese},
	{"Kannada", voices.Kannada},
	{"Korean", voices.Korean},
	{"Latvian", voices.Latvian},
	{"Malayalam", voices.Malayalam},
	{"Marathi", voices.Marathi},
	{"Malay", voices.Malay},
	{"Nepali", voices.Nepali},
	{"Portuguese", voices.Portuguese},
	{"Romanian", voices.Romanian},
	{"Sinhala", voices.Sinhala},
	{"Serbian", voices.Serbian},
	{"Sundanese", voices.Sundanese},
	{"Tamil", voices.Tamil},
	{"Telugu", voices.Telugu},
	{"Tagalog", voices.Tagalog},
	{"Urdu", voices.Urdu},
	{"Chinese", voices.Chinese},
	{"Swahili", voices.Swahili},
	{"Albanian", voices.Albanian},
	{"Burmese", voices.Burmese},
	{"Macedonian", voices.Macedonian},
	{"Armenian", voices.Armenian},
	{"Croatian", voices.Croatian},
	{"Esperanto", voices.Esperanto},
	{"Bosnian", voices.Bosnian},
}

// codePattern is the shape of the codes the Google Translate endpoint accepts:
// a two or three letter language, optionally followed by a region.
var codePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)

func TestLanguages(t *testing.T) {
	seen := make(map[voices.Language]string, len(languages))
	for _, tt := range languages {
		t.Run(tt.name, func(t *testing.T) {
			if tt.lang == "" {
				t.Fatalf("%s is empty", tt.name)
			}
			if !codePattern.MatchString(string(tt.lang)) {
				t.Errorf("%s = %q does not match %s", tt.name, tt.lang, codePattern)
			}
			if got, want := tt.lang.String(), string(tt.lang); got != want {
				t.Errorf("%s.String() = %q, want %q", tt.name, got, want)
			}
			if other, dup := seen[tt.lang]; dup {
				t.Errorf("%s = %q duplicates %s", tt.name, tt.lang, other)
			}
			seen[tt.lang] = tt.name
		})
	}
}

func TestLanguagesListIsComplete(t *testing.T) {
	declared := declaredConstants(t, "languages.go")

	listed := make(map[string]bool, len(languages))
	for _, tt := range languages {
		if listed[tt.name] {
			t.Errorf("%s is listed twice", tt.name)
		}
		listed[tt.name] = true
	}
	for _, name := range declared {
		if !listed[name] {
			t.Errorf("%s is declared in languages.go but missing from the test list", name)
		}
	}
	if len(declared) != len(languages) {
		t.Errorf("languages.go declares %d constants, the test lists %d", len(declared), len(languages))
	}
}

// declaredConstants parses file and returns the names of its constants. It
// fails the test for a constant that is not declared with the Language type.
func declaredConstants(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var names []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "Language" {
				t.Errorf("%s: %s is not declared as Language", fset.Position(vs.Pos()), vs.Names[0].Name)
			}
			for _, name := range vs.Names {
				names = append(names, name.Name)
			}
		}
	}
	return names
}

func ExampleLanguage_String() {
	fmt.Println(voices.EnglishUK.String())
	// Output: en-GB
}
