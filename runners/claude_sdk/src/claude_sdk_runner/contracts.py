"""Vertragsprüfung des Runners gegen die gemeinsamen JSON-Schemas.

Go (``internal/domain``) und Python prüfen gegen **dieselben** Schema-Dateien unter
``contracts/`` (Draft 2020-12). Dieses Modul enthält bewusst keine zweite Typdefinition:
es löst den Pfad der Schemas auf, prüft eine Nachricht beziehungsweise ein Dokument strikt
und liefert bei einem Verstoß die Fehlerklasse zurück. Die Regeln stehen in
``docs/architecture/contracts.md``.

Pfadauflösung: ``contracts_dir()`` liefert ``<Repo-Wurzel>/contracts`` relativ zu dieser
Datei (Quellbaum und editierbare Installation). Für andere Ablageorte setzt man die
Umgebungsvariable ``CCMAS_CONTRACTS_DIR``.

Der Import führt keine Agenten aus und greift weder auf das Netzwerk noch auf ein Konto zu.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path
from typing import Any

import jsonschema
from jsonschema.exceptions import ValidationError
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

# Prüfziele: Name (zugleich Fixture-Präfix) -> (Schema-Datei, Definition unter $defs).
# Die Namen stimmen mit internal/domain.Target überein.
TARGETS: dict[str, tuple[str, str]] = {
    "to-runner": ("runner-v1.schema.json", "envelope_to_runner"),
    "from-runner": ("runner-v1.schema.json", "envelope_from_runner"),
    "project-config": ("project-v1.schema.json", "project_config"),
    "agent-profile": ("project-v1.schema.json", "agent_profile"),
    "work-order": ("project-v1.schema.json", "work_order"),
    "decision": ("decision-v1.schema.json", "decision"),
    "integration-request": ("decision-v1.schema.json", "integration_request"),
    "review-report": ("runner-v1.schema.json", "review_report"),
}

_MAX_NESTING_NOTE = "Verschachtelung zu tief"


class ContractError(ValueError):
    """Vertragsverstoß mit differenzierter Fehlerklasse.

    ``categories`` enthält die eindeutigen Klassen aller gefundenen Verstöße, alphabetisch
    sortiert; ``category`` ist die erste. Dieselbe Regel gilt in Go.
    """

    def __init__(self, categories: list[str] | tuple[str, ...], message: str, location: str = "") -> None:
        self.categories: tuple[str, ...] = tuple(sorted(set(categories)))
        self.location = location
        self.message = message
        super().__init__(f"vertragsverstoß [{','.join(self.categories)}] bei {location or '/'}: {message}")

    @property
    def category(self) -> str:
        return self.categories[0]


def contracts_dir() -> Path:
    """Verzeichnis der Schema-Dateien (eine Quelle der Wahrheit, keine Kopie im Paket)."""
    override = os.environ.get("CCMAS_CONTRACTS_DIR")
    if override:
        return Path(override)
    # runners/claude_sdk/src/claude_sdk_runner/contracts.py -> Repository-Wurzel
    return Path(__file__).resolve().parents[4] / "contracts"


@dataclass(frozen=True)
class _Loaded:
    registry: Registry
    validators: dict[str, jsonschema.protocols.Validator]
    max_message_bytes: int
    categories: tuple[str, ...]


@lru_cache(maxsize=None)
def _load(directory: str) -> _Loaded:
    base = Path(directory)
    files = sorted(base.glob("*.schema.json"))
    if not files:
        raise FileNotFoundError(
            f"keine Schema-Dateien in {base}; CCMAS_CONTRACTS_DIR setzen oder aus dem Repository ausführen"
        )
    docs: dict[str, Any] = {}
    resources = []
    for f in files:
        doc = json.loads(f.read_text(encoding="utf-8"))
        jsonschema.Draft202012Validator.check_schema(doc)
        docs[f.name] = doc
        resources.append((doc["$id"], Resource.from_contents(doc, default_specification=DRAFT202012)))
    registry = Registry().with_resources(resources)
    validators = {}
    for target, (file, definition) in TARGETS.items():
        schema = {"$ref": f"{docs[file]['$id']}#/$defs/{definition}"}
        validators[target] = jsonschema.Draft202012Validator(schema, registry=registry)
    return _Loaded(
        registry=registry,
        validators=validators,
        max_message_bytes=int(docs["runner-v1.schema.json"]["x-max-message-bytes"]),
        categories=tuple(docs["common-v1.schema.json"]["x-error-classes"]),
    )


def _loaded() -> _Loaded:
    return _load(str(contracts_dir()))


def max_message_bytes() -> int:
    """Größtes zulässiges Nachrichtenvolumen in Bytes (x-max-message-bytes im Schema)."""
    return _loaded().max_message_bytes


def categories() -> tuple[str, ...]:
    """Alle von Python erzeugten Fehlerklassen (x-error-classes in common-v1)."""
    return _loaded().categories


def __getattr__(name: str) -> Any:
    # Modulattribute, die erst nach dem Laden der Schemas bekannt sind.
    if name == "MAX_MESSAGE_BYTES":
        return max_message_bytes()
    if name == "CATEGORIES":
        return categories()
    raise AttributeError(name)


class _FloatNumber(Exception):
    """Kommazahl oder Exponentenschreibweise: kein Syntaxfehler, sondern ein Wertfehler."""


def _no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key, value in pairs:
        if key in out:
            raise ValueError(f"doppelter Schlüssel {key!r}")
        out[key] = value
    return out


def _reject_float(text: str) -> Any:
    raise _FloatNumber(text)


def _reject_constant(text: str) -> Any:
    raise ValueError(f"ungültige Zahl {text}")


def _parse_strict(data: bytes) -> dict[str, Any]:
    """Strikter JSON-Parser: UTF-8, genau ein Objekt, keine Folgedaten, keine doppelten
    Schlüssel, keine NaN/Infinity, keine Kommazahlen (alle Zahlen der Verträge sind Ganzzahlen)."""
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ContractError(["syntax"], f"ungültiges UTF-8: {exc.reason}") from None
    try:
        value = json.loads(
            text,
            object_pairs_hook=_no_duplicates,
            parse_float=_reject_float,
            parse_constant=_reject_constant,
        )
    except _FloatNumber as exc:
        raise ContractError(
            ["value"], f"Zahl {exc}: nur Ganzzahlen ohne Nachkomma- und Exponentenschreibweise sind zulässig"
        ) from None
    except RecursionError:
        raise ContractError(["syntax"], _MAX_NESTING_NOTE) from None
    except ValueError as exc:  # JSONDecodeError ist ein ValueError
        raise ContractError(["syntax"], f"kein gültiges JSON: {exc}") from None
    if not isinstance(value, dict):
        raise ContractError(["syntax"], "ein JSON-Objekt wird erwartet")
    return value


def _escape(part: object) -> str:
    return str(part).replace("~", "~0").replace("/", "~1")


def _location(error: ValidationError) -> str:
    parts = [_escape(p) for p in error.absolute_path]
    return "/" + "/".join(parts) if parts else ""


def _leaf_category(error: ValidationError) -> str:
    """Klasse eines Blattfehlers; dieselbe Regel wie internal/domain (schema.go, leafCategory)."""
    if error.validator == "required":
        return "field"
    if error.validator in ("additionalProperties", "unevaluatedProperties"):
        return "unknown_field"
    annotation = error.schema.get("x-error-class") if isinstance(error.schema, dict) else None
    if isinstance(annotation, str) and annotation:
        return annotation
    if error.validator in ("maxLength", "maxItems", "maxProperties"):
        return "size"
    return "value"


def _classify(errors: list[ValidationError]) -> ContractError:
    cats = [_leaf_category(e) for e in errors]
    messages = sorted(f"{_location(e) or '/'} verletzt {e.validator}" for e in errors)
    return ContractError(cats, "; ".join(messages), _location(errors[0]))


def validate(target: str, raw: bytes | str) -> dict[str, Any]:
    """Prüft ``raw`` strikt gegen den Vertrag ``target`` und liefert das geparste Objekt.

    Reihenfolge: Größe, JSON-Syntax, Schema. Bei einem Verstoß wird ``ContractError`` mit
    Fehlerklasse ausgelöst. Ein unbekanntes ``target`` löst ``KeyError`` aus.
    """
    loaded = _loaded()
    validator = loaded.validators[target]
    try:
        data = raw.encode("utf-8") if isinstance(raw, str) else bytes(raw)
    except UnicodeEncodeError:
        raise ContractError(["syntax"], "ungültiges UTF-8") from None
    if len(data) > loaded.max_message_bytes:
        raise ContractError(
            ["size"], f"Nachricht hat {len(data)} Bytes, erlaubt sind höchstens {loaded.max_message_bytes}"
        )
    instance = _parse_strict(data)
    errors = list(validator.iter_errors(instance))
    if errors:
        raise _classify(errors)
    return instance
