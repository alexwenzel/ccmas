"""Gemeinsame Testkonfiguration: Live-Tests sind standardmäßig übersprungen."""

import os

import pytest


def pytest_collection_modifyitems(config, items):
    if os.environ.get("CCMAS_LIVE_TESTS") == "1":
        return
    skip = pytest.mark.skip(reason="Live-Test: nur mit CCMAS_LIVE_TESTS=1 (echter SDK-Lauf, verbraucht Abo-Kontingent)")
    for item in items:
        if "live" in item.keywords:
            item.add_marker(skip)
