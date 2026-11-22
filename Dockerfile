# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Python: locked PaddleOCR environment (optional OCR enrichment)
# ---------------------------------------------------------------------------
FROM ghcr.io/astral-sh/uv:0.13.9 AS uv

FROM python:3.12-slim-bookworm AS python-dependencies

COPY --from=uv /uv /uvx /usr/local/bin/

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=0

WORKDIR /app

COPY .python-version pyproject.toml uv.lock ./
RUN uv sync --locked --no-install-project

# ---------------------------------------------------------------------------
# Go: static binary
#
# The bot is pure Go: SQLite comes from modernc.org/sqlite and the PDF renderer
# from the WebAssembly PDFium build, so CGO and a C toolchain are not needed.
# ---------------------------------------------------------------------------
FROM golang:1.27-bookworm AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -trimpath -ldflags="-s -w" -o /out/spar-tilbuds-bot ./cmd/spar-tilbuds-bot

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
FROM python:3.12-slim-bookworm AS runtime

# Run unprivileged. Named volumes inherit this ownership on first use; a host
# bind mount at /data must be writable by the same UID (see README).
ARG UID=10001
ARG GID=10001

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONUTF8=1 \
    PYTHONIOENCODING=utf-8 \
    PADDLEOCR_PYTHON=/app/.venv/bin/python \
    CONFIG_PATH=/data/config.toml \
    # check_cron and notification_time are evaluated in this timezone.
    TZ=Europe/Copenhagen \
    HOME=/home/bot \
    PATH=/app/.venv/bin:${PATH}

# tzdata makes TZ meaningful; the rest are PaddleOCR/PaddlePaddle runtime
# libraries (OpenGL, OpenMP, X11 shims).
#
# DL3008 (pin apt versions) is disabled on purpose: Debian point releases drop
# old package versions, so pinning would break the build over time. The base
# image tag is the reproducible unit here.
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        ca-certificates \
        libgl1 \
        libglib2.0-0 \
        libgomp1 \
        libsm6 \
        libxext6 \
        libxrender1 \
        tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${GID}" bot \
    && useradd --uid "${UID}" --gid "${GID}" --create-home --home-dir /home/bot --shell /usr/sbin/nologin bot

WORKDIR /app

COPY --from=python-dependencies /app/.venv /app/.venv
COPY --from=go-builder /out/spar-tilbuds-bot /usr/local/bin/spar-tilbuds-bot
# The bot launches this file by its repository-relative path.
COPY internal/ocr/paddleocr_launcher.py /app/internal/ocr/paddleocr_launcher.py

# work/ holds the PDF/image cache and the config fallback; /data is the state
# mount. Both must be writable by the unprivileged user.
RUN mkdir -p /app/work /data \
    && chown -R bot:bot /app/work /data /home/bot

USER ${UID}:${GID}

# Liveness without exposing a port: the bot touches a heartbeat file and this
# check fails when it goes stale, which also catches a hung process.
HEALTHCHECK --interval=60s --timeout=10s --start-period=90s --retries=3 \
    CMD ["/usr/local/bin/spar-tilbuds-bot", "--healthcheck"]

ENTRYPOINT ["/usr/local/bin/spar-tilbuds-bot"]
