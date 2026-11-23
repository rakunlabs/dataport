package server

import "unicode/utf8"

// clip keeps at most n bytes of s, ending on a whole character: a cut inside
// one is invalid UTF-8, which PostgreSQL refuses and JSON garbles.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	// The last character starts at most UTFMax-1 bytes before the cut; it
	// stays only when it ends before the cut.
	start := n
	for start > 0 && n-start < utf8.UTFMax && !utf8.RuneStart(s[start]) {
		start--
	}

	if _, size := utf8.DecodeRuneInString(s[start:]); start+size <= n {
		return s[:n]
	}

	return s[:start]
}

// clipStart keeps at most the last n bytes of s, starting on a whole
// character.
func clipStart(s string, n int) string {
	if len(s) <= n {
		return s
	}

	s = s[len(s)-n:]
	for i := 0; i < utf8.UTFMax && len(s) > 0 && !utf8.RuneStart(s[0]); i++ {
		s = s[1:]
	}

	return s
}
