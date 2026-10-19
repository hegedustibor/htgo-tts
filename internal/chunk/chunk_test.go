package chunk

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		text string
		max  int
		want []string
	}{
		{
			name: "short text",
			text: "Hello, world!",
			max:  100,
			want: []string{"Hello, world!"},
		},
		{
			name: "empty text",
			text: "",
			max:  100,
			want: nil,
		},
		{
			name: "only white space",
			text: " \t\n\r ",
			max:  100,
			want: nil,
		},
		{
			name: "surrounding white space is trimmed",
			text: "  Szia!  \n",
			max:  100,
			want: []string{"Szia!"},
		},
		{
			name: "exactly 100 runes",
			text: strings.Repeat("ű", 100),
			max:  100,
			want: []string{strings.Repeat("ű", 100)},
		},
		{
			name: "101 runes",
			text: strings.Repeat("ű", 101),
			max:  100,
			want: []string{strings.Repeat("ű", 100), "ű"},
		},
		{
			name: "33 runes fit in 100",
			text: strings.Repeat("a", 33),
			max:  100,
			want: []string{strings.Repeat("a", 33)},
		},
		{
			name: "33 runes with a maximum of 32 keep the last rune",
			text: strings.Repeat("a", 32) + "b",
			max:  32,
			want: []string{strings.Repeat("a", 32), "b"},
		},
		{
			name: "4097 runes with a maximum of 4096",
			text: strings.Repeat("x", 4097),
			max:  4096,
			want: []string{strings.Repeat("x", 4096), "x"},
		},
		{
			name: "long text without marks or spaces",
			text: strings.Repeat("é", 250),
			max:  100,
			want: []string{strings.Repeat("é", 100), strings.Repeat("é", 100), strings.Repeat("é", 50)},
		},
		{
			name: "several sentences",
			text: "Első mondat. Második mondat! Harmadik mondat? Negyedik mondat…",
			max:  30,
			want: []string{"Első mondat. Második mondat!", "Harmadik mondat?", "Negyedik mondat…"},
		},
		{
			name: "clause marks",
			text: "alma, körte, szilva, barack",
			max:  14,
			want: []string{"alma, körte,", "szilva, barack"},
		},
		{
			name: "spaces",
			text: "one two three four",
			max:  9,
			want: []string{"one two", "three", "four"},
		},
		{
			name: "runs of spaces",
			text: "one   two   three",
			max:  6,
			want: []string{"one", "two", "three"},
		},
		{
			name: "decimal points do not end a chunk",
			text: "A pi 3.14, az e 2.71 és a gyök 1.41",
			max:  12,
			want: []string{"A pi 3.14,", "az e 2.71 és", "a gyök 1.41"},
		},
		{
			name: "ellipsis stays together",
			text: "Várj... mi? Igen",
			max:  8,
			want: []string{"Várj...", "mi? Igen"},
		},
		{
			name: "CJK sentences",
			text: "今天天气很好。我们去公园吧！你觉得怎么样？",
			max:  8,
			want: []string{"今天天气很好。", "我们去公园吧！", "你觉得怎么样？"},
		},
		{
			name: "CJK clause marks",
			text: "我喜欢苹果，香蕉和橙子",
			max:  7,
			want: []string{"我喜欢苹果，", "香蕉和橙子"},
		},
		{
			name: "CJK without marks",
			text: strings.Repeat("好", 10),
			max:  4,
			want: []string{"好好好好", "好好好好", "好好"},
		},
		{
			name: "a maximum below one disables the limit",
			text: strings.Repeat("a", 500),
			max:  0,
			want: []string{strings.Repeat("a", 500)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.text, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("Split returned %d chunks %q, want %d chunks %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("chunk %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
			checkChunks(t, tt.text, tt.max, got)
		})
	}
}

// checkChunks verifies the properties that hold for every split: no chunk is
// empty, padded or longer than max, and together the chunks carry every
// non-space rune of the input in order.
func checkChunks(t *testing.T, text string, max int, chunks []string) {
	t.Helper()
	for i, c := range chunks {
		if c == "" {
			t.Errorf("chunk %d is empty", i)
		}
		if strings.TrimSpace(c) != c {
			t.Errorf("chunk %d %q has surrounding white space", i, c)
		}
		if n := utf8.RuneCountInString(c); max >= 1 && n > max {
			t.Errorf("chunk %d has %d runes, more than %d", i, n, max)
		}
	}
	if got, want := stripSpace(strings.Join(chunks, "")), stripSpace(text); got != want {
		t.Errorf("the chunks lose or reorder runes:\n got %q\nwant %q", got, want)
	}
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
