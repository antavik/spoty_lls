import base64
import functools
import json
import logging
import time
import typing as t
from collections.abc import Callable, Iterator
from itertools import batched

import config as c
from transport import RequestsTransport, TransportError

log = logging.getLogger("spoty_lls.spotify")


class SpotifyError(RuntimeError):
    """Raised when a Spotify API call fails unrecoverably."""


class RetryableError(SpotifyError):
    """Raised when a Spotify API call fails but may succeed if retried."""

    def __init__(self, message: str, retry_after: t.Optional[int] = None) -> None:
        super().__init__(message)

        self.retry_after = retry_after


class RateLimitError(RetryableError):
    """HTTP 429 rate limit exceeded."""


class ServerError(RetryableError):
    """HTTP 5xx server error."""


class AuthExpiredError(SpotifyError):
    """HTTP 401 - access token expired or revoked.

    Not retryable by delay; recover by calling client.authenticate().
    """


P = t.ParamSpec("P")
R = t.TypeVar("R")


def retry(
    max_attempts: int = c.MAX_RETRIES,
    base_delay: int = c.RETRY_DELAY,
    max_wait: int = c.MAX_RETRY_WAIT,
) -> Callable[[Callable[P, R]], Callable[P, R]]:
    def decorator(func: Callable[P, R]) -> Callable[P, R]:
        @functools.wraps(func)
        def wrapper(*args: P.args, **kwargs: P.kwargs) -> R:
            attempt = 1
            while True:
                try:
                    return func(*args, **kwargs)
                except RetryableError as e:
                    if attempt >= max_attempts:
                        raise

                    wait = min(
                        base_delay if e.retry_after is None else e.retry_after,
                        max_wait,
                    )

                    log.warning(
                        "%s failed (%s); retry %d/%d in %ds",
                        func.__name__,
                        e,
                        attempt,
                        max_attempts,
                        wait,
                    )

                    time.sleep(wait)
                except TransportError as e:
                    if attempt >= max_attempts:
                        raise SpotifyError(f"network error: {e}") from None

                    log.warning(
                        "%s network error; retry %d/%d",
                        func.__name__,
                        attempt,
                        max_attempts,
                    )

                    time.sleep(base_delay)

                attempt += 1

        return wrapper

    return decorator


def reauth_on_expiry(func: Callable[P, R]) -> Callable[P, R]:
    @functools.wraps(func)
    def wrapper(*args: P.args, **kwargs: P.kwargs) -> R:
        client = args[0]
        try:
            return func(*args, **kwargs)
        except AuthExpiredError:
            log.info("401 received; refreshing token and retrying once")

            client.authenticate()

            return func(*args, **kwargs)

    return wrapper


