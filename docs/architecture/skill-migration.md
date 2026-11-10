# CCMAS – Skill-Bestand, Manifest und Snapshots

Stand: Ticket CC-35, Teil 1 (Katalog, Manifest, Snapshot, CLI) und Teil 2 (Einbringung in die Laufzeit), ergänzt um
CC-33, Teil 1 (Workflow-Verträge, Rollenregeln, Prüfskills; siehe [`workflow-rules.md`](workflow-rules.md)) und
Teil 2 (W33.2: `feature-parallel`, `meilenstein-abschluss` und `antworten-abholen` an Ort und Stelle angepasst). **Führend ist die Spezifikation**
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

## Inventar (Stand CC-33, Teil 2)

Die zehn ursprünglichen Skills sind inhaltsgleich zum Original bis auf das Präfix `ccmas-` in Namen und Querverweisen
(Ableitung 2026-11-09, Version 1). Zusätzlich: `feature-parallel` und `neue-aufgabe-nehmen` ohne YAML-Anführungszeichen im
Frontmatter, `projekt-anlegen` mit ergänztem abschließendem Zeilenumbruch; `obsidian-seite/scripts/linkcheck.py` ist
identisch. Mit CC-33 kamen hinzu: die Datei `contract.yaml` bei den vier ursprünglichen Workflow-Skills (Version 2) und die
zwei neuen Prüfskills `ccmas-concept-review` und `ccmas-code-review` (Version 1, Ableitung 2026-11-10, ohne Originalpaket).
Bei `ccmas-concept-ticket` änderte sich außerdem die `SKILL.md` (Einstufung routine/strategisch, Konzeptprüfung,
Ausführungskontext; Version 3 nach dem Review: Status `review` bis zur Freigabe des Konzeptprüfers beziehungsweise
Projektinhabers, Routineweg nach FREIGEGEBEN und BEFUNDE). `ccmas-concept-review` Version 2: Bericht bezieht sich auf
die Konzeptversion, `commit` ist nur Bezugsstand; Konzeptschleife mit dem Reparaturbudget der Code-Schleife.

Mit W33.2 (Entscheidung des Projektinhabers vom 10.11.2026: Anpassung an Ort und Stelle, keine englischen Doppelungen,
keine neuen Skills) wurden `ccmas-feature-parallel`, `ccmas-meilenstein-abschluss` (zugleich Feature-Abschluss) und
`ccmas-antworten-abholen` auf ihre CCMAS-Form gebracht: Version 3, `approved`, `default_for` jeweils ihre Rolle. Neu
in den Texten: Ausführungskontext (CCMAS: Plattform führt Schreibaktionen über Adapter aus und startet allein
Ausführungen; Entwicklungssitzung: Hauptsitzung nach Regeldatei und Repo-`CLAUDE.md`); `feature-parallel` ohne eigenes
Fan-out und ohne `gh pr merge`, mit Review vor jedem Push; `meilenstein-abschluss` ohne GitHub-Milestones, Termine und
Verschieben (Regel 7.7); `antworten-abholen` mit dem Fragenweg über die Zentrale. `ccmas-obsidian-seite` ist zusätzlich für
die Rollen `completion` und `question_handler` erlaubt (nicht als Standard, Inhalt und Version unverändert), weil die
Verträge von Abschluss und Fragenbearbeitung sie als `uses_skills` nennen.

