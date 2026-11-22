package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSavePreservesUnmanagedContent is the regression test for hand-edited
// config files: unknown [bot] keys, unknown sections, a preamble and comments
// must survive a bot save instead of being dropped by the generated template.
func TestSavePreservesUnmanagedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := strings.Join([]string{
		"# my deployment notes",
		"# second preamble line",
		"",
		"[bot]",
		"channel_id = 42",
		"# the operator keeps an extra key here",
		"my_custom_key = \"keep me\"",
		"keywords = [",
		"  \"chips\",",
		"]",
		"",
		"[operator]",
		"timezone = \"Europe/Copenhagen\"",
		"",
		"[[want]]",
		"id = \"cola\"",
		"include = [\"cola\"]",
		"note = \"unknown want field\"",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	if _, err := store.Update(func(config *BotConfig) {
		config.ChannelID = 43
		config.LastWeek = "week-1"
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	for _, want := range []string{
		"# my deployment notes",
		"# second preamble line",
		"# the operator keeps an extra key here",
		"my_custom_key = \"keep me\"",
		"[operator]",
		"timezone = \"Europe/Copenhagen\"",
		"unknown want field",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("saved config lost %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "channel_id = 43") {
		t.Errorf("saved config did not persist the new channel:\n%s", text)
	}

	// The unknown [bot] key must still be inside [bot], not pushed into a
	// [[want]] block or an unknown section.
	botBlock := text[strings.Index(text, "[bot]"):strings.Index(text, "[[want]]")]
	if !strings.Contains(botBlock, "my_custom_key") {
		t.Errorf("unknown [bot] key left the [bot] table:\n%s", text)
	}

	// And the file must still parse back to the values the bot wrote.
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ChannelID != 43 || reloaded.LastWeek != "week-1" {
		t.Fatalf("reloaded = %+v", reloaded)
	}
}

// TestSaveIsIdempotent guards against extras accumulating on every save.
func TestSaveIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := strings.Join([]string{
		"# preamble",
		"[bot]",
		"channel_id = 1",
		"custom = \"value\"",
		"[extra]",
		"flag = \"on\"",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	for i := 0; i < 5; i++ {
		if _, err := store.Update(func(config *BotConfig) {}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	for _, marker := range []string{"# preamble", "custom = \"value\"", "[extra]", "flag = \"on\""} {
		if got := strings.Count(text, marker); got != 1 {
			t.Errorf("%q appears %d times after repeated saves, want 1:\n%s", marker, got, text)
		}
	}
	if got := strings.Count(text, configHeader); got != 1 {
		t.Errorf("header appears %d times, want 1", got)
	}
}

// TestSaveKeepsBackup verifies the rollback copy of the previous state.
func TestSaveKeepsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[bot]\nchannel_id = 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	if _, err := store.Update(func(config *BotConfig) { config.ChannelID = 8 }); err != nil {
		t.Fatal(err)
	}

	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if !strings.Contains(string(backup), "channel_id = 7") {
		t.Errorf("backup = %q, want the previous contents", backup)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "channel_id = 8") {
		t.Errorf("current = %q, want the new contents", current)
	}
}

// TestSaveUsesTempFileAndRename verifies the write is atomic: the target is
// replaced, never truncated in place, and no temp files are left behind.
func TestSaveUsesTempFileAndRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	store := NewConfigStore(path)
	if _, err := store.Update(func(config *BotConfig) { config.ChannelID = 9 }); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".config-") {
			t.Errorf("leftover temp file %q", name)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != configFileMode {
		t.Errorf("mode = %v, want %v", mode, configFileMode)
	}
}

// TestWriteFileAtomicReplacesTarget checks the helper directly, including the
// case where the target does not exist yet.
func TestWriteFileAtomicReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")

	if err := writeFileAtomic(path, []byte("first\n"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic() error = %v", err)
	}
	if err := writeFileAtomic(path, []byte("second\n"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second\n" {
		t.Errorf("data = %q, want %q", data, "second\n")
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(backup) != "first\n" {
		t.Errorf("backup = %q, want %q", backup, "first\n")
	}
}
