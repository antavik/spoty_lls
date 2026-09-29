# Workflow Summary — A-60

- **Run timestamp:** 2026-09-29T07:51Z (UTC)
- **Issue:** [A-60 — add env var to configure quantity of songs to copy](https://linear.app/insta21/issue/A-60/add-env-var-to-configure-quantity-of-songs-to-copy) (default 100 songs)
- **Branch:** `antavik/a-60-add-env-var-to-configure-quantity-of-songs-to-copy` (worktree `/Users/svecha/Repos/antavik-a-60-add-env-var-to-configure-quantity-of-songs-to-copy`, based on `origin/main` @ 48f4cb0)
- **Draft PR:** https://github.com/antavik/spoty_lls/pull/3
- **Commit:** d1296a8 `feat: add SPOTIFY_LIKED_LIMIT env var to configure synced song count`

## Implementation

Optional env var `SPOTIFY_LIKED_LIMIT` configures how many recently-liked songs sync to the "Last Liked" playlist. Unset/empty → `DefaultLikedLimit = 100`; non-numeric or <= 0 → fail-fast `Load()` error containing "SPOTIFY_LIKED_LIMIT". No upper cap (deliberate, avoids arbitrary product policy). Const `LikedLimit` renamed `DefaultLikedLimit`; `Config` gains `LikedLimit int`; `app.Run` signature changed to `Run(cfg, client, notify)` (matches AGENTS.md-documented design) and `sync` uses `cfg.LikedLimit`. No spotify client changes — pagination (Page=50) and ReplaceTracks batching (Batch=100) already handle arbitrary limits.

## TDD evidence

- 1 task. `@test` wrote tests first → RED (compile errors: `undefined: DefaultLikedLimit`, `cfg.LikedLimit undefined`, `too many arguments in call to Run` — all MISSING_BEHAVIOR on not-yet-existing API).
- `@make` implemented contract → GREEN: `go test ./...` all packages pass; `go vet ./...` clean; `gofmt -l .` empty; `go build ./...` ok.
- Post-@test file gate: only `internal/app/sync_test.go`, `internal/config/config_test.go` touched. Pass.
- NOT_TESTABLE tasks: none. Test-quality escalations: none.
- Note: `@make` sandbox denied `go` tooling; caller ran verification directly.

## Review outcomes

- **Plan review (1 cycle):** @check ACCEPTABLE (MEDIUM: no upper bound — deferred as follow-up; LOW: boundary parsing tests — covered by table test). @simplify ACCEPTABLE (pass bare int instead of Config — rejected, AGENTS.md documents `Run(cfg, client, notify)`; consolidate config tests — adopted as table-driven).
- **Final review (1 cycle):** @check ACCEPTABLE, no correctness/safety issues. @simplify ACCEPTABLE; two low-payoff test nits (unset-vs-empty setup branch in TestLoadLikedLimit; overlapping passthrough tests) — left as-is.
- Unresolved blockers: none.

## Files changed

- internal/config/config.go — DefaultLikedLimit const, Config.LikedLimit, Load() parsing/validation
- internal/app/sync.go — Run(cfg, client, notify), sync uses cfg.LikedLimit
- cmd/spoty_lls/main.go — passes cfg to app.Run
- internal/config/config_test.go — TestLoadLikedLimit table test, TestConstants updated
- internal/app/sync_test.go — testCfg() helper, migrated Run call sites, TestRunUsesCfgLikedLimit
- README.md, AGENTS.md, .env.example — SPOTIFY_LIKED_LIMIT docs

## Follow-ups (optional)

- Consider an upper cap for SPOTIFY_LIKED_LIMIT if huge values become an operational problem (@check MEDIUM, deferred).
- `cmd/get_token` also fails on invalid SPOTIFY_LIKED_LIMIT since it shares config.Load() — acceptable fail-fast, acknowledge in release notes.
