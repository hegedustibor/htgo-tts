// Package chunk splits text into pieces that fit the length limit of a
// speech endpoint, cutting at sentence and clause boundaries where it can.
package chunk

import (
	"strings"
	"unicode"
)

// Split breaks text into chunks of at most max runes each.
//
// It prefers to cut after a sentence-ending mark, then after a comma-like
// mark, then at white space, and only as a last resort between two arbitrary
// runes. Marks that are followed by white space win over marks inside a word,
// so a decimal point does not end a chunk when a space is available; scripts
// written without spaces, such as Chinese or Japanese, are cut at their own
// sentence and clause marks.
//
// Chunks carry no leading or trailing white space and are never empty. Text
// that is empty or contains only white space yields nil. A max below one
// disables the limit, and the whole text comes back as a single chunk.
func Split(text string, max int) []string {
	rest := []rune(strings.TrimSpace(text))
	if len(rest) == 0 {
		return nil
	}
	if max < 1 {
		return []string{string(rest)}
	}

	var chunks []string
	for len(rest) > max {
		cut := cutPoint(rest, max)
		chunks = append(chunks, strings.TrimRightFunc(string(rest[:cut]), unicode.IsSpace))
		rest = trimLeft(rest[cut:])
	}
	if len(rest) > 0 {
		chunks = append(chunks, string(rest))
	}
	return chunks
}

// cutPoint returns the length of the next chunk of rest, between 1 and max.
// rest is longer than max, so rest[max] always exists.
func cutPoint(rest []rune, max int) int {
	rules := []func(prev, next rune) bool{
		func(prev, next rune) bool { return isSentenceEnd(prev) && unicode.IsSpace(next) },
		func(prev, next rune) bool { return isClauseEnd(prev) && unicode.IsSpace(next) },
		func(_, next rune) bool { return unicode.IsSpace(next) },
		func(prev, next rune) bool { return isSentenceEnd(prev) && !isSentenceEnd(next) },
		func(prev, next rune) bool { return isClauseEnd(prev) && !isClauseEnd(next) },
	}
	for _, ok := range rules {
		for i := max; i >= 1; i-- {
			if ok(rest[i-1], rest[i]) {
				return i
			}
		}
	}
	return max
}

func trimLeft(r []rune) []rune {
	for len(r) > 0 && unicode.IsSpace(r[0]) {
		r = r[1:]
	}
	return r
}

func isSentenceEnd(r rune) bool {
	return strings.ContainsRune(".!?…。！？", r)
}

func isClauseEnd(r rune) bool {
	return strings.ContainsRune(",;:，、；：", r)
}