| Skill | Version | roles | status / Anpassung | category | default_for | external_writes | tree_sha256 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `ccmas-concept-ticket` | 3 | concept | approved | concept | concept | asana, vault | `aa4539d916264dde641aa64951e3e6636fbfdd5d4b9ae16d7ebfed56d1f8d162` |
| `ccmas-concept-review` | 2 | concept_reviewer | approved (neu in CC-33) | review | concept_reviewer | – | `6b14e2d3c2c3f33b7e15e1cd5574c8732744f7255fe73946dbf0578c2acf7898` |
| `ccmas-code-review` | 1 | reviewer | approved (neu in CC-33) | review | reviewer | – | `60797c5af7f5d43ced4827b6d668911da9b58343f17fb2cfee3f545fce300c9d` |
| `ccmas-obsidian-seite` | 1 | concept, documentation, completion, question_handler | approved (completion und question_handler erlaubt, kein Standard) | knowledge | concept, documentation | vault | `1abc8b0cdaed7ac803bee01a5d37947b7b9e73f8982f87559a4b1a30f6ef78b7` |
| `ccmas-asana-ticket` | 1 | supervisor, concept, completion, question_handler | approved (für concept nur auf ausdrückliche Anforderung, daher kein Standard) | tracking | – | asana | `fd600476d4883c3ef56b58a70c1a37d61d95194d33fb98f0f29588cbc2ea4556` |
| `ccmas-regeln-pflegen` | 1 | documentation | approved (nur auf ausdrücklichen Auftrag, daher kein Standard) | rules | – | vault | `8986c0c0b04be22d478d9c270c44d90a6a1648ee5efd65c0290a6366e5d512fd` |
| `ccmas-projekt-anlegen` | 1 | project_setup | pending: Zuschnitt auf lokale Projektzuordnung (M07) | setup | – | asana, vault | `7385b9f448ec150e60749af2b8bb46edfd82ab372bdf305e6fd6fd29b03d5bf0` |
| `ccmas-neue-aufgabe-nehmen` | 1 | supervisor | pending: Anpassung an die Supervisor-Planung (CC-14) | planning | – | asana, vault | `c6c4138891c148fa20d2d09cbfe73b8a25753d424678887d279ab546e93bb9b2` |
| `ccmas-morgen-briefing` | 1 | supervisor | pending: Anpassung an die Supervisor-Planung (CC-14) | planning | – | – | `1b95c0137ffad7ae2508ab7982e22594b4208c030651e1b43ae6f190d783a92b` |
| `ccmas-feature-parallel` | 3 | implementer | approved (W33.2) | delivery | implementer | git | `5a6f547cacb7b9caf04272d47a4c916bfcbf0f0c2ccbe065b59e978a12d122ec` |
| `ccmas-meilenstein-abschluss` | 3 | completion | approved (W33.2) | completion | completion | asana, vault | `221578e620cdd0086555b8af87300a92cd1be8886135f0a64b5243baaf675417` |
| `ccmas-antworten-abholen` | 3 | question_handler | approved (W33.2) | questions | question_handler | asana, vault | `1f31038da696f751edab4c04496d0e8d04b143c32875b96e7863804731cf3a44` |

Workflow-Skills sind die sechs Skills der Kategorien `concept`, `review`, `delivery`, `completion` und `questions`; sie
haben eine `contract.yaml` (Aufbau und Prüfung: [`workflow-rules.md`](workflow-rules.md)). Hilfsskills haben keinen Vertrag.

Die Standardausstattung des Konzept-Agenten besteht nur aus `ccmas-concept-ticket` und `ccmas-obsidian-seite`, weil die
Spezifikation (Tabelle „Rollen und Ergebnisse“) nur diese beiden nennt; `ccmas-asana-ticket` bleibt für `concept`
erlaubt (`ccmas-concept-ticket` verweist darauf), wird aber nur auf ausdrückliche Anforderung bereitgestellt. Der Konzeptprüfer
erhält `ccmas-concept-review`, der Reviewer `ccmas-code-review`; beide sind reine Lese-/Prüfskills ohne Schreibzugriffe.
Seit W33.2 erhalten außerdem der Implementierer `ccmas-feature-parallel`, die Abschlussrolle `ccmas-meilenstein-abschluss`
und die Fragenbearbeitung `ccmas-antworten-abholen` ihre Skills als Standard; jede Workflow-Rolle hat damit ihren
Workflow-Skill in der Standardausstattung (Test `TestWorkflowSkillsAreAdapted`).

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
                                      # (concept review delivery completion questions = Workflow-Skills mit contract.yaml)
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

