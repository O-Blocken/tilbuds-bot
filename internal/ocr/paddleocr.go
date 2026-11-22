package ocr

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"tjekapi/pdf"
)

const (
	paddleOCRPythonEnv = "PADDLEOCR_PYTHON"
	uvCommand          = "uv"
	paddleOCRLauncher  = "internal/ocr/paddleocr_launcher.py"
)

// NewPaddleOCROption enables the upstream PaddleOCR worker using the project's
// locked uv environment. PADDLEOCR_PYTHON can override uv with a Python
// executable or launcher that accepts the worker's command-line arguments.
// PaddleOCR remains optional: when neither the configured interpreter nor uv is
// available, the PDF processor continues without OCR enrichment.
func NewPaddleOCROption(logger *slog.Logger) (pdf.Option, bool) {
	if logger == nil {
		logger = slog.Default()
	}

	if python := strings.TrimSpace(os.Getenv(paddleOCRPythonEnv)); python != "" {
		if _, err := exec.LookPath(python); err != nil {
			logger.Warn(
				"PaddleOCR disabled: configured Python executable was not found",
				slog.String("environment", paddleOCRPythonEnv),
				slog.String("python", python),
				slog.String("error", err.Error()),
			)
			return nil, false
		}
		return pdf.WithPaddleOCR(
			pdf.WithPaddleOCRPython(python, paddleOCRLauncher),
		), true
	}

	if _, err := exec.LookPath(uvCommand); err != nil {
		logger.Warn(
			"PaddleOCR disabled: uv was not found; run uv sync --locked or set PADDLEOCR_PYTHON",
			slog.String("error", err.Error()),
		)
		return nil, false
	}

	return pdf.WithPaddleOCR(
		pdf.WithPaddleOCRPython(uvCommand, "run", "--quiet", "--locked", "python", paddleOCRLauncher),
	), true
}
