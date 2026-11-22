package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatPathSitsNextToConfig(t *testing.T) {
	if got := heartbeatPath(filepath.Join("data", "config.toml")); got != filepath.Join("data", ".heartbeat") {
		t.Errorf("heartbeatPath() = %q", got)
	}
	if got := heartbeatPath("config.toml"); got != ".heartbeat" {
		t.Errorf("heartbeatPath(cwd) = %q", got)
	}
}

func TestRunHealthcheckHealthy(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	// The heartbeat file is written by the bot, not by the config store.
	if err := os.WriteFile(heartbeatPath(configPath), []byte("beat\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := runHealthcheck(configPath, time.Now(), &out); code != 0 {
		t.Fatalf("runHealthcheck() = %d, want 0 (%s)", code, out.String())
	}
	if !strings.Contains(out.String(), "healthy") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunHealthcheckStale(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	path := heartbeatPath(configPath)
	if err := os.WriteFile(path, []byte("beat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-heartbeatMaxAge - time.Minute)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := runHealthcheck(configPath, time.Now(), &out); code != 1 {
		t.Fatalf("runHealthcheck() = %d, want 1 for a stale heartbeat", code)
	}
	if !strings.Contains(out.String(), "unhealthy") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunHealthcheckMissing(t *testing.T) {
	var out bytes.Buffer
	// t.TempDir() is empty, and the fallback candidates do not exist in a test
	// run, so nothing is fresh.
	if code := runHealthcheck(filepath.Join(t.TempDir(), "config.toml"), time.Now(), &out); code != 1 {
		t.Fatalf("runHealthcheck() = %d, want 1 when no heartbeat exists", code)
	}
}

func TestStartHeartbeatWritesImmediately(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startHeartbeat(ctx, configPath, testLogger(t))

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(heartbeatPath(configPath)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat file was not written")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A fresh heartbeat must satisfy the healthcheck.
	var out bytes.Buffer
	if code := runHealthcheck(configPath, time.Now(), &out); code != 0 {
		t.Fatalf("runHealthcheck() = %d, want 0 (%s)", code, out.String())
	}
}
