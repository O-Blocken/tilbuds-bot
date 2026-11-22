package search

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase", "Coca-Cola", "coca cola"},
		{"collapse whitespace", "coca   cola", "coca cola"},
		{"punctuation boundaries", "Coca_Cola!flaske", "coca cola flaske"},
		{"trim", "  øl  ", "oel"},
		{"danish letters fold", "snøfler", "snoefler"},
		{"danish letters fold ø", "franskbrød med smør", "franskbroed med smoer"},
		{"accents fold", "café crème", "cafe creme"},
		{"digits kept", "1664 øl", "1664 oel"},
		{"empty", "!!!", ""},
		{"emoji stripped", "øl 🍺", "oel"},
		// The three formatting variants must agree.
		{"plan coca-cola 1", "Coca-Cola", "coca cola"},
		{"plan coca-cola 2", "COCA COLA", "coca cola"},
		{"plan coca-cola 3", "coca   cola", "coca cola"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeEquivalentFormats(t *testing.T) {
	a := Normalize("Coca-Cola")
	b := Normalize("COCA COLA")
	c := Normalize("coca   cola")
	if a != b || b != c {
		t.Fatalf("formatting variants differ: %q %q %q", a, b, c)
	}
}

func TestTokens(t *testing.T) {
	got := Tokens("Coca-Cola, 2 flasker")
	want := []string{"coca", "cola", "2", "flasker"}
	if len(got) != len(want) {
		t.Fatalf("Tokens len = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tokens[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if Tokens("!!") != nil {
		t.Fatal("Tokens on punctuation-only text should be nil")
	}
}
