# CCMAS – Skill-Bestand, Manifest und Snapshots

Stand: Ticket CC-35, Teil 1 (Katalog, Manifest, Snapshot, CLI). **Führend ist die Spezifikation**
„Multi Agent Setup“, Abschnitt „Skills, Regeln und Rollen“ (Entscheidungen des Projektinhabers vom 09.11. und
10.11.2026). Diese Datei hält fest, wie das Repository den Bestand führt und prüft; sie ist keine Kopie der
Spezifikation.

## Führender Bestand und Herkunftsregel

- Führend sind die Skills unter `.claude/skills/ccmas-<name>/` im CCMAS-Repository. Das Manifest
  `.claude/skills/manifest.yaml` inventarisiert sie. Eine identische Kopie der Skill-Ordner liegt im Vault unter
  `Eigene Projekte/ccmas/Skills/`; der Orchestrator zieht sie nach dem Merge nach (Regeldatei, Abschnitt 8.3).
- Die vorhandenen Skills werden **an Ort und Stelle** auf ihre endgültige CCMAS-Form angepasst; es gibt keine
  getrennten Laufzeitvarianten. Anpassungen folgen in eigenen Tickets (Spalte „Anpassung“ unten).
- Die Originalpakete liegen als ZIP-Archive unter `Fachwissen/Claude/Skills/<name>.skill` im Vault und bleiben
  unverändert. `source.package` (vault-relativ) und `source.package_sha256` sind reine **Inventarangaben**:
  Go liest zur Laufzeit nie den Vault, enthält keine Vault-Pfade im Code, und die Tests laufen in der CI ohne Vault.
- **Quelle ist immer der CCMAS-Softwareordner**, nie das Zielprojekt oder das Arbeitsverzeichnis (siehe CLI).
- Der frühere Platzhalter `skills/` (nur `.gitkeep`) ist entfallen (Entscheidung 10.11.2026). Das Ticket nannte noch
  `skills/manifest.yaml`; das gilt nicht mehr.

## Inventar (Stand der Einführung, alle `version: 1`, Ableitung 2026-11-09)

Alle Skills sind inhaltsgleich zum Original bis auf das Präfix `ccmas-` in Namen und Querverweisen. Zusätzlich:
`feature-parallel` und `neue-aufgabe-nehmen` ohne YAML-Anführungszeichen im Frontmatter, `projekt-anlegen` mit
ergänztem abschließendem Zeilenumbruch; `obsidian-seite/scripts/linkcheck.py` ist identisch.

| Skill | roles | status / Anpassung | category | default_for | external_writes | tree_sha256 |
| --- | --- | --- | --- | --- | --- | --- |
| `ccmas-concept-ticket` | concept | approved | concept | concept | asana, vault | `dbfd6ee975eefdb519d0ab8bb8d4de01c0b7ead1fe610b750978fe3bfa2fa007` |
| `ccmas-obsidian-seite` | concept, documentation | approved | knowledge | concept, documentation | vault | `1abc8b0cdaed7ac803bee01a5d37947b7b9e73f8982f87559a4b1a30f6ef78b7` |
| `ccmas-asana-ticket` | supervisor, concept, completion, question_handler | approved (für concept nur auf ausdrückliche Anforderung, daher kein Standard) | tracking | – | asana | `fd600476d4883c3ef56b58a70c1a37d61d95194d33fb98f0f29588cbc2ea4556` |
| `ccmas-regeln-pflegen` | documentation | approved (nur auf ausdrücklichen Auftrag, daher kein Standard) | rules | – | vault | `8986c0c0b04be22d478d9c270c44d90a6a1648ee5efd65c0290a6366e5d512fd` |
| `ccmas-projekt-anlegen` | project_setup | pending: Zuschnitt auf lokale Projektzuordnung (M07) | setup | – | asana, vault | `7385b9f448ec150e60749af2b8bb46edfd82ab372bdf305e6fd6fd29b03d5bf0` |
| `ccmas-neue-aufgabe-nehmen` | supervisor | pending: Anpassung an die Supervisor-Planung (CC-14) | planning | – | asana, vault | `c6c4138891c148fa20d2d09cbfe73b8a25753d424678887d279ab546e93bb9b2` |
| `ccmas-morgen-briefing` | supervisor | pending: Anpassung an die Supervisor-Planung (CC-14) | planning | – | – | `1b95c0137ffad7ae2508ab7982e22594b4208c030651e1b43ae6f190d783a92b` |
| `ccmas-feature-parallel` | implementer | pending: zentrale Delegation statt Fan-out, Review vor Push (CC-33) | delivery | – | git, github | `8313c4da97ac7af4b0ab6cecd65bc41379fb79ab8f4ef465960d04b7692f6659` |
| `ccmas-meilenstein-abschluss` | completion | pending: PR-/Auto-Modus, Feature-Abschluss, keine GitHub-Milestones und Termine (CC-33) | completion | – | asana, vault | `7df5af38caab7aa6e1c31f6bce850c1f0f30fce25476685119de087b463cd61d` |
| `ccmas-antworten-abholen` | question_handler | pending: Fragenweiterleitung über die Zentrale, PR-/Auto-Modus (CC-33) | questions | – | asana, vault | `7f79a37040ed2e7688e38fe219024b62b4bcbc1a4a9e6e582c1f10b760037cbc` |