**Skills ohne Originalpaket** (neu in CCMAS, CC-33: `ccmas-concept-review`, `ccmas-code-review`) tragen
`package: none` und `package_sha256` aus 64 Nullen (in Anführungszeichen). Der Snapshot-Vertrag verlangt dort einen Pfad
und 64 Hexziffern; die Kennzeichnung erfüllt beides, ohne eine Paketprüfsumme zu erfinden. Beide Angaben gelten nur
zusammen (`invalid_manifest`, wenn `none` mit einem echten Hash oder ein Nullhash mit einem Paketpfad steht);
`derived` und `changes` bleiben Pflicht und benennen den Ursprung. `ccmas skills show` zeigt „kein Originalpaket“.

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
- Jede **vorhandene** `contract.yaml` ist in sich gültig (`invalid_contract`, `platform_limit` in jeder Tiefe; Bezüge zu
  `internal/domain`). `Load` verlangt aber weder `rules/roles.yaml` noch das Vorhandensein von Verträgen: Ein Katalog ohne
  Regeln und ohne Verträge lädt (synthetische Kataloge in Tests anderer Pakete).
- Die Plattformregeln prüft die zweite, getrennte Stufe `skills.LoadChecked` beziehungsweise `skills.LoadRules` und
  `skills.ValidateWorkflow(catalog, rules)`: Workflow-Skills (Kategorien `concept`, `review`, `delivery`, `completion`,
  `questions`) haben einen Vertrag (`missing_contract`), die Regeldatei fehlt nie und ist gültig (`missing_rules`,
  `invalid_rules`), Verträge passen zu den Rollenregeln und zum Katalog. Sie läuft in `ccmas skills validate`, in den
  Tests gegen den echten Bestand und vor jeder Entscheidung der Auswerter. Einzelheiten:
  [`workflow-rules.md`](workflow-rules.md).
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
| `missing_rules` / `invalid_rules` | `rules/roles.yaml` fehlt beziehungsweise ist ungültig (CC-33); nur in der Regelprüfung (`LoadRules`, `LoadChecked`, `ValidateWorkflow`), nicht in `Load` |
| `missing_contract` / `invalid_contract` | Workflow-Skill ohne `contract.yaml` (nur `ValidateWorkflow`) beziehungsweise ungültiger Vertrag (CC-33; Format schon in `Load`) |
| `platform_limit` | Regeldatei oder Vertrag setzt eine Plattformgrenze (Worker-Limit, Modus, Policy, Berechtigungen, Zielbranch) (CC-33) |
| `review_missing`, `review_invalid`, `review_not_independent`, `concept_approval_invalid` | Entscheidungen der Auswerter für Push und Konzeptfreigabe, kein Ladefehler (CC-33) |

## Rollenbezogene Auswahl

`(*Catalog).Resolve(role, skillIDs)`:

- leere Liste = Standardausstattung der Rolle (alle approved-Skills mit der Rolle in `default_for`); sonst genau die
  genannten Skills. Eine Rolle ohne Standardausstattung ergibt einen gültigen Snapshot ohne Skills.
- Jede ID muss existieren, `approved` sein und die Rolle erlauben (in dieser Reihenfolge geprüft).
- `Resolve` liest die Dateien der gewählten Skills **neu** und vergleicht sie mit den Manifest-Hashes; eine Änderung
  nach `Load` wird als `changed_resource`, `missing_resource` oder `unlisted_resource` erkannt. Ein Snapshot enthält
  die dabei gelesenen und geprüften Bytes, nicht eine zweite Lesung.
- Die Standardausstattung einer Rolle enthält nie einen pending-Skill; die Kategorien `delivery`, `completion` und
  `questions` enthalten seit W33.2 keinen pending-Skill mehr; Prüfrollen erhalten nie `rules`- oder
  `delivery`-Skills (Manifest-Invariante plus Test gegen den echten Bestand). Seit CC-33 hat `reviewer` die
  Standardausstattung `ccmas-code-review` und `concept_reviewer` `ccmas-concept-review`.

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
Byte (mit `-update` neu schreiben); `VerifySnapshotDir` akzeptiert es. Die Python-Seite rechnet daran dieselben
Hashes nach (`runners/claude_sdk/tests/test_skill_snapshot.py`) und prüft zusätzlich einen Snapshot, den die echte Go-CLI schreibt. Git speichert keine Dateirechte; ausgechecktes Fixture-Material hat deshalb normale Rechte.