class SpotifyClient:
    def __init__(
        self,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> None:
        self._client_id = client_id
        self._client_secret = client_secret
        self._refresh_token = refresh_token
        if not self._refresh_token:
            raise SpotifyError("SPOTIFY_REFRESH_TOKEN not set")

        self._transport = RequestsTransport()
        self._token: str | None = None

    def authenticate(self) -> None:
        auth = base64.b64encode(f"{self._client_id}:{self._client_secret}".encode()).decode()

        try:
            response = self._transport.raw_request(
                "POST",
                c.TOKEN_URL,
                headers={
                    "Authorization": f"Basic {auth}",
                    "Content-Type": "application/x-www-form-urlencoded",
                },
                data={
                    "grant_type": "refresh_token",
                    "refresh_token": self._refresh_token,
                },
                timeout=c.REQUEST_TIMEOUT,
            )
        except TransportError as e:
            raise SpotifyError(f"token refresh failed: {e}") from None

        code = response.status_code
        if code >= 400:
            detail = response.text if response.content else ""
            raise SpotifyError(f"token refresh failed: HTTP {code}: {detail}")

        try:
            payload = json.loads(response.content)
        except ValueError as e:
            raise SpotifyError("token refresh failed: invalid JSON response") from e

        token = payload.get("access_token")
        if not token:
            raise SpotifyError("token refresh failed: no access_token in response")

        self._token = token
        self._transport.set_session_headers(Authorization=f"Bearer {token}")

    def current_user_id(self) -> str:
        result = self._request("GET", f"{c.API_BASE}/me")
        user_id = result.get("id")
        if not user_id:
            raise SpotifyError("unexpected response from /me: missing 'id'")

        return user_id

    def liked_track_uris(self, limit: int = c.LIKED_LIMIT) -> list[str]:
        uris: list[str] = []
        for data in self._paginate(f"{c.API_BASE}/me/tracks?limit={c.PAGE}"):
            for item in data.get("items", []):
                track = item.get("track")
                if track and track.get("uri"):
                    uris.append(track["uri"])

                if len(uris) >= limit:
                    return uris

        return uris

    def find_playlist(self, name: str, owner_id: str) -> tuple[str, str | None] | None:
        for data in self._paginate(f"{c.API_BASE}/me/playlists?limit={c.PAGE}"):
            for pl in data.get("items", []):
                if pl.get("name") == name and pl.get("owner", {}).get("id") == owner_id:
                    return pl["id"], pl.get("description")

        return None

    def create_playlist(self, owner_id: str, name: str) -> str:
        result = self._request(
            "POST",
            f"{c.API_BASE}/users/{owner_id}/playlists",
            data={
                "name": name,
                "public": False,
            },
        )

        if not (playlist_id := result.get("id")):
            raise SpotifyError("unexpected response from create playlist: missing 'id'")

        return playlist_id

    def change_details(
        self,
        playlist_id: str,
        *,
        name: t.Optional[str] = None,
        description: t.Optional[str] = None,
        public: t.Optional[bool] = None,
    ) -> None:
        body = {
            k: v
            for k, v in (
                ("name", name),
                ("description", description),
                ("public", public),
            )
            if v is not None
        }
        if not body:
            return

        self._request("PUT", f"{c.API_BASE}/playlists/{playlist_id}", data=body)

    def replace_tracks(self, playlist_id: str, uris: list[str]) -> None:
        if not uris:
            log.warning("replace_tracks called with empty uris - skipping")

            return

        result = self._request(
            "PUT",
            f"{c.API_BASE}/playlists/{playlist_id}/tracks",
            data={"uris": uris[: c.BATCH]},
        )

        if snapshot := result.get("snapshot_id"):
            log.info("replace_tracks snapshot_id=%s", snapshot)

        for chunk in batched(uris[c.BATCH :], c.BATCH, strict=False):
            self._request(
                "POST",
                f"{c.API_BASE}/playlists/{playlist_id}/tracks",
                data={"uris": chunk},
            )

    def close(self) -> None:
        self._transport.close()

    @reauth_on_expiry
    @retry()
    def _request(self, method: str, url: str, data: dict | None = None) -> dict:
        if self._token is None:
            raise SpotifyError("client not authenticated")

        resp = self._transport.request(method, url, json=data, timeout=c.REQUEST_TIMEOUT)

        code = resp.status_code
        if code == 401:
            raise AuthExpiredError(f"{method} {url} -> HTTP 401")

        if code == 429:
            try:
                wait = int(resp.headers.get("Retry-After", "2"))
            except ValueError:
                wait = 2

            raise RateLimitError(f"{method} {url} -> HTTP 429", retry_after=wait)

        if 500 <= code < 600:
            raise ServerError(f"{method} {url} -> HTTP {code}: {resp.text}")

        if code >= 400:
            raise SpotifyError(f"{method} {url} -> HTTP {code}: {resp.text}")

        if not resp.content:
            return {}

        try:
            return json.loads(resp.content)
        except ValueError as e:
            raise SpotifyError(f"{method} {url} -> invalid JSON response") from e

    def _paginate(self, url: str) -> Iterator[dict]:
        while url:
            data = self._request("GET", url)

            yield data

            url = data.get("next")

    def __enter__(self) -> "SpotifyClient":
        self.authenticate()

        return self

    def __exit__(self, *exc_info: object) -> None:
        self.close()
