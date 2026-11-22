package service

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOldWorkDirsRemovesOnlyExpiredFolders(t *testing.T) {
	root := t.TempDir()
	oldDir := makeWorkDir(t, root, "old", 20*24*time.Hour)
	recentDir := makeWorkDir(t, root, "recent", time.Hour)
	keepDir := makeWorkDir(t, root, "keep", 30*24*time.Hour)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := cleanupOldWorkDirs(root, 14*24*time.Hour, keepDir, logger); err != nil {
		t.Fatalf("cleanupOldWorkDirs() error = %v", err)
	}

	if exists(oldDir) {
		t.Fatalf("old work dir still exists: %s", oldDir)
	}
	if !exists(recentDir) {
		t.Fatalf("recent work dir was removed: %s", recentDir)
	}
	if !exists(keepDir) {
		t.Fatalf("keep work dir was removed: %s", keepDir)
	}
}

func makeWorkDir(t *testing.T, root, name string, age time.Duration) string {
	t.Helper()

	dir := filepath.Join(root, name)
	nested := filepath.Join(dir, "cropped")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file := filepath.Join(nested, "deal.png")
	if err := os.WriteFile(file, []byte("png"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	when := time.Now().Add(-age)
	for _, path := range []string{file, nested, dir} {
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", path, err)
		}
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