Die Standardausstattung des Konzept-Agenten besteht nur aus `ccmas-concept-ticket` und `ccmas-obsidian-seite`, weil die
Spezifikation (Tabelle „Rollen und Ergebnisse“) nur diese beiden nennt; `ccmas-asana-ticket` bleibt für `concept`
erlaubt (`ccmas-concept-ticket` verweist darauf), wird aber nur auf ausdrückliche Anforderung bereitgestellt.

`external_writes` sagt, in welche Systeme ein Skill nach seinem Inhalt schreiben lässt. Das ist eine
Inventarangabe für die späteren Adapter und **keine Berechtigung**: Asana-, Vault- und Git-Schreibvorgänge laufen
über die kontrollierten Adapter.

## Statusbedeutung

| Status | Bedeutung |
| --- | --- |
| `approved` | Freigegeben: darf in einen Laufzeit-Snapshot. Nur approved-Skills können zur Standardausstattung (`default_for`) gehören. |
| `pending_adaptation` | Vorhanden und sichtbar (`ccmas skills list/show`), aber noch nicht an CCMAS angepasst. `Resolve` lehnt ihn mit `skill_not_approved` ab, auch bei ausdrücklicher Anforderung. `adaptation` nennt, was und wo (Ticket) angepasst wird. |

Sobald ein Anpassungsticket den Skill-Inhalt geändert hat, wird `status` auf `approved` gesetzt, `adaptation`
entfernt und `version` erhöht (Ablauf unten).

## Manifest `.claude/skills/manifest.yaml` (`schema_version: 1`)

```yaml
schema_version: 1
skills:
  - id: ccmas-concept-ticket          # = Verzeichnisname, Präfix ccmas-, nur a-z 0-9 -
    version: 1                        # Ganzzahl >= 1
    status: approved                  # approved | pending_adaptation
    # adaptation: "CC-33: …"          # Pflicht bei pending_adaptation, bei approved nicht erlaubt
    category: concept                 # planning setup concept knowledge tracking rules delivery completion questions review
    roles: [concept]                  # Rollen aus internal/domain
    default_for: [concept]            # Teilmenge von roles; nur bei approved
    external_writes: [asana, vault]   # Teilmenge von asana vault git github (Inventar, keine Berechtigung)
    tree_sha256: <hex>
    files:
      - path: SKILL.md
        sha256: <hex>
    source:
      package: Fachwissen/Claude/Skills/concept-ticket.skill   # vault-relativ
      package_sha256: <hex>
      derived: "2026-11-09"
      changes: "Kurztext der Änderungen gegenüber dem Original"
```

Das Manifest wird **strikt** gelesen (`go.yaml.in/yaml/v3`, `KnownFields`): unbekannte und doppelte Schlüssel,
ein leeres Manifest und mehrere YAML-Dokumente sind Fehler.

**Invarianten bei `skills.Load`** (jeder Verstoß ist ein eigener Befund; `ccmas skills validate` zeigt alle auf einmal):

