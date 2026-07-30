#!/usr/bin/env python3
"""Sync the 100 most recently liked Spotify songs into a playlist.

Runs once and exits. Intended to be triggered by a scheduler (cron/CronJob).
Configuration comes from environment variables (see config.py).
"""

import logging
import sys

import config as c
from alerting import notify_telegram
from helpers import build_description, compute_uris_hash, parse_hash
from spotify import SpotifyClient

logging.basicConfig(
    level=logging.DEBUG if c.DEV_MODE else logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
)
log = logging.getLogger("spoty_lls")


def main() -> None:
    with SpotifyClient(
        client_id=c.CLIENT_ID,
        client_secret=c.CLIENT_SECRET,
        refresh_token=c.REFRESH_TOKEN,
    ) as client:
        user_id = client.current_user_id()
        log.debug("get current user id: %s", user_id)

        liked = client.liked_track_uris()
        if not liked:
            log.warning("no liked tracks found, skipping playlist update")
            return

        log.debug("get %d liked tracks", len(liked))

        digest = compute_uris_hash(liked)
        found = client.find_playlist(c.PLAYLIST_NAME, user_id)

        if found is None:
            playlist_id = client.create_playlist(user_id, c.PLAYLIST_NAME)
            stored_hash = None
            log.info("created playlist '%s' (%s)", c.PLAYLIST_NAME, playlist_id)
        else:
            playlist_id, description = found
            stored_hash = parse_hash(description)
            log.debug("found playlist '%s' (%s)", c.PLAYLIST_NAME, playlist_id)

        if stored_hash == digest:
            log.info("liked songs unchanged (hash %s), skipping update", digest)
            return

        client.replace_tracks(playlist_id, liked)
        log.debug("replaced tracks in playlist '%s' (%s)", c.PLAYLIST_NAME, playlist_id)

        client.change_details(playlist_id, description=build_description(len(liked), digest))
        log.debug("updated playlist description for '%s' (%s)", c.PLAYLIST_NAME, playlist_id)

        log.info("done")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        msg = f"spoty_lls failed: {e}"
        notify_telegram(msg)
        log.exception(msg)
        sys.exit(1)
