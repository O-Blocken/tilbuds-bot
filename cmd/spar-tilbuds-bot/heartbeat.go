package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// The container healthcheck needs a liveness signal that does not require a
// port, a network round trip or extra packages in the image. The bot touches a
// heartbeat file; `--healthcheck` fails when it is stale, which catches a hung
// or deadlocked process that a TCP check would miss.
const (
	heartbeatInterval = 30 * time.Second
	// heartbeatMaxAge tolerates a slow start and one missed tick.
	heartbeatMaxAge   = 3 * time.Minute
	heartbeatFileName = ".heartbeat"
	// fallbackConfigName mirrors resolveConfigPath's fallback candidates.
	fallbackWorkConfig = "work/config.toml"
	fallbackTempConfig = "spar-tilbuds-bot-config.toml"
)

// heartbeatPath places the heartbeat next to the effective config file, in a
// directory the bot has already proven it can write.
func heartbeatPath(configPath string) string {
	dir := filepath.Dir(configPath)
	if dir == "" || dir == "." {
		return heartbeatFileName
	}
	return filepath.Join(dir, heartbeatFileName)
}

// heartbeatCandidates lists every location the heartbeat may be in, so the
// healthcheck works whether or not the config fell back to a writable path.
func heartbeatCandidates(configPath string) []string {
	return []string{
		heartbeatPath(configPath),
		heartbeatPath(fallbackWorkConfig),
		heartbeatPath(filepath.Join(os.TempDir(), fallbackTempConfig)),
	}
}

// startHeartbeat writes the heartbeat immediately and then on an interval until
// ctx is cancelled. Failures only warn: a read-only state directory must not
// take the bot down.
func startHeartbeat(ctx context.Context, configPath string, logger *slog.Logger) {
	path := heartbeatPath(configPath)
	touch := func() {
		payload := time.Now().UTC().Format(time.RFC3339Nano) + "\n"
		if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
			logger.Warn("cannot write heartbeat",
				slog.String("path", path), slog.String("error", err.Error()))
		}
	}

	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			touch()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// runHealthcheck returns 0 when a recent heartbeat exists and 1 otherwise,
// writing the reason to w so `docker inspect` shows it.
func runHealthcheck(configPath string, now time.Time, w io.Writer) int {
	var lastErr error
	for _, path := range heartbeatCandidates(configPath) {
		info, err := os.Stat(path)
		if err != nil {
			lastErr = err
			continue
		}
		age := now.Sub(info.ModTime())
		if age > heartbeatMaxAge {
			lastErr = fmt.Errorf("last heartbeat %s ago (limit %s) at %s", age.Round(time.Second), heartbeatMaxAge, path)
			continue
		}
		fmt.Fprintf(w, "healthy: last heartbeat %s ago at %s\n", age.Round(time.Second), path)
		return 0
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no heartbeat found in %v", heartbeatCandidates(configPath))
	}
	fmt.Fprintf(w, "unhealthy: %v\n", lastErr)
	return 1
}