## Ablauf bei einer Skill-Änderung

1. Datei(en) unter `.claude/skills/ccmas-<name>/` ändern (Branch/PR nach `develop`); bei Workflow-Skills auch die
   `contract.yaml` (sie ist Teil des Skills und damit des Hashes).
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
`snapshot --role R [--skill ID …] --out DIR [--json]`. `validate` prüft seit CC-33 zusätzlich die Rollenregeln und die
Workflow-Verträge gegeneinander (`skills.LoadChecked`, Bericht mit `workflow_contracts`). Seit CC-14 lädt auch `snapshot`
mit `skills.LoadChecked`: Snapshots sind die Grundlage von Ausführungen, und auf einem Bestand ohne gültige Plattformregeln
(`missing_rules`, `invalid_rules`, `missing_contract`, `invalid_contract`) wird keiner geschrieben (Exit-Code 1). `list` und
`show` laden nur den Katalog (`skills.Load`) und brauchen die Regeldatei nicht, damit ein Bestand auch bei verletzten
Plattformregeln einsehbar bleibt; sie schreiben nichts.

Der Softwareordner ergibt sich aus `--root`, dann der Umgebungsvariable `CCMAS_HOME`, dann dem Verzeichnis über dem
Binary (`<exe-dir>/..`, Symlinks aufgelöst, nur wenn dort `.claude/skills/manifest.yaml` liegt); sonst endet der
Befehl mit einer verständlichen Meldung. Das Arbeitsverzeichnis und ein Zielprojekt werden nie gelesen.

Exit-Codes: 0 Erfolg; 1 Befund oder Laufzeitfehler (`validate` mit Befunden, Resolve-Fehler, unbekannter Skill,
Softwareordner nicht gefunden); 2 Bedienfehler (unbekannte Rolle, fehlende Pflichtoption, unerwartetes Argument).
Fehlermeldungen gehen nach stderr, bei Skill-Fehlern mit dem Code in eckigen Klammern; reguläre Ausgaben nach stdout.
`validate --json` schreibt den Bericht (`ok`, `findings[]` mit `code`, `skill`, `path`, `message`) auch bei Befunden
nach stdout und endet dann mit Exit-Code 1.

## Einbringung in die Laufzeit (umgesetzt in CC-35)

Skills gelangen nur über einen geprüften Snapshot in eine Sitzung; Projekt-, Benutzer- und synchronisierte Skills bleiben
über `setting_sources=[]` aus. Das Protokoll trägt den Snapshot im Start (`skill_snapshot`: Kennung und Pfad) und meldet
in `session_started`, was geladen wurde (`skill_snapshot_id`, `skills`); Felder und Fehlercodes: [`contracts.md`](contracts.md),
Abschnitt „Skills im Protokoll“.

1. **Vorprüfung** (`runners/claude_sdk/src/claude_sdk_runner/skill_snapshot.py`, vor jedem Sitzungsstart, ohne Modellaufruf):
   `skill_ids` ohne Snapshot ist ein terminaler Fehler. Mit Snapshot prüft Python die `snapshot.json` gegen den Vertrag
   `skill-snapshot`, rechnet alle Datei-Hashes, `tree_sha256` und `snapshot_id` nach den Definitionen oben nach (dieselben
   wie in Go; am gemeinsamen Beispiel und an einem von Go geschriebenen Snapshot getestet), erkennt fehlende und zusätzliche
   Dateien und Verzeichnisse, Symlinks und Sonderdateien und gleicht Kennung, Rolle und Menge der Skill-IDs mit dem Auftrag
   ab. Die `snapshot_id` im Auftrag ist der Anker: ein in sich stimmig umgeschriebener Snapshot hat eine andere Kennung.
