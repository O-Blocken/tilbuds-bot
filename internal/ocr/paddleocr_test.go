package ocr

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

func TestNewPaddleOCROptionDisablesWithoutConfiguredRuntime(t *testing.T) {
	t.Setenv(paddleOCRPythonEnv, "")
	t.Setenv("PATH", t.TempDir())

	option, enabled := NewPaddleOCROption(testLogger())
	if enabled {
		t.Fatal("PaddleOCR enabled without uv or a configured Python executable")
	}
	if option != nil {
		t.Fatal("PaddleOCR returned an option while disabled")
	}
}

func TestNewPaddleOCROptionAcceptsConfiguredInterpreter(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	t.Setenv(paddleOCRPythonEnv, executable)

	option, enabled := NewPaddleOCROption(testLogger())
	if !enabled {
		t.Fatal("PaddleOCR disabled for an existing configured interpreter")
	}
	if option == nil {
		t.Fatal("PaddleOCR returned a nil processor option")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
