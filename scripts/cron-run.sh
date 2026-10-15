#!/usr/bin/env bash
# Run the spoty_lls sync container once. Intended for host cron, e.g.:
#   0 3 * * * /opt/spoty_lls/cron-run.sh >> /var/log/spoty-lls.log 2>&1
#
# Overridable via env:
#   SPOTY_IMAGE     image to run      (default: ghcr.io/antavik/spoty_lls:latest)
#   SPOTY_ENV_FILE  SPOTIFY_* env file (default: /opt/spoty_lls/.env)
set -euo pipefail

# cron starts with a minimal PATH; make sure docker is found.
export PATH="/usr/local/bin:/usr/bin:/bin:${PATH:-}"

IMAGE="${SPOTY_IMAGE:-ghcr.io/antavik/spoty_lls:latest}"
ENV_FILE="${SPOTY_ENV_FILE:-/opt/spoty_lls/.env}"
NAME="spoty-lls"

log() { printf '%s cron-run: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

if [[ ! -r "$ENV_FILE" ]]; then
  log "env file not readable: $ENV_FILE"
  exit 1
fi

# Fixed container name doubles as a lock: an overlapping run fails fast.
if [[ "$(docker container inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null)" == true ]]; then
  log "previous run still in progress, skipping"
  exit 0
fi
# Leftover from an interrupted run (e.g. daemon restart) would block the name.
docker container rm "$NAME" >/dev/null 2>&1 || true

# Pick up new releases; a registry hiccup should not block the sync.
docker pull -q "$IMAGE" >/dev/null || log "pull failed, using cached image"

log "start $IMAGE"
rc=0
docker run --rm --name "$NAME" \
  --env-file "$ENV_FILE" \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  "$IMAGE" || rc=$?
log "done exit=$rc"
exit "$rc"