2. **Optionen:** Mit Snapshot (und mindestens einem Skill) gilt `plugins=[{"type":"local","path":<realpath>}]`,
   `skills=["ccmas:<id>", …]` und `"Skill"` in `tools`. Ohne Snapshot (oder ohne Skills darin) ist `skills=[]`
   ausdrücklich gesetzt: kein Skill ist aufrufbar. Plugin-Skills heißen `<plugin>:<skill>`, hier `ccmas:<id>`
   (Plugin-Name aus `.claude-plugin/plugin.json`). Die Rechte des Policy-Profils bleiben unverändert; das SDK ergänzt nur die
   Regeln `Skill(<name>)` der Allowlist.
3. **Init-Frame:** Das Plugin `ccmas` mit genau dem Pfad des Snapshots ist geladen, jeder `ccmas:<id>` steht in `skills`, und es
   gibt keinen weiteren `ccmas:`-Eintrag und kein weiteres Plugin namens `ccmas`; sonst Abbruch mit `skills_not_loaded`, bevor ein
   Werkzeug läuft. Nachweis in der echten Sitzung: [`runtime-capabilities.md`](runtime-capabilities.md), „Skills in der Sitzung (CC-35)“.

Laufende Ausführungen bleiben bei ihrem Snapshot: der Snapshot ist ein eigenes Verzeichnis je Kennung, ein geänderter
Bestand erzeugt eine neue Kennung. Der Runner prüft einmal vor dem Start; zwischen Prüfung und Lesen durch die CLI schützt
nur das Schreibschutz-Layout (0444/0555), eine zweite Prüfung gibt es nicht.

## Offene Punkte

- **Go-Adapter für den Runner (CC-46):** Go schreibt den Snapshot (`Snapshot.Write`) und setzt `RunSpec.SkillSnapshot`
  (`SkillSnapshotRef`); der Adapter selbst folgt in CC-46. Bis dahin tragen nur die Typen und Fixtures das Feld.
- **Ressourcen neben `SKILL.md`** (zum Beispiel `scripts/…`): Die Dateien liegen im Snapshot, aber der Runner gibt dem Modell
  **keinen** Lesezugriff auf das Snapshot-Verzeichnis (Profile `readonly`/`scoped_write` unverändert). Ob ein Skill mit
  Ressourcen so nutzbar ist, wurde nicht geprüft (der Live-Probe-Skill besteht nur aus `SKILL.md`); ein Freischalten braucht die
  Freigabe des Projektinhabers.
- **Anbindung an Profile und Decision:** `AgentProfile.skill_ids` und `Decision.selection.skill_ids` werden vom
  Orchestrator über `Resolve` aufgelöst; der `domain.Catalog` wird aus dem Katalog gespeist (`profiles.BuildCatalog`) und
  führt seit CC-14 zusätzlich die Rollen je Skill, sodass `Decision.Check` einen für die Rolle nicht erlaubten Skill ablehnt.
  Der Runner-Adapter schreibt Snapshots nur aus einem mit `skills.LoadChecked` geladenen Katalog (`Catalog.Checked()`).
- **Anpassungen der pending-Skills:** W33.2 ist erledigt (`feature-parallel`, `meilenstein-abschluss`,
  `antworten-abholen`). Offen: CC-14, W14.2/W14.3 (`neue-aufgabe-nehmen`, `morgen-briefing`, neue Skills `ccmas-supervisor` und
  `ccmas-work-assessment`), M07 (`projekt-anlegen`). Konzeptprüfer und Reviewer haben seit CC-33 ihre Skills
  (`ccmas-concept-review`, `ccmas-code-review`).
- **Adapter statt direkter Schreibzugriffe:** Die Texte der angepassten Skills nennen die Zentrale (`ccmas question`),
  den Go-Dienst für Commits (M04) und die kontrollierten Adapter als Ziel; bis sie verfügbar sind, gilt in der
  Entwicklungssitzung die Regeldatei (Regel 7.8 für Antworten über Asana).
- **Regeln:** `rules/roles.yaml` ist maschinenlesbarer Auszug der Regeldatei und wird von `skills.LoadRules`/`LoadChecked`
  geprüft, nicht von `skills.Load` ([`workflow-rules.md`](workflow-rules.md)). Ein Befehl `ccmas rules` bleibt geplant.
