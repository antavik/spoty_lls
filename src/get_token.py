#!/usr/bin/env python3
"""One-time helper to obtain a Spotify refresh token.

Usage:
    export SPOTIFY_CLIENT_ID=...
    export SPOTIFY_CLIENT_SECRET=...
    python get_token.py

Make sure the redirect URI is registered in your Spotify app settings
(Dashboard -> your app -> Settings -> Redirect URIs).
"""

import base64
import urllib.parse

import requests

import config as c


def main() -> None:
    client_id = c.CLIENT_ID
    client_secret = c.CLIENT_SECRET
    redirect_uri = c.REDIRECT_URI

    params = urllib.parse.urlencode({
        "client_id": client_id,
        "response_type": "code",
        "redirect_uri": redirect_uri,
        "scope": c.SCOPES,
    })

    print("\n1) Open this URL in your browser and click 'Agree':\n")
    print(f"{c.AUTHORIZE_URL}?{params}")
    print(
        "\n2) Your browser will redirect to a URL that may look 'broken' "
        "(page not found) - that's fine."
    )
    print("   Copy the FULL redirected URL from the address bar and paste it here.\n")

    redirected = input("Redirected URL: ").strip()
    qs = urllib.parse.parse_qs(urllib.parse.urlparse(redirected).query)
    if "code" not in qs:
        err = qs.get("error", ["unknown"])[0]

        raise SystemExit(f"no 'code' in URL (error: {err}). Full query: {qs}")

    code = qs["code"][0]
    auth = base64.b64encode(f"{client_id}:{client_secret}".encode()).decode()

    try:
        resp = requests.post(
            c.TOKEN_URL,
            data={
                "grant_type": "authorization_code",
                "code": code,
                "redirect_uri": redirect_uri,
            },
            headers={"Authorization": f"Basic {auth}"},
            timeout=c.REQUEST_TIMEOUT,
        )
        resp.raise_for_status()
    except requests.HTTPError as e:
        detail = e.response.text if e.response is not None else ""
        code_str = e.response.status_code if e.response is not None else "?"

        raise SystemExit(f"token exchange failed: HTTP {code_str}: {detail}") from None
    except requests.RequestException as e:
        raise SystemExit(f"token exchange failed: {e}") from None

    try:
        payload = resp.json()
    except ValueError:
        raise SystemExit("token exchange failed: invalid JSON response") from None

    refresh_token = payload.get("refresh_token")
    if not refresh_token:
        raise SystemExit("no 'refresh_token' in response. Check scopes and credentials.")

    print("\n=== Save this as SPOTIFY_REFRESH_TOKEN ===\n")
    print(refresh_token)
    print()


if __name__ == "__main__":
    main()
