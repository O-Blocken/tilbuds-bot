# Contributing

Thanks for taking a look. This is a single-maintainer hobby project, so the most
useful contributions are focused bug reports and small, tested patches.

## Before you open a pull request

```sh
gofmt -l .          # must print nothing
go vet ./...
go test ./...       # and ./... -race
CGO_ENABLED=0 go build ./...
```

CI runs exactly these, plus `govulncheck` and a container build.

## Ground rules

- **Tests.** Behaviour changes need a test. The repository has no network tests:
  anything that would talk to Tjek, Discord or prej.dk is injected or skipped.
- **No live guild data.** Never commit `data/`, `config.toml`, `*.db` or real
  channel/role/user IDs. Use obviously fake snowflakes such as
  `100000000000000001` in tests.
- **Danish user-facing text.** Bot replies and log hints aimed at the operator
  are Danish, matching the existing strings. Code, comments and documentation
  are English except `docs/sogesprog.md`, which documents the Danish query
  language for users.
- **Keep matching deterministic.** The query engine must not consult an LLM, an
  embedding model or any remote service. Reasons shown in `/preview_deals` and
  `/search` have to be reproducible.
- **No new services.** The bot is one process plus the optional PaddleOCR
  worker; features that need a database server, broker or web frontend are out
  of scope.

## Refreshing the real-offer fixture

`internal/search/testdata/real_offers.toml` is a captured publication used to
check the concept registry against real wording:

```sh
go run ./cmd/dealfetch -out internal/search/testdata/real_offers.toml
```

It contains only public offer data (names, prices, departments), never personal
data.

## Licensing

Contributions are accepted under the project license (AGPL-3.0-only). By
opening a pull request you agree to license your contribution under those terms.
