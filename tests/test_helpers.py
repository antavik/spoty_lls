import hashlib
import re
from datetime import datetime, timezone
from unittest.mock import patch

import pytest

from helpers import (
    build_description,
    compute_uris_hash,
    parse_hash,
    str2bool,
)

HEX12 = re.compile(r"^[0-9a-f]{12}$")
FIXED_DT = datetime(2026, 7, 28, 9, 5, tzinfo=timezone.utc)
EXPECTED_TS = "2026-07-28 09:05"


# --------------------------------------------------------------------------- #
# compute_uris_hash                                                           #
# --------------------------------------------------------------------------- #
@pytest.mark.parametrize(
    "uris,expected",
    [
        pytest.param([],         "e3b0c44298fc", id="empty-list"),
        pytest.param(["a", "b"], "fb8e20fc2e4c", id="multi"),
    ],
)
def test_compute_uris_hash_golden(uris, expected):
    got = compute_uris_hash(uris)

    assert HEX12.match(got)
    assert got == expected


@pytest.mark.parametrize(
    "uris",
    [
        pytest.param(["spotify:track:1"], id="single"),
        pytest.param(["x", "y", "z"],     id="multi"),
        pytest.param(["ünïcodé", "🎵"],   id="unicode"),
    ],
)
def test_compute_uris_hash_shape(uris):
    assert HEX12.match(compute_uris_hash(uris))


def test_compute_uris_hash_deterministic():
    assert compute_uris_hash(["a", "b"]) == compute_uris_hash(["a", "b"])


def test_compute_uris_hash_order_sensitive():
    assert compute_uris_hash(["a", "b"]) != compute_uris_hash(["b", "a"])


def test_compute_uris_hash_join_collision():
    # KNOWN behavior: concatenation loses element boundaries
    assert compute_uris_hash(["a", "b"]) == compute_uris_hash(["ab"])


def test_compute_uris_hash_matches_hashlib():
    uris = ["spotify:track:1", "spotify:track:2"]
    expected = hashlib.sha256("".join(uris).encode()).hexdigest()[:12]

    assert compute_uris_hash(uris) == expected


# --------------------------------------------------------------------------- #
# parse_hash                                                                  #
# --------------------------------------------------------------------------- #
@pytest.mark.parametrize(
    "desc, expected",
    [
        pytest.param(None,                      None, id="none"),
        pytest.param("",                        None, id="empty-falsy"),
        pytest.param("no hash here",            None, id="no-bracket"),
        pytest.param("[#0123456789a]",          None, id="too-short-11"),
        pytest.param("[#0123456789abc]",        None, id="too-long-13"),
        pytest.param("[#0123456789AB]",         None, id="uppercase-rejected"),
        pytest.param("[#0123456789zz]",         None, id="non-hex"),
        pytest.param("[0123456789ab]",          None, id="missing-hash"),
        pytest.param("0123456789ab",            None, id="missing-brackets"),
        pytest.param("[#0123456789ab]",                "0123456789ab", id="valid-exact"),
        pytest.param("txt [#0123456789ab] txt",        "0123456789ab", id="valid-embedded"),
        pytest.param("[#aaaaaaaaaaaa][#bbbbbbbbbbbb]", "aaaaaaaaaaaa", id="first-match-wins"),
    ],
)
def test_parse_hash(desc, expected):
    assert parse_hash(desc) == expected


# --------------------------------------------------------------------------- #
# build_description                                                           #
# --------------------------------------------------------------------------- #
@patch('helpers.datetime')
@pytest.mark.parametrize(
    "count, digest, expected",
    [
        pytest.param(
            0,
            "0123456789ab",
            f"0 most recently liked songs. Auto-updated {EXPECTED_TS} UTC. [#0123456789ab]",
            id="zero",
        ),
        pytest.param(
            50,
            "aaaaaaaaaaaa",
            f"50 most recently liked songs. Auto-updated {EXPECTED_TS} UTC. [#aaaaaaaaaaaa]",
            id="fifty",
        ),
    ],
)
def test_build_description_exact(mock_datetime, count, digest, expected):
    mock_datetime.now.return_value = FIXED_DT

    assert build_description(count, digest) == expected


@patch('helpers.datetime')
def test_build_description_roundtrip_with_parse_hash(mock_datetime):
    mock_datetime.now.return_value = FIXED_DT
    desc = build_description(10, "0123456789ab")

    assert parse_hash(desc) == "0123456789ab"


# --------------------------------------------------------------------------- #
# str2bool                                                                    #
# --------------------------------------------------------------------------- #
@pytest.mark.parametrize(
    "val, expected",
    [
        pytest.param(True,       True,  id="bool-true"),
        pytest.param(False,      False, id="bool-false"),
        pytest.param("  true  ", True,  id="strip-true"),
        pytest.param("   ",      False, id="whitespace-only-to-empty-false"),
        *[(v, True) for v in ("1", "yes", "Yes", "YES", "y", "Y", "true", "True", "TRUE", "t")],
        *[(v, False) for v in ("0", "no", "No", "NO", "n", "N", "false", "False", "FALSE", "f", "")],
    ],
)
def test_str2bool_ok(val, expected):
    assert str2bool(val) is expected


@pytest.mark.parametrize(
    "val",
    [
        pytest.param("maybe", id="unknown-word"),
        pytest.param("2",     id="unknown-number"),
        pytest.param("tru",   id="partial"),
    ],
)
def test_str2bool_valueerror(val):
    with pytest.raises(ValueError):
        str2bool(val)


@pytest.mark.parametrize(
    "val",
    [
        pytest.param(1,     id="int"),
        pytest.param(None,  id="none"),
        pytest.param(1.0,   id="float"),
        pytest.param(["x"], id="list"),
    ],
)
def test_str2bool_typeerror(val):
    with pytest.raises(TypeError):
        str2bool(val)
