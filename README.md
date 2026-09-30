# spoty_lls — Spotify "Last Liked" playlist sync

Keeps a playlist in sync with your most recently liked songs (100 by default, configurable via `SPOTIFY_LIKED_LIMIT`).
Written in Go — **zero dependencies** (stdlib only), ships as a single static binary.

## 1. Create a Spotify app
1. Go to https://developer.spotify.com/dashboard and create an app.
2. Copy the **Client ID** and **Client Secret**.
3. In the app **Settings**, add Redirect URI: `http://127.0.0.1:8888/callback`

## 2. Get a refresh token (one time)
```sh
cp .env.example .env   # fill in SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET first
make token
```
Follow the prompts, then copy the printed refresh token into your `.env`
as `SPOTIFY_REFRESH_TOKEN`.

Requires Go 1.27+ on the host if you prefer running it directly:
`go run ./cmd/get_token`.

## 3. Build and run locally
```sh
make run
```
Or without Docker: `go run ./cmd/spoty_lls` (reads the same env vars; use
`set -a; . ./.env; set +a` or your shell's env-file mechanism).

## 4. Testing

Tests run directly on the host Go toolchain — no Docker, no network
(all HTTP is faked with `httptest`):

    make test    # go test ./...
    make lint    # go vet + gofmt check
    make fmt     # gofmt -w .

## 5. Schedule in the cloud
The container is a one-shot job. Trigger it on a schedule with your platform:

**Kubernetes CronJob (daily at 03:00):**
```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: spoty-lls
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: spoty-lls
              image: your-registry/spoty-lls:latest
              envFrom:
                - secretRef:
                    name: spoty-lls-secrets   # holds the SPOTIFY_* vars
```

**Plain host cron running Docker:**
```cron
0 3 * * * docker run --rm --env-file /opt/spoty_lls/.env your-registry/spoty-lls:latest >> /var/log/spoty-lls.log 2>&1
```

## Environment variables

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `SPOTIFY_CLIENT_ID` | yes | — | Spotify app client ID |
| `SPOTIFY_CLIENT_SECRET` | yes | — | Spotify app client secret |
| `SPOTIFY_REFRESH_TOKEN` | yes (sync) | — | Obtained once via `make token` |
| `SPOTIFY_REDIRECT_URI` | no | `http://127.0.0.1:8888/callback` | Must match Spotify app settings |
| `SPOTIFY_LIKED_LIMIT` | no | `100` | How many recently liked songs to sync; must be a positive integer |
| `SPOTIFY_PLAYLIST_NAME` | no | `Last Liked` | Name of the playlist to sync into |
| `DEV_MODE` | no | `0` | Truthy → debug logging |
| `TELEGRAM_BOT_TOKEN` | no | — | Enables failure alerts |
| `TELEGRAM_CHAT_ID` | no | — | Enables failure alerts |

## Notes
- Scopes used: `user-library-read`, `playlist-read-private`,
  `playlist-modify-private`, `playlist-modify-public`.
- The playlist is created **private** if it doesn't exist.
- Changing `SPOTIFY_PLAYLIST_NAME` targets (or creates) a different playlist; the
  previously used playlist is not migrated or deleted.
- Each run replaces the playlist with your `SPOTIFY_LIKED_LIMIT` (default 100) most
  recently liked songs, preserving liked order (most-recent first).
- Idempotent: the SHA-256 digest of the liked URIs is stored in the playlist
  description as `[#<12-hex>]`; unchanged digest → no API writes.
- **Security: treat refresh token like a password. Anyone with it controls the Spotify account's library/playlists.**
- On host cron, run `chmod 600 .env` so the file is not readable by other users.
- Image: multi-stage `golang:1.27` build → `distroless/static:nonroot` runtime
  (static binary, `CGO_ENABLED=0`, non-root user for least-privilege).
