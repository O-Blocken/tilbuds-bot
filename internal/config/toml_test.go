package config

import (
	"os"
	"strings"
	"testing"
)

func TestIsQuotedValue(t *testing.T) {
	cases := map[string]bool{
		`"abc"`:  true,
		`""`:     true,
		`"a\"b"`: true,  // escaped quote inside, real terminator at the end
		`"a\\"`:  true,  // escaped backslash, then a real terminator
		`"a\"`:   false, // the only closing quote is escaped
		`"a\\\"`: false, // odd number of trailing backslashes
		`abc`:    false,
		`"`:      false,
	}
	for input, want := range cases {
		if got := isQuotedValue(input); got != want {
			t.Errorf("isQuotedValue(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestParseTomlStringKeepsUnterminatedValue guards against silently unescaping
// a value whose closing quote is escaped: the raw text must be preserved rather
// than losing a trailing backslash.
func TestParseTomlStringKeepsUnterminatedValue(t *testing.T) {
	raw := `"trailing\"`
	if got := parseTomlString(raw); got != raw {
		t.Errorf("parseTomlString(%q) = %q, want the raw text", raw, got)
	}

	// Complete strings still unescape as before.
	if got := parseTomlString(`"a\tb"`); got != "a\tb" {
		t.Errorf("parseTomlString escaped = %q", got)
	}
	if got := parseTomlString(`"a\\"`); got != `a\` {
		t.Errorf("parseTomlString escaped backslash = %q", got)
	}
}

// TestLoadSurvivesEscapedQuoteInConfig covers the whole path: a config file with
// an unterminated string must still load and must not corrupt other keys.
func TestLoadSurvivesEscapedQuoteInConfig(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	contents := strings.Join([]string{
		"[bot]",
		`last_week = "abc\"`,
		"channel_id = 77",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	config, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.ChannelID != 77 {
		t.Errorf("ChannelID = %d, want 77", config.ChannelID)
	}
	if config.LastWeek != `"abc\"` {
		t.Errorf("LastWeek = %q, want the raw value preserved", config.LastWeek)
	}
}
