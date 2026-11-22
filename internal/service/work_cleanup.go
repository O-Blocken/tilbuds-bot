package service

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cleanupOldWorkDirs(root string, maxAge time.Duration, keepDir string, logger *slog.Logger) error {
	if maxAge <= 0 {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(root, entry.Name())
		if samePath(dir, keepDir) {
			continue
		}

		newest, err := newestModTime(dir)
		if err != nil {
			logger.Warn("failed to inspect work folder", slog.String("path", dir), slog.String("error", err.Error()))
			continue
		}
		if newest.After(cutoff) {
			continue
		}

		if err := removeWorkDir(root, dir); err != nil {
			logger.Warn("failed to remove old work folder", slog.String("path", dir), slog.String("error", err.Error()))
			continue
		}
		logger.Info("removed old work folder", slog.String("path", dir), slog.Time("newest_file", newest))
	}
	return nil
}

func newestModTime(dir string) (time.Time, error) {
	newest := time.Time{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

func removeWorkDir(root, dir string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	rootAbs = filepath.Clean(rootAbs)
	dirAbs = filepath.Clean(dirAbs)
	prefix := rootAbs + string(os.PathSeparator)
	if !strings.HasPrefix(strings.ToLower(dirAbs), strings.ToLower(prefix)) {
		return nil
	}
	return os.RemoveAll(dirAbs)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}
