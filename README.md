# SPAR Tilbuds Bot

Go Discord bot that automatically posts selected SPAR, Føtex and Lidl deals every week using the
[`blockgameentity/tjekapi`](https://github.com/blockgameentity/tjekapi) Go library.

## Features

- Resolves the current SPAR, Føtex and Lidl publications through Tjek's SSR data
- Reads paginated hotspot data through the external tjekapi library
- Filters offers with a small deterministic query language: terms,
  exclusions (`-zero`), phrases (`"coca cola"`), price/unit-price/
  discount/store filters (`max:50`, `unitprice:kg:<80`, `discount:>=25%`)
  and concept/alias expansion (`øl` also finds `pilsner` and `Carlsberg`)
- Validates query expressions when subscriptions are added, with
  user-facing parse errors (see [docs/sogesprog.md](docs/sogesprog.md))
- Admin-managed concept/alias registry: `/concepts list/show/set/remove/
  restore/reload` edits synonyms live (stored in `bot.db`, layered over
  `concepts.toml`) without restarting the bot
- Price alerts: `/prisalarm add/list/remove/check` pings when a product
  reaches a target price — or undercuts the cheapest price ever recorded
  in the price history — once per weekly publication
- Shows enhedspris on deal embeds ("12,50 kr/kg") from structured offer
  data, with a per-liter fallback derived from OCR pack sizes
- Schedules automatic checks from a configurable 5-field cron line
- Downloads the catalog PDF and crops product images with tjekapi's PDFium processor
- OCRs cropped deal images in Danish with the bundled PaddleOCR worker
- Adds a per-item price line for detected multipack offers
- Reuses current publication files under `work/<publication>` and removes old work folders after 14 days
- Uses tjekapi's retry and rate-limit handling for Tjek/PDF HTTP requests
- Posts Discord embeds with attached deal images
- Tracks posted Discord message IDs for precise cleanup

## Requirements

- Go 1.27 or newer (the exact directive lives in `go.mod`; CI builds with it)
- A Discord bot token
- Optional: Python 3.12 + `uv` for PaddleOCR enrichment

The bot is pure Go: SQLite uses `modernc.org/sqlite` and the PDF renderer uses the
WebAssembly PDFium backend from `go-pdfium`. No C toolchain, CGO or native PDFium
install is required, and `CGO_ENABLED=0 go build ./...` works.

PaddleOCR is optional enrichment, but the repository's default service configuration uses the locked `uv` environment whenever `uv` is available. The environment is pinned to CPython 3.12 in `.python-version` and installs PaddleOCR 3.7 with PaddlePaddle 3.2 from `uv.lock`. PaddleOCR models are downloaded lazily by the first OCR request. If `uv` is unavailable, image cropping and deal posting continue without OCR enrichment. The bundled launcher filters native PaddlePaddle diagnostics from stdout and forces UTF-8 because tjekapi uses stdout for its JSON protocol and Windows deal filenames can contain non-ASCII characters.

From the repository root, set up the OCR environment with:

```powershell
uv python install 3.12
uv sync --locked
uv run --locked python -c "import paddleocr, sys; print(sys.executable)"
```

The Go service starts PaddleOCR through `uv run --locked python` by default. To use an already-created Python interpreter instead, set `PADDLEOCR_PYTHON` to its executable path before starting the bot. This is useful when using a separately managed uv environment or when `uv` is not on the service process's `PATH`. Start the bot from the repository root so the launcher and `work/` paths resolve correctly.

The requested tjekapi repository currently declares its Go module path as `tjekapi`,
so `go.mod` pins the GitHub revision through a `replace` directive. This keeps the
project on the upstream repository while using the path expected by its packages.

## Setup

1. Create a `.env` file:

```text
DISCORD_BOT_TOKEN=your_bot_token_here
```

1. Set up the locked PaddleOCR environment:

```powershell
uv python install 3.12
uv sync --locked
```

1. Download Go dependencies:

```powershell
go mod tidy
```

1. Run the tests:

```powershell
go test ./...
```

1. Run the bot:

```powershell
go run ./cmd/spar-tilbuds-bot
```

## Docker Compose

The repository includes a multi-stage `Dockerfile` and `compose.yaml`. The image
contains the Go bot and the locked uv-managed PaddleOCR environment, so the host
only needs Docker with Compose. No ports are exposed because the bot connects to
Discord and Tjek over outbound HTTPS/WebSocket connections.

1. Create `.env` from the template and put the token (and ideally the guild ID) in it:

```sh
cp .env.example .env
$EDITOR .env            # DISCORD_BOT_TOKEN=..., DISCORD_GUILD_ID=...
```

`compose.yaml` marks `.env` as optional, so a missing file no longer stops the
whole Compose project; the bot then exits with `missing DISCORD_BOT_TOKEN`.

1. Make the state directory writable by the unprivileged container user
   (UID/GID `10001`) and start it:

```sh
mkdir -p data
sudo chown -R 10001:10001 data
docker compose up -d --build
docker compose logs -f spar-tilbuds-bot
```

The image runs as `10001:10001` (not root). Named volumes inherit that ownership
on first use; the `./data` bind mount does not, which is why the `chown` above
matters. Without it the bot logs the likely cause (host ownership, rootless
Docker/userns-remap, SELinux) and falls back to a writable path instead of
crash-looping.

`docker compose ps` reports the container health: the image ships a `HEALTHCHECK`
that runs `spar-tilbuds-bot --healthcheck`, which fails when the liveness
heartbeat next to the config file goes stale (catching a hung process, not just a
dead one). `check_cron` and `notification_time` are evaluated in `TZ`
(`Europe/Copenhagen` by default; override with `TZ=... docker compose up -d`).

The bot state lives in `data/config.toml`, which is mounted read-write at `/data`
so channel settings and tracked message IDs survive container updates. The
container root stays read-only, and the container points at the file through
`CONFIG_PATH=/data/config.toml`. On first start the bot verifies it can create
and replace that file. If the directory is not writable the bot logs the
likely cause (for example a SELinux-labeled bind mount or host ownership) and
falls back to `work/config.toml` (kept on the named work volume) or `/tmp`,
migrating any previously stored state, so a read-only mount cannot crash-loop
the bot. State stored on a fallback path is lost when the container is
rebuilt, so restore the primary path as soon as the mount is fixed. If
`data/config.toml` does not exist yet, the bot starts with default
settings and creates the file the first time it saves. The catalog/image cache
and PaddleOCR model cache use named volumes. Do not use `docker compose down -v`
unless those caches and the stored bot state should be deleted.

The container sets `PADDLEOCR_PYTHON` to the Python interpreter built by uv. The
PaddleOCR models are still downloaded lazily on the first OCR request. The image
build follows the target platform and the lockfile includes Linux x86_64 and
arm64 PaddlePaddle wheels.

If the log says `kunne ikke oprette config-fil i /data: ... permission denied`,
the `./data` bind mount is not writable from inside the container. On Linux
the usual causes are:

1. **Rootless Docker or `userns-remap`** (common on Arch): the container's
   root is an unprivileged host user, so it cannot write a host directory
   owned by another user. Either create the `data/` directory in the
   subuid/subgid range that Docker's user namespace uses, or make it writable
   by the user that runs Docker.
2. **Ownership or a read-only filesystem**: the host `data/` directory must
   be writable by the container user (root for this image with regular
   Docker) and must not be on a read-only mount.
3. **SELinux or AppArmor** (less common; e.g. RHEL/Fedora hosts): label the
   mount with `./data:/data:z` or adjust the security module.

Until the host is fixed the bot keeps running on the fallback path, so deal
posting is not interrupted by the container crashing.

## Build

Build a runnable binary:

```powershell
go build -o .\bin\spar-tilbuds-bot.exe .\cmd\spar-tilbuds-bot
```

Run it with:

```powershell
.\bin\spar-tilbuds-bot.exe
```

On macOS/Linux, use `./bin/spar-tilbuds-bot` as the output path/name.

## Discord Commands

All commands are admin-only.

- `/set_deals_channel #channel [butik]` - Set the channel for automatic deal posts (SPAR, Føtex or Lidl)
- `/send_deals [#channel] [butik]` - Manually fetch and post current deals (one, several or all stores)
- `/preview_deals [limit] [butik]` - Preview current matching deals privately, with scores
- `/search <query> [limit] [butik]` - Search the current publication with the query language (defaults to SPAR); see [docs/sogesprog.md](docs/sogesprog.md)
- `/want add <id> <tekst>` - Add a shared deal want (also `remove` and `list`)
- `/top [limit] [butik]` - This week's top-ranked deals with price badges
- `/stats` - Matcher, wants and database status
- `/clear_deals` - Delete the bot's tracked deal messages

Each retailer floods its own channel with one message per deal (+ role ping).
Manual re-sends only post deals that are new for the publication.

## Configuration

`config.toml` controls the posting channels (SPAR + optional Føtex and Lidl), ping roles, schedule, notification time, matcher, wants and tracked message IDs. Outside Docker the bot reads and writes `config.toml` in the working directory; in Docker Compose it uses `CONFIG_PATH=/data/config.toml`, so edit `data/config.toml` on the host. A `bot.db` SQLite file (wants, price history, prej.dk cache, feed state, concept overrides, price alerts) lives next to it.

[`data/config.toml.example`](data/config.toml.example) documents every key. The
generated file is not committed and `data/`, `*.db` and `.env` are gitignored on
purpose: `config.toml` holds one guild's channel/message IDs and `bot.db` holds
price alerts with Discord user IDs.

Writes are atomic (temp file + fsync + rename) and the previous version is kept
as `config.toml.bak`. The bot only owns the `[bot]` keys and `[[want]]` blocks;
anything else in the file — unknown sections, extra keys, comments — is read back
and re-emitted on the next save, so hand edits are not silently dropped. Unknown
keys *inside* a `[[want]]` block are preserved as comments, because that block is
regenerated from the modelled fields.

Environment variables (see [`.env.example`](.env.example)):

| Variable | Meaning |
| --- | --- |
| `DISCORD_BOT_TOKEN` | Required. Bot token. |
| `DISCORD_GUILD_ID` | Optional but recommended. Registers slash commands for that guild only and refuses interactions from every other guild. |
| `CONFIG_PATH` | State file location (default `./config.toml`). |
| `CONCEPTS_PATH` | Concept registry override (default: `concepts.toml` next to the config). |
| `PADDLEOCR_PYTHON` | Interpreter for the OCR worker; defaults to `uv run --locked python`. |

All commands are admin-only, and that is enforced twice: Discord hides them from
members without the Administrator permission, and every interaction re-checks the
invoking member's effective guild permissions before running.

### Concept registry (`concepts.toml`)

Search synonyms live in `concepts.toml`, loaded from `CONCEPTS_PATH` when
set, else from the config directory (`/data/concepts.toml` in Docker).
The file is read at startup and on `/concepts reload`; the embedded
registry inside the binary is used when no file is mounted. Concept
changes made with `/concepts set` and `/concepts remove` are stored in
`bot.db` and layered over the file, so they survive container updates.
To customize the base registry in Docker, drop a `concepts.toml` into
the mounted `data/` directory on the host and run `/concepts reload` —
no rebuild needed.

The default cron schedule is:

```toml
check_cron = "*/30 * * * 5"
```

That matches the old behavior: check every 30 minutes on Fridays. The bot supports standard 5-field cron syntax: minute, hour, day-of-month, month, day-of-week.

The actual automatic notification is held until the configured local time:

```toml
notification_time = "07:00"
```

With the default cron and `notification_time = "07:00"`, the bot checks throughout Friday but will not fetch/post deals before 7 AM local time. Manual `/send_deals` and `/preview_deals` are not gated by this setting. Føtex posts to `fotex_channel_id` and Lidl to `lidl_channel_id` once set; without them only the SPAR feed runs.

Deal matching is controlled by `matcher` plus either rich `[[want]]` blocks or the
legacy `keywords` list (one normalized want per keyword when no wants are defined):

```toml
matcher = "query"   # or "keywords" for the strict legacy matcher

[[want]]
id = "sodavand"
describe = "Sodavand og øl til weekenden"
include = ["sodavand", "cola", "pepsi"]
exclude = ["lakrids"]
max_price = 25.0
allow_app_price = false
threshold = 0.45
```

`posted_messages` is maintained by the bot as `"retailer:channel_id:message_id"` entries so cleanup can delete exact messages instead of scanning channel history.

## Project Structure

```text
Dockerfile                  # Multi-stage Go and PaddleOCR image
compose.yaml                # Docker Compose hosting definition
.dockerignore               # Container build context exclusions

cmd/spar-tilbuds-bot/       # Bot entry point, config-path probing, heartbeat
cmd/dealfetch/              # Fixture tool: captures a real publication for tests

internal/
├── config/                 # config.toml parsing, atomic writes, .env loading
├── discordbot/             # Session, slash commands, posting loop, alerts
├── match/                  # Wants, matchers, scoring, concept integration
├── search/                 # Query language: parse, compile, match, concepts.toml
├── service/                # fetch → crop → OCR → rank pipeline
├── store/                  # SQLite state (wants, history, feeds, alerts, concepts)
└── ocr/                    # uv-aware PaddleOCR option setup

docs/sogesprog.md           # User-facing query-language reference (Danish)
data/config.toml.example    # Documented config template (commit this, not state)
pyproject.toml, uv.lock     # Locked PaddleOCR environment (optional)
flake.nix, NIX.md           # Optional NixOS module + Podman image build
.github/workflows/ci.yml    # vet, race tests, govulncheck, container build
```

The external `tjekapi` and `tjekapi/pdf` packages provide the Tjek API, deal
modeling, PDF download, image cropping, and PaddleOCR worker integration.

## How It Works

1. Ask tjekapi for the current SPAR publication ID from Tjek's SSR data.
2. Fetch each page of hotspots through tjekapi.
3. Parse offer names for product name, DKK price, and APP price flag.
4. Match offers against `[[want]]` blocks (or legacy `keywords`) with the
   `internal/match` package: Danish normalization, whole-word and quoted-phrase
   matching, exclusions and price guards, plus concept/alias expansion from
   `concepts.toml`. There is no LLM, no embedding model and no fuzzy matching;
   every hit carries a deterministic reason shown in `/preview_deals`.
5. Deduplicate repeated/grouped offers.
6. Download the publication PDF through tjekapi when needed and reuse it for the same publication.
7. Crop hotspot regions through tjekapi's PDF processor to PNG files under `work/<publication>/cropped`.
8. Convert cropped offer images to black and white OCR copies when OCR is enabled.
9. Send OCR requests to a long-lived Danish PaddleOCR worker started through uv and look for pack sizes like `18x33cl`.
10. Send Discord embeds with the cropped images attached and per-item prices when available.
11. Store the sent Discord message IDs in `config.toml` and the per-retailer publication state in `bot.db`.
12. Remove cached work folders whose newest file is older than 14 days.

## Automatic Posting

The bot checks according to `check_cron`. With the default, it checks every 30 minutes on Fridays. It will:

- Wait until `notification_time` before automatic posting
- Skip publications already recorded as posted (`bot.db`, with `config.toml` as
  fallback)
- Delete tracked bot messages before posting a new batch

A publication is only recorded as posted once a run **completed**. If the fetch
fails, the match set is empty, or posting aborts midway, the marker stays unset
and the next scheduled check retries — messages that already reached Discord are
still tracked, so the retry can clean them up instead of leaking duplicates.
Manual `/send_deals` is incremental: it appends only offers this publication has
not posted yet (which needs `bot.db`; without SQLite it reposts everything).

## Security

- Runs unprivileged (`10001:10001`) on a read-only root filesystem with all
  capabilities dropped and `no-new-privileges`.
- The bot token is read from the environment/`.env` only; it is never logged,
  committed or baked into the image.
- Slash commands are administrator-only, enforced in code as well as by Discord,
  and optionally pinned to one guild via `DISCORD_GUILD_ID`.
- Internal error details (SQL, paths, upstream URLs) are logged, not posted to
  channels; users get a short Danish message.
- Dependency and image scanning: CI runs `go vet`, the race-enabled test suite
  and `govulncheck`; Dependabot keeps Go, GitHub Actions, Docker and uv current.

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## Testing

```sh
go test ./...          # unit tests, no network
go test -race ./...
go vet ./...
gofmt -l .             # must print nothing
CGO_ENABLED=0 go build ./...
```

`internal/search/testdata/real_offers.toml` is a captured publication used to
check the concept registry against real wording; refresh it with
`go run ./cmd/dealfetch -out internal/search/testdata/real_offers.toml`.

## License

AGPL-3.0-only. See [LICENSE](LICENSE). In short: you may run, modify and
redistribute this bot, but if you run a modified version as a network service you
must offer its source to the users of that service.
