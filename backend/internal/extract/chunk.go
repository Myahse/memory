package extract

import (
	"strings"
	"unicode/utf8"
)

// Chunk splits text into overlapping chunks of roughly size characters,
// preferring paragraph, then sentence, then word boundaries.
func Chunk(text string, size, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if size <= 0 {
		size = 1200
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 6
	}
	if utf8.RuneCountInString(text) <= size {
		return []string{text}
	}

	// Split into units (paragraphs, then sentences for long paragraphs).
	var units []string
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(p) <= size {
			units = append(units, p)
			continue
		}
		units = append(units, splitLong(p, size)...)
	}

	var chunks []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, u := range units {
		if cur.Len() > 0 && utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(u)+2 > size {
			prev := cur.String()
			flush()
			if tail := tailRunes(prev, overlap); tail != "" {
				cur.WriteString(tail)
				cur.WriteString("\n\n")
			}
		}
		cur.WriteString(u)
		cur.WriteString("\n\n")
	}
	flush()
	return chunks
}

func splitLong(p string, size int) []string {
	var out []string
	var cur strings.Builder
	for _, s := range sentences(p) {
		if utf8.RuneCountInString(s) > size { // a giant "sentence": split on words
			for _, w := range strings.Fields(s) {
				if utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(w)+1 > size && cur.Len() > 0 {
					out = append(out, strings.TrimSpace(cur.String()))
					cur.Reset()
				}
				cur.WriteString(w)
				cur.WriteByte(' ')
			}
			continue
		}
		if utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(s)+1 > size && cur.Len() > 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		cur.WriteString(s)
		cur.WriteByte(' ')
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func sentences(p string) []string {
	var out []string
	start := 0
	for i, r := range p {
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			if i+1 >= len(p) || p[i+1] == ' ' || p[i+1] == '\n' {
				out = append(out, strings.TrimSpace(p[start:i+1]))
				start = i + 1
			}
		}
	}
	if start < len(p) {
		if s := strings.TrimSpace(p[start:]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tailRunes returns roughly the last n runes of s, starting at a word boundary.
func tailRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	t := string(r[len(r)-n:])
	if i := strings.IndexByte(t, ' '); i >= 0 {
		t = t[i+1:]
	}
	return t
}
