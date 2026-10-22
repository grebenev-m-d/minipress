package service

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

func ReadingTime(text string) int {
	if m := len(strings.Fields(text)) / 200; m > 1 {
		return m
	}
	return 1
}

var uploadRef = regexp.MustCompile(`/uploads/([A-Za-z0-9_-]+\.[A-Za-z0-9]+)`)

func ExtractImageNames(markdown string) []string {
	matches := uploadRef.FindAllStringSubmatch(markdown, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, dup := seen[m[1]]; dup {
			continue
		}
		seen[m[1]] = struct{}{}
		out = append(out, m[1])
	}
	return out
}

var (
	mdNoise    = regexp.MustCompile("(?s)```.*?```|!\\[[^\\]]*\\]\\([^)]*\\)|<[^>]+>")
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdSymbols  = regexp.MustCompile("[#>*_`~|-]+")
	whitespace = regexp.MustCompile(`\s+`)
)

func Excerpt(markdown string, maxRunes int) string {
	s := mdNoise.ReplaceAllString(markdown, " ")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdSymbols.ReplaceAllString(s, " ")
	s = strings.TrimSpace(whitespace.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)[:maxRunes]
	cut := strings.TrimSpace(string(r))
	if i := strings.LastIndex(cut, " "); i > maxRunes/2 {
		cut = cut[:i]
	}
	return cut + "..."
}
