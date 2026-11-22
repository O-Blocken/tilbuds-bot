package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func TestResolveConfigPathPrimaryWritable(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "config.toml")
	if got := resolveConfigPath(primary, testLogger(t)); got != primary {
		t.Fatalf("resolveConfigPath() = %q, want %q", got, primary)
	}
}

func TestResolveConfigPathFallsBackToWorkAndMigratesState(t *testing.T) {
	base := t.TempDir()
	readOnlyDir := filepath.Join(base, "data")
	if err := os.MkdirAll(readOnlyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	primary := filepath.Join(readOnlyDir, "config.toml")
	primaryState := []byte("[bot]\nchannel_id = 42\n")
	if err := os.WriteFile(primary, primaryState, 0o444); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	// Lock the directory after the file exists: 0555 keeps it readable
	// (traversal needs the execute bit) while denying new file creation,
	// which is what makes the path unwritable for resolveConfigPath.
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { os.Chmod(readOnlyDir, 0o755) })

	if err := probeConfigDir(readOnlyDir); err == nil {
		t.Skip("test user can write to 0444 directory; cannot simulate an unwritable path")
	}

	t.Chdir(base)
	got := resolveConfigPath(primary, testLogger(t))
	wantFallback := filepath.Join("work", "config.toml")
	if filepath.Clean(got) != filepath.Clean(wantFallback) {
		t.Fatalf("resolveConfigPath() = %q, want fallback %q", got, wantFallback)
	}

	state, err := os.ReadFile(wantFallback)
	if err != nil {
		t.Fatalf("state not migrated to fallback: %v", err)
	}
	if string(state) != string(primaryState) {
		t.Fatalf("migrated state = %q, want %q", state, primaryState)
	}
}
