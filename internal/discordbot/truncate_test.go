package discordbot

import (
	"strings"
	"testing"
)

func TestTruncateForDiscordKeepsShortContent(t *testing.T) {
	content := "Kun administratorer kan bruge denne kommando."
	if got := truncateForDiscord(content); got != content {
		t.Fatalf("truncateForDiscord() changed short content: %q", got)
	}
}

func TestTruncateForDiscordClampsLongContent(t *testing.T) {
	// A concept listing is the realistic case: one line per concept.
	var builder strings.Builder
	for i := 0; i < 400; i++ {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("• koncept-")
		builder.WriteString(strings.Repeat("x", 10))
	}
	content := builder.String()
	if utf16Length(content) <= discordContentLimit {
		t.Fatal("test fixture is not long enough")
	}

	got := truncateForDiscord(content)
	if length := utf16Length(got); length > discordContentLimit {
		t.Fatalf("truncated length = %d, want <= %d", length, discordContentLimit)
	}
	if !strings.HasSuffix(got, truncationSuffix) {
		t.Errorf("truncated content must be marked, got suffix %q", got[len(got)-40:])
	}
	// It must be a prefix of the original: no content is invented.
	head := strings.TrimSuffix(got, truncationSuffix)
	if !strings.HasPrefix(content, head) {
		t.Error("truncated head is not a prefix of the original content")
	}
	// It must cut on a line boundary instead of mid-entry.
	if strings.HasPrefix(head, "\n") || strings.HasSuffix(head, "koncept-") {
		t.Errorf("cut did not land on a line boundary: %q", head[len(head)-30:])
	}
}

func TestTruncateForDiscordCountsUTF16Units(t *testing.T) {
	// U+1F600 is one rune but two UTF-16 code units, which is how Discord
	// measures the limit; a rune-based clamp would overshoot.
	content := strings.Repeat("\U0001F600", 1200)
	if utf16Length(content) <= discordContentLimit {
		t.Fatalf("fixture length = %d, want > %d", utf16Length(content), discordContentLimit)
	}
	got := truncateForDiscord(content)
	if length := utf16Length(got); length > discordContentLimit {
		t.Fatalf("truncated length = %d UTF-16 units, want <= %d", length, discordContentLimit)
	}
}

func TestTruncateForDiscordLimitEdgeCases(t *testing.T) {
	if got := truncateForDiscordLimit("short", 100); got != "short" {
		t.Errorf("limit above the content changed it: %q", got)
	}
	// A limit smaller than the suffix cannot keep any content: the marker is
	// still better than an empty or over-long message.
	if got := truncateForDiscordLimit("abcdef", 5); utf16Length(got) > len(truncationSuffix) {
		t.Errorf("tiny limit produced %q", got)
	}
	// No newline available: cut on a rune boundary, never inside a rune.
	got := truncateForDiscordLimit(strings.Repeat("ø", 3000), 100)
	if utf16Length(got) > 100 {
		t.Errorf("no-newline truncation exceeded the limit: %d", utf16Length(got))
	}
	for _, r := range strings.TrimSuffix(got, truncationSuffix) {
		if r != 'ø' {
			t.Fatalf("truncation split a rune: %q", r)
		}
	}
}

func TestUTF16Length(t *testing.T) {
	cases := map[string]int{
		"":           0,
		"abc":        3,
		"æbleskiver": 10,
		"✅":          1, // U+2705 is BMP
		"\U0001F600": 2, // non-BMP emoji
	}
	for input, want := range cases {
		if got := utf16Length(input); got != want {
			t.Errorf("utf16Length(%q) = %d, want %d", input, got, want)
		}
	}
}
