"""Gemeinsame Testkonfiguration: Live-Tests sind standardmäßig übersprungen."""

import os

import pytest

import support


def pytest_collection_modifyitems(config, items):
    if os.environ.get("CCMAS_LIVE_TESTS") == "1":
        return
    skip = pytest.mark.skip(reason="Live-Test: nur mit CCMAS_LIVE_TESTS=1 (echter SDK-Lauf, verbraucht Abo-Kontingent)")
    for item in items:
        if "live" in item.keywords:
            item.add_marker(skip)


@pytest.fixture(autouse=True)
def _hangs_must_be_cancelled_by_the_runner():
    """Ein absichtlicher Hänger der Test-Doubles muss vom Runner abgebrochen werden; läuft er ab, ist der Test rot."""
    support.EXPIRED_HANGS.clear()
    yield
    assert not support.EXPIRED_HANGS, f"nicht abgebrochene Hänger: {support.EXPIRED_HANGS}"
