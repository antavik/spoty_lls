import typing as t
from dataclasses import dataclass

import requests


class TransportError(Exception):
    """Network-layer failure — replaces requests.RequestException at the app boundary."""


@dataclass(frozen=True)
class Response:
    status_code: int
    headers: dict[str, str]
    content: bytes
    text: str


class RequestsTransport:
    def __init__(self):
        self._session = requests.Session()

    def request(
        self,
        method: str,
        url: str,
        *,
        headers: t.Optional[dict] = None,
        json: t.Optional[dict] = None,
        data: t.Optional[dict] = None,
        timeout: t.Optional[int] = None,
    ) -> Response:
        try:
            response = self._session.request(
                method, url, headers=headers, json=json, data=data, timeout=timeout
            )
            response.raise_for_status()
        except requests.RequestException as e:
            raise TransportError(str(e)) from e

        return Response(
            status_code=response.status_code,
            headers=dict(response.headers),
            content=response.content,
            text=response.text,
        )

    @staticmethod
    def raw_request(
        method: str,
        url: str,
        *,
        headers: t.Optional[dict] = None,
        json: t.Optional[dict] = None,
        data: t.Optional[dict] = None,
        timeout: t.Optional[int] = None,
    ) -> Response:
        try:
            response = requests.request(
                method, url, headers=headers, json=json, data=data, timeout=timeout
            )
            response.raise_for_status()
        except requests.RequestException as e:
            raise TransportError(str(e)) from e

        return Response(
            status_code=response.status_code,
            headers=dict(response.headers),
            content=response.content,
            text=response.text,
        )

    def set_session_headers(self, **kwargs) -> None:
        self._session.headers.update(kwargs)

    def close(self) -> None:
        self._session.close()