- IDs eindeutig; Verzeichnis `.claude/skills/<id>/` existiert und ist ein echtes Verzeichnis (`missing_resource`,
  `unsafe_resource`).
- `SKILL.md` hat Frontmatter mit `name` gleich ID und nicht leerer `description` (`invalid_frontmatter`).
- Jede Datei im Verzeichnis ist gelistet (`unlisted_resource`), jede gelistete vorhanden (`missing_resource`) mit
  passendem Hash (`changed_resource`); `tree_sha256` stimmt (`invalid_manifest`).
- Jedes `ccmas-*`-Verzeichnis unter `.claude/skills/` hat einen Eintrag (`unregistered_skill`).
- Kategorien `rules` und `delivery` dürfen nie `reviewer` oder `concept_reviewer` erlauben (`invalid_manifest`).
- `default_for` ⊆ `roles`, nur bei `approved`; Rollen, Kategorien, `external_writes` aus geschlossenen Mengen;
  Pfade relativ und kanonisch (kein `..`, kein führendes `/`); Hashes 64 Hexziffern in Kleinbuchstaben;
  `derived` ein Datum `JJJJ-MM-TT`.
- Symlinks, Sonderdateien und Dateinamen mit Steuerzeichen oder ungültigem UTF-8 im Skill-Verzeichnis sind
  `unsafe_resource`. **Ignoriert wird nur `.DS_Store`** (macOS-Ordnermetadaten, in jeder Tiefe). Jede andere Datei,
  auch jede andere versteckte, muss im Manifest stehen.

Fehler haben einen stabilen Code (`skills.Code`), Skill-ID und Pfad (relativ zum Skill-Verzeichnis; bei Manifest-
und Unregistriert-Fehlern relativ zum Softwareordner) und eine deutsche Meldung. `errors.Is(err, &skills.Error{Code: …})`
prüft den Code; mehrere Befunde sind `skills.Findings`.

| Code | Bedeutung |
| --- | --- |
| `unknown_skill` | ID nicht im Manifest |
| `unregistered_skill` | `ccmas-*`-Verzeichnis ohne Manifest-Eintrag |
| `skill_not_approved` | Status ist nicht `approved` |
| `skill_not_allowed_for_role` | Rolle darf den Skill nicht erhalten |
| `missing_resource` | Verzeichnis, `SKILL.md` oder gelistete Datei fehlt |
| `changed_resource` | Dateiinhalt weicht vom Manifest-Hash ab |
| `unlisted_resource` | Datei im Verzeichnis, nicht im Manifest |
| `unsafe_resource` | Symlink, Sonderdatei oder unzulässiger Dateiname |
| `missing_manifest` / `invalid_manifest` | Manifest fehlt beziehungsweise verletzt Format oder Invarianten |
| `invalid_frontmatter` | Frontmatter der `SKILL.md` fehlt, ist fehlerhaft oder passt nicht zur ID |
| `invalid_request` | unbekannte Rolle, doppelt angeforderte Skill-ID |
| `snapshot_mismatch` | Snapshot-Verzeichnis weicht von seiner `snapshot.json` ab (veränderte, fehlende, zusätzliche Dateien) |
| `invalid_snapshot` | `snapshot.json` fehlt, ist nicht lesbar oder verletzt den Vertrag |
| `snapshot_write` | Snapshot nicht schreibbar (Dateisystem) |

## Rollenbezogene Auswahl

`(*Catalog).Resolve(role, skillIDs)`:

- leere Liste = Standardausstattung der Rolle (alle approved-Skills mit der Rolle in `default_for`); sonst genau die
  genannten Skills. Eine Rolle ohne Standardausstattung ergibt einen gültigen Snapshot ohne Skills.
- Jede ID muss existieren, `approved` sein und die Rolle erlauben (in dieser Reihenfolge geprüft).
- `Resolve` liest die Dateien der gewählten Skills **neu** und vergleicht sie mit den Manifest-Hashes; eine Änderung
  nach `Load` wird als `changed_resource`, `missing_resource` oder `unlisted_resource` erkannt. Ein Snapshot enthält
  die dabei gelesenen und geprüften Bytes, nicht eine zweite Lesung.
