# spoty_lls — Spotify "Last Liked" playlist sync

Keeps a playlist in sync with your 100 most recently liked songs.
Single dependency: the `requests` package.

## 1. Create a Spotify app
1. Go to https://developer.spotify.com/dashboard and create an app.
2. Copy the **Client ID** and **Client Secret**.
3. In the app **Settings**, add Redirect URI: `http://127.0.0.1:8888/callback`

## 2. Install dependencies
```sh
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
```

## 3. Get a refresh token (one time)
```sh
make token
```
Follow the prompts, then copy the printed refresh token into your `.env`.

## 4. Configure
```sh
cp .env.example .env
# edit .env and fill in the secrets
```

## 5. Build and run locally
```sh
make run
```

## 6. Testing

Unit tests run in an isolated Docker stage (dev deps never ship to the prod image).

    make test

This builds the `test` target (`pytest` + `pytest-cov`) and runs the suite via
`docker run --rm`. Non-zero exit code on failure. Coverage report (statements,
misses, missing lines) is printed after tests finish.

For a browsable HTML report, mount a volume to persist the output:

    make test-cov

Open `htmlcov/index.html` in your browser.

## 7. Schedule in the cloud
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

## Notes
- Scopes used: `user-library-read`, `playlist-read-private`,
  `playlist-modify-private`, `playlist-modify-public`.
- The playlist is created **private** if it doesn't exist.
- Each run replaces the playlist with your 100 most recently liked songs,
  preserving liked order (most-recent first).
- **Security: treat refresh token like a password. Anyone with it controls the Spotify account's library/playlists.**
- On host cron, run `chmod 600 .env` so the file is not readable by other users.
- Image uses `python:3.13-slim` base with non-root user for least-privilege.
