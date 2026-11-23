package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClip(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"aé", 2, "a"},         // é is two bytes; the cut splits it
		{"aé", 3, "aé"},        // ends exactly after it
		{"a€b", 3, "a"},        // € is three bytes
		{"a€b", 4, "a€"},       // whole
		{"a😀", 4, "a"},         // four bytes, cut after three
		{"\xffab", 2, "\xffa"}, // invalid input before the cut stays
	}

	for _, tt := range tests {
		if got := clip(tt.in, tt.n); got != tt.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}

	long := strings.Repeat("ş", 100)
	for n := range 200 {
		if got := clip(long, n); !utf8.ValidString(got) || len(got) > n {
			t.Fatalf("clip(n=%d) = %q", n, got)
		}
		if got := clipStart(long, n); !utf8.ValidString(got) || len(got) > n {
			t.Fatalf("clipStart(n=%d) = %q", n, got)
		}
	}
}