- Die Standardausstattung einer Rolle enthält nie einen pending-Skill; Prüfrollen erhalten nie `rules`- oder
  `delivery`-Skills (Manifest-Invariante plus Test gegen den echten Bestand).

## Hash-Definitionen (in Python identisch nachrechenbar)

- **Datei:** SHA-256 der rohen Bytes, hexadezimal in Kleinbuchstaben.
- **`tree_sha256` je Skill:** SHA-256 über die Zeilen `<datei-sha256>␠␠<relativer Pfad>\n` (zwei Leerzeichen),
  bytewise (UTF-8) nach Pfad sortiert, Pfade relativ zum Skill-Verzeichnis mit `/`. Das ist das Format von
  `shasum -a 256`; Beispiel: `cd .claude/skills/ccmas-obsidian-seite && find . -type f ! -name .DS_Store | sed 's|^\./||' | LC_ALL=C sort | xargs shasum -a 256 | shasum -a 256`.
  Bytewise Reihenfolge heißt: `SKILL.md` < `a-b.txt` < `a.txt` < `a/x.txt` (nicht verzeichnisweise).
- **`snapshot_id`:** `"sha256:" + sha256(` `"ccmas-skill-snapshot v1\n"` + `"role <rolle>\n"` + je Skill, nach ID
  sortiert, `"skill <id> <version> <tree_sha256>\n"` `)`. Kein JSON-Kanonisieren. Gleiche Auswahl und gleicher
  Inhalt ergeben dieselbe ID; Zeit und Manifest-Datei gehen nicht ein.

## Snapshot-Layout

`Snapshot.Write(dir)` legt den Snapshot atomar (Temp-Verzeichnis im selben Ordner, dann `rename`) unter
`<dir>/<snapshot_id ohne "sha256:">/` ab, im Layout eines Claude-Plugins:

```text
<hex>/
  snapshot.json                  Vertrag skill-snapshot-v1 (contracts/skill-snapshot-v1.schema.json)
  .claude-plugin/plugin.json     {"name":"ccmas"}
  skills/                        immer vorhanden, bei einer Rolle ohne Skills leer
    <id>/…                       alle Dateien des Skills
```

Danach sind Dateien `0444` und Verzeichnisse `0555` (auch das leere `skills/`). `VerifySnapshotDir` verlangt
`skills/` immer und meldet fremde Einträge darin als `snapshot_mismatch`. Existiert das Ziel bereits, wird es mit `VerifySnapshotDir`
geprüft und wiederverwendet (nie überschrieben); ein abweichendes Verzeichnis ergibt `snapshot_mismatch`.
`VerifySnapshotDir(path)` rechnet alle Hashes nach (Dateien, `tree_sha256`, `snapshot_id`) und meldet veränderte,
fehlende und zusätzliche Dateien oder Verzeichnisse sowie Symlinks; trägt das Verzeichnis den Namen einer
`snapshot_id`, muss er zur ID passen. In einem Snapshot-Verzeichnis ist auch `.DS_Store` eine zusätzliche Datei
(die Ignorierregel gilt nur für Skill-Quellverzeichnisse). Dateirechte werden nicht geprüft.

`snapshot.json` enthält `schema_version`, `snapshot_id`, `role`, `created_at` (UTC, Sekunden),
`manifest_sha256` (Prüfsumme der Manifest-Datei, aus der aufgelöst wurde; Herkunftsangabe, nicht Teil der ID) und
je Skill `id`, `version`, `tree_sha256`, `files[]` (`path`, `sha256`) und `source` (`package`, `package_sha256`).
Keine absoluten Pfade, keine persönlichen Daten. Wurde ein Snapshot unter derselben ID früher geschrieben, bleibt
dessen `snapshot.json` (mit dem damaligen `created_at` und `manifest_sha256`) bestehen; der Skill-Inhalt ist
nachweislich derselbe.

**Unveränderlichkeit:** `Snapshot` ist ein Wert mit eigenen Kopien der Dateiinhalte; Getter (`SkillIDs`, `File`,
`Document`) liefern Kopien. Spätere Änderungen an Quelldateien, Manifest oder Katalog ändern einen bestehenden
Snapshot weder im Speicher noch im Verzeichnis. Ein neuer Stand ergibt eine neue ID und ein neues Verzeichnis.

