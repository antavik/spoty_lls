import hashlib
import re

from datetime import datetime, timezone

_HASH_RE = re.compile(r"\[#([0-9a-f]{12})\]")


def compute_uris_hash(uris: list[str]) -> str:
    return hashlib.sha256("".join(uris).encode()).hexdigest()[:12]


def parse_hash(description: str | None) -> str | None:
    if not description:
        return None

    m = _HASH_RE.search(description)

    return m and m.group(1)


def build_description(count: int, digest: str) -> str:
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M")

    return f"{count} most recently liked songs. Auto-updated {ts} UTC. [#{digest}]"


class _StrToBool:
    _true_vals = {"1", "yes", "Yes", "YES", "y", "Y", "true", "True", "TRUE", "t"}
    _false_vals = {"0", "no", "No", "NO", "n", "N", "false", "False", "FALSE", "f", ""}

    def __call__(self, v: str) -> bool:
        if isinstance(v, bool):
            return v

        if not isinstance(v, str):
            raise TypeError("Invalid data type to cast")

        v = v.strip()

        if v in self._true_vals:
            return True
        elif v in self._false_vals:
            return False
        else:
            raise ValueError("Unsupported string value for common notation")


str2bool = _StrToBool()
