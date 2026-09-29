# AGENTS.md — spoty_lls

Guidance for code agents working in this repo.

## What this project is

`spoty_lls` ("Spotify Last Liked Sync") is a small, single-purpose **Go** job that keeps a Spotify playlist named **"Last Liked"** in sync with the user's **most recently liked songs** (default 100, configurable via `LIKED_LIMIT`). It runs **once and exits** — designed to be triggered on a schedule (cron / Kubernetes CronJob) as a one-shot container.

Key design goals:
- **Zero dependencies**: Go stdlib only (Go 1.27). No third-party modules — keep it that way.
- **Do not over-engineer**: small app, plain code. JSON payloads are handled as `map[string]any` (typed structs only where stability matters, e.g. the token response).
- **Idempotent**: each run hashes the liked-track URIs (SHA-256, first 12 hex chars) and stores the digest in the playlist description (`[#<12-hex>]`). Unchanged hash → skip API writes.
- **Resilient**: retries on HTTP 429 (honors `Retry-After`, capped), 5xx, and network errors; re-authenticates on HTTP 401 then retries.
- **Least-privilege**: prod image is distroless static, nonroot.

> History note: this repo was rewritten from Python to Go (Linear A-59). The Python implementation had a latent bug — its transport layer's `raise_for_status()` converted every HTTP error into a generic `TransportError` before the 401/429/5xx branches could run, so those branches were dead code. The Go version implements the **documented intended semantics** (401 → re-auth → retry; 429 → honor `Retry-After`; other 4xx → immediate error, no retry). This was a deliberate, reviewed deviation.

## Runtime flow (`cmd/spoty_lls` → `internal/app.Run`)

1. `config.Load()` — env-derived settings; fails if `SPOTIFY_CLIENT_ID`/`SPOTIFY_CLIENT_SECRET` missing.
2. `spotify.New(...)` — fails if refresh token empty.
3. `app.Run(cfg, client, notify)`:
   - `Authenticate()` — refresh-token grant → Bearer token.
   - `CurrentUserID()`.
   - `LikedTrackURIs(cfg.LikedLimit)` (default 100) — most-recent first, paginated.
   - Empty liked list → log warning, exit success, **no playlist calls at all**.
   - `helpers.ComputeURIsHash(liked)` → 12-hex digest.
   - `FindPlaylist("Last Liked", userID)`; not found → `CreatePlaylist` (private).
   - Stored hash (parsed from description) == digest → skip.
   - Else `ReplaceTracks` (PUT first ≤100, POST remaining ≤100-chunks) + `ChangeDetails` (rewrite description with count/timestamp/hash).
4. On any failure: `Run` calls `notify("spoty_lls failed: …")` (Telegram, best-effort) and returns the error; `run()` returns exit code 1.

## Source layout

| Path | Responsibility |
|------|----------------|
| `cmd/spoty_lls/main.go` | Sync entrypoint. Thin: `main()` = `os.Exit(run())`; `run() int` is testable without `os.Exit`. |
| `cmd/get_token/main.go` | One-time interactive CLI to obtain a **refresh token** via the OAuth authorization-code flow (paste-the-redirect-URL style). Not unit-tested by design. |
| `internal/config/config.go` | Constants (API URLs, limits, retry tuning, scopes) + `Load()` from env + `DebugMode`/`Debugf` gate. |
| `internal/spotify/client.go` | `Client`: auth, core `do()` with retry loop, pagination, all Web API calls. No separate error taxonomy — plain `fmt.Errorf` errors; retry classification lives in `attempt()`/`do()`. |
| `internal/helpers/helpers.go` | Pure functions: `ComputeURIsHash`, `ParseHash`, `BuildDescription`, `Str2Bool`. |
| `internal/app/sync.go` | `Run()` orchestrator + `SpotifyAPI` interface (the test seam). |
| `internal/app/alerting.go` | `NotifyTelegram` — best-effort, no-op if bot token/chat ID unset. |

## Retry model (`internal/spotify/client.go`)

- Max **3 attempts** (`config.MaxRetries`) per API call; sleeps via injectable `c.sleep` (tests never sleep for real).
- **401** → `Authenticate()` → retry immediately (wait 0, consumes an attempt).
- **429** → wait `Retry-After` seconds (default 2 if absent/invalid), capped at 300 (`config.MaxRetryWait`).
- **5xx / network error** → wait 2s (`config.RetryDelay`), retry.
- **Other 4xx** → immediate error, **no retry**.
- 2xx empty body → empty map; invalid JSON → error.

Spotify paging/batching (`config`): `Page=50`, `Batch=100`, `DefaultLikedLimit=100` (overridable via `LIKED_LIMIT`).

## Configuration (environment variables)

Required: `SPOTIFY_CLIENT_ID`, `SPOTIFY_CLIENT_SECRET`, `SPOTIFY_REFRESH_TOKEN` (obtained once via `make token`).
Optional: `SPOTIFY_REDIRECT_URI` (default `http://127.0.0.1:8888/callback`), `LIKED_LIMIT` (positive integer, default 100; invalid/non-positive → `Load()` error), `DEV_MODE` (truthy → debug logging), `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`.

`.env.example` documents the required vars. Copy to `.env` for local runs.

> **Security:** the refresh token is equivalent to a password — anyone with it controls the account's library/playlists. Never commit `.env` or log the token.

## Build / test / lint (Makefile)

| Command | What it does |
|---------|--------------|
| `make build` | Docker build (`golang:1.27` → `distroless/static:nonroot`, `CGO_ENABLED=0`). |
| `make run` | Build + run the sync job once (`--env-file .env`). |
| `make token` | Interactive `get_token` helper in Docker. |
| `make test` | `go test ./...` on the host toolchain (stdlib only; no network — all HTTP faked with `httptest`). |
| `make lint` | `go vet ./...` + fail if `gofmt -l .` non-empty. |
| `make fmt` | `gofmt -w .` |

## Conventions

- **Go 1.27**, stdlib only. Adding a third-party module requires a very strong justification (issue constraint).
- gofmt-clean, `go vet`-clean. Line length isn't enforced, but keep lines readable.
- Tests are **colocated** (`*_test.go` next to sources). `internal/spotify` tests are same-package (`package spotify`) so they can inject `apiBase`/`tokenURL`/`sleep`/`token` on the `Client` directly.
- Keep `internal/helpers` pure and side-effect free (most unit-tested layer).
- Keep the orchestration in `internal/app.Run` behind the `SpotifyAPI` interface so `sync_test.go` can fake it; keep `cmd/*/main.go` thin.
- `config.Debugf` for debug logging (gated on `DEV_MODE`); plain `log` for info/warning/error.
- If your edit touches HTTP behavior, run `make test` before finishing.