**Gemeinsame Beispiel-Struktur:** `contracts/fixtures/skill-snapshots/example/` ist ein vollständiges Snapshot-
Verzeichnis aus einem kleinen Test-Katalog. `TestExampleSnapshotFixture` erzeugt es neu und vergleicht Byte für
Byte (mit `-update` neu schreiben); `VerifySnapshotDir` akzeptiert es. Die Python-Seite rechnet daran in W35.2 dieselben
Hashes nach. Git speichert keine Dateirechte; ausgechecktes Fixture-Material hat deshalb normale Rechte.

## Ablauf bei einer Skill-Änderung

1. Datei(en) unter `.claude/skills/ccmas-<name>/` ändern (Branch/PR nach `develop`).
2. `version` des Skills im Manifest erhöhen; bei abgeschlossener Anpassung `status: approved` setzen und
   `adaptation` entfernen.
3. Hashes im Manifest nachziehen: `ccmas skills validate` nennt je Abweichung Datei, erwarteten und tatsächlichen
   Hash; `tree_sha256` mit dem `shasum`-Beispiel oben berechnen.
4. `ccmas skills validate` muss ohne Befund enden (`make test` prüft den echten Bestand in
   `TestRepoManifestMatchesFiles`).
5. Nach dem Merge spiegelt der Orchestrator die Skill-Ordner in den Vault (`Eigene Projekte/ccmas/Skills/`,
   Regeldatei 8.3); `source.changes` dokumentiert die Abweichung vom Originalpaket.

Laufende Ausführungen bleiben bei ihrem Snapshot; ein geänderter Bestand gilt erst für nachfolgende Auflösungen.

## CLI

`ccmas skills list [--role R] [--json]`, `show <id> [--json]`, `validate [--json]`,
`snapshot --role R [--skill ID …] --out DIR [--json]`.

Der Softwareordner ergibt sich aus `--root`, dann der Umgebungsvariable `CCMAS_HOME`, dann dem Verzeichnis über dem
Binary (`<exe-dir>/..`, Symlinks aufgelöst, nur wenn dort `.claude/skills/manifest.yaml` liegt); sonst endet der
Befehl mit einer verständlichen Meldung. Das Arbeitsverzeichnis und ein Zielprojekt werden nie gelesen.

Exit-Codes: 0 Erfolg; 1 Befund oder Laufzeitfehler (`validate` mit Befunden, Resolve-Fehler, unbekannter Skill,
Softwareordner nicht gefunden); 2 Bedienfehler (unbekannte Rolle, fehlende Pflichtoption, unerwartetes Argument).
Fehlermeldungen gehen nach stderr, bei Skill-Fehlern mit dem Code in eckigen Klammern; reguläre Ausgaben nach stdout.
`validate --json` schreibt den Bericht (`ok`, `findings[]` mit `code`, `skill`, `path`, `message`) auch bei Befunden
nach stdout und endet dann mit Exit-Code 1.

## Offene Punkte (W35.2)

- **Laufzeit-Einbringung:** Der Python-Runner stellt den Snapshot der Ausführung bereit (Plugin-Pfad, nur die
  gewählten Skills), rechnet die Hashes aus diesem Dokument nach und weist Abweichungen ab. Live-Nachweis, dass nur
  die ausgewählten Skills sichtbar sind.
- **Anbindung an Profile und Decision:** `AgentProfile.skill_ids` und `Decision.selection.skill_ids` werden vom
  Orchestrator über `Resolve` aufgelöst; der heutige `domain.Catalog.Skills` (reine ID-Menge) wird aus dem Katalog
  gespeist.
- **Anpassungen der pending-Skills:** CC-33 (`feature-parallel`, `meilenstein-abschluss`, `antworten-abholen`),
  CC-14 (`neue-aufgabe-nehmen`, `morgen-briefing`), M07 (`projekt-anlegen`); neue Skills (Supervisor, Konzeptprüfer,
  Reviewer) entstehen in eigenen Tickets und erhalten dann `review`-Einträge und Standardausstattungen für die
  Prüfrollen.
- **Regeln:** `ccmas rules` bleibt geplant.
