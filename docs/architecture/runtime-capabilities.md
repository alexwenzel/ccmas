# CCMAS – Runtime-Fähigkeiten und technische Nachweise (CC-17)

Stand: Ticket CC-17 (Meilenstein M01), Recherche und Läufe am **2026-11-10**. Führend ist die Spezifikation
„Multi Agent Setup“ (Version 1.3.0), Abschnitte „Go und SDK zusammenführen“, „Runner-Protokoll“, „Anmeldung und
Abonnement“, „Berechtigungen und technische Isolation“, „Technische Nachweise und Freigabepunkte“ und „Remote
Control, mobile Geräte und Desktop“. Diese Datei hält die Befunde fest und ist keine Kopie der Spezifikation.

Eingesetzte Versionen bei allen Läufen: `claude-agent-sdk` 0.2.180 (gepinnt), lokales Claude Code 2.1.311
(`cli_path` ausdrücklich gesetzt, nicht die gebündelte CLI), Python 3.13, macOS (Seatbelt), Modell
`claude-haiku-5-5` (angefordert: `haiku`), Effort `low`, Anmeldeart claude.ai (Max-Abonnement).

## Überblick der Fähigkeiten

| Fähigkeit | nachgewiesen | nicht nachgewiesen | blockiert |
| --- | --- | --- | --- |
| Start mit lokalem Binary und eigener Anmeldung | Echte Sitzung über `python -m claude_sdk_runner` und `ClaudeSDKClient`, `cli_path` = lokales Binary, `apiKeySource: none`, Anmeldeart claude.ai (Beleg `live-roundtrip.jsonl`) | andere CLI-Versionen, auch die im SDK gebündelte 2.1.321 | – |
| Ereignisstrom | `session_started`, `activity`, `tool_event` (started, finished, failed), genau ein `result` (Belege) | Teilnachrichten-Streaming (`include_partial_messages`) – M04 | – |
| Wirksame Konfiguration | Modell, Berechtigungsmodus, CLI-Pfad und -Version, Anmeldeart kommen aus dem Init-Frame der CLI beziehungsweise der Vorprüfung | **Wirksames Effort:** der Init-Frame enthält kein Effort-Feld, der Runner meldet nur den übergebenen Wert | – |
| Interrupt/Stop | Interrupt über das Protokoll während eines laufenden Bash-Prozesses: Prozess beendet, genau ein terminales `result` mit `cancelled` (Beleg `live-interrupt.jsonl`) | Stop als eigener Sitzungsabschluss (der Runner behandelt `stop` wie `interrupt`); Prozessbaum bei Schlaf/Absturz | – |
| Fragen/Antworten | – | `answer` wird mit nicht terminalem `error` `answer_unsupported` beantwortet; Fragen aus der Sitzung (`AskUserQuestion`, `can_use_tool`) sind nicht angebunden (M04, CC-40) | – |
| Resume | `resume_session_id` wird an das SDK durchgereicht | Wiederaufnahme wurde nicht ausgeführt | – |
| Isolation (Dateisystem) | Abschnitt 5: Sandbox plus Permission-Regeln über den Runner (Beleg `live-scope-probe.jsonl`) | Abschnitt 5: Git, Werkzeugkette, Netzwerk, Hooks/MCP, Prozesszählung, andere Plattformen | **Autonomer Modus:** nicht verfügbar (Profil `autonomous` wird abgelehnt) |
| Diagnosen ohne Konto | Abschnitt 6 (Tests ohne Konto-Zugriff) | Abo-Limit gegen die echte CLI (nur mit Fake-Client belegt) | – |
| Remote Control | nur Dokumentation und `--help` gelesen (Abschnitt 7) | SDK-Sitzungen in Remote Control | – |
| Bedingungen für SDK mit Max-Anmeldung | Abschnitt 1 | eindeutige Erlaubnis in den Bedingungen | – |

## Der Runner in diesem Stand

Dateien im Paket `claude_sdk_runner` (`runners/claude_sdk/src/claude_sdk_runner/`): `__main__.py` (Prozess-Einstieg),
`entrypoint.py` (JSON-Lines-Protokoll), `runtime.py` (Vorprüfung und SDK-Sitzung), `environment.py` (Umgebung),
`policy.py` (Policy-Profile), `diagnostics.py` (Fehlerarten, Schwärzung). Tests: `runners/claude_sdk/tests/`
(`test_runtime_smoke.py`, `test_runtime_units.py`, Hilfen in `support.py`).

- **Eingang:** `python -m claude_sdk_runner [--max-turns N]`. stdin liefert `start` (`RunSpec`), danach `answer`,
  `interrupt`, `stop`; jede Zeile wird mit `claude_sdk_runner.contracts` gegen `to-runner` geprüft (höchstens 1 MiB je
  Zeile). `--max-turns` (ganze Zahl von 1 bis 200) begrenzt die Runden der Sitzung und damit die Kosten, zum Beispiel
  in Live-Tests. Ein ungültiger Start oder ein ungültiges Argument liefert nichts auf stdout und Exit-Code 2.
- **Ausgang:** nur gültige Ereignisse auf stdout (jede Zeile wird vor dem Schreiben gegen `from-runner` geprüft),
  redigierte und begrenzte Logs auf stderr; Prompts werden nie protokolliert. Exit-Code 0 nach einem terminalen
  Ergebnis (auch terminalem `error`), 1 bei Ende ohne Ergebnis (Absturz, entscheidet Go).
- **Genau ein terminales Ergebnis:** `ProtocolSink` verweigert ein zweites terminales Ergebnis und jedes Ereignis
  danach.
- **Umgebung (Entscheidung vom 10.11.2026):** Der Runner **entfernt** Variablen, die den Login übersteuern oder auf
  API-/Provider-Abrechnung umleiten (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`,
  `CLAUDE_CODE_USE_*`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_PROFILE`, Federation-Variablen u. ä.), und meldet sie mit dem
  nicht terminalen `error` `api_credentials_removed` (nur Namen, nie Werte). Ebenso entfernt er alle übrigen
  `ANTHROPIC_*`-, `CLAUDE_CODE_*`- und `CLAUDE_AGENT_SDK_*`-Variablen sowie `CLAUDECODE`; `CLAUDE_CONFIG_DIR` bleibt.
  Das geschieht vor dem ersten Aufruf des Binarys, `claude -v` und `claude auth status` laufen mit der bereinigten
  Umgebung. Es gibt keinen API-Fallback: ist danach keine claude.ai-Anmeldung aktiv, endet der Versuch mit `not_logged_in`.
- **Kontrollierte Umgebung beim SDK-Start:** Das echte SDK erbt `os.environ`. Der Runner startet deshalb nie einen
  echten `ClaudeSDKClient`, wenn `main(..., environ=<Mapping>)` ein anderes Mapping als `os.environ` bekam (terminaler
  `internal_error` nach der Vorprüfung, ohne Sitzung). Tests mit Fake-Client dürfen ein Dict übergeben.
- **Vorprüfung** (Reihenfolge): Umgebung bereinigen, Policy, Binary (absolut, ausführbar, nicht die gebündelte
  CLI), SDK-Pin, `claude -v` (Mindestversion 2.1.311), **Versionssperre** (siehe unten), `claude auth status` (nur
  `loggedIn`, `authMethod`, `apiProvider` werden gelesen). Kein Modellaufruf. Im Init-Frame prüft der Runner
  `apiKeySource == none`, die Fähigkeit `interrupt_receipt_v1` und die CLI-Version.
- **Fail-closed bei ungetesteter CLI-Version:** Ist die CLI ungleich 2.1.311 (aber nicht älter), bricht ein
  schreibendes Profil (`scoped_write`, `policy.WRITING_PROFILES`) in der Vorprüfung terminal mit
  `cli_version_untested` ab (die Meldung nennt beide Versionen), ohne Sitzung und ohne Modellaufruf. `readonly` läuft
  mit nicht terminaler Warnung `cli_version_untested` weiter.
- **Policy-Profile** (`RunSpec.policy_profile`): `readonly`, `scoped_write` (Abschnitt 5), `autonomous` wird mit
  `isolation_not_proven` abgelehnt, alles andere mit `unknown_policy_profile`. `setting_sources` ist immer leer, damit
  Benutzer- und Projekteinstellungen (zum Beispiel `remoteControlAtStartup`, Hooks, MCP) nicht in Läufe gelangen.
- **Wirksame Konfiguration:** `session_started` trägt die optionalen Felder `permission_mode`, `claude_binary`,
  `auth_source` (Vertragserweiterung in CC-17, siehe `contracts.md`).
- **Grenzen:** `max_runtime_seconds` gilt für Verbindungsaufbau, Anfrage und Antworten zusammen. Bei Ablauf: Interrupt
  (nur bei bereiter Sitzung, höchstens 10 s), dann terminaler `error` `max_runtime_exceeded`. Das Schließen der Sitzung
  (Disconnect, beendet den CLI-Prozess) hat eine eigene Grenze von 10 s (`runtime.CLOSE_TIMEOUT`) und ändert das
  Ergebnis nie; ein Fehler dabei wird nur protokolliert. Der schlechteste Fall liegt also bei
  `max_runtime_seconds` + 20 s. Wird ein hängender `__aenter__` abgebrochen, räumt der SDK-Client den gestarteten
  Prozess selbst auf (`connect()` ruft bei jedem Abbruch `disconnect()` auf); diese SDK-interne Aufräumzeit hat der
  Runner nicht begrenzt. `skill_ids` werden noch nicht angewendet (nicht terminaler `error` `skills_not_applied`).

Prüfbefehle:

```bash
cd runners/claude_sdk
uv run --frozen pytest tests/test_runtime_smoke.py -q                                      # ohne Konto, ohne Netzwerk
CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q -s        # echte Läufe (Abo-Kontingent), -s zeigt die Session-IDs
CCMAS_LIVE_TESTS=1 CCMAS_EVIDENCE_DIR=<Ziel> uv run --frozen pytest tests/test_runtime_smoke.py -m live -q   # mit Belegen
```

Die Live-Tests laufen nie in CI und nie in `make py-test`. Alle drei starten den Runner als Unterprozess
(`python -m claude_sdk_runner`; Interrupt-Test mit `Popen`, Kommandos über stdin, `--max-turns 4`; Scope-Probe mit
`--max-turns 16`) und geben ihm eine **Allowlist-Umgebung**, nie die Umgebung des Testprozesses: `PATH` (Binary-Suche,
Shell-Werkzeuge), `HOME` (Konfiguration, Schlüsselbund), `USER`/`LOGNAME` (Kontoname), `SHELL` (Bash-Werkzeug),
`TMPDIR` (macOS-Temp, Sandbox), `LANG`, `__CF_USER_TEXT_ENCODING` (macOS). Der Roundtrip-Test setzt zusätzlich einen
Dummy-`ANTHROPIC_API_KEY` (`ccmas-dummy-not-a-key`) und prüft, dass er gemeldet und entfernt wird und die Sitzung über
claude.ai (`auth_source: claude_ai`) läuft. Ohne Konto-Zugriff prüft `test_real_sdk_transport_passes_none_of_the_injected_variables_to_the_binary`
über den echten SDK-Transport mit einem Stub-Binary, dass keine injizierte Variable das Binary erreicht.

## 1. Bedingungen für das SDK mit der eigenen claude.ai-Anmeldung

Frage: Ist der Einsatz des SDK mit der eigenen claude.ai-Max-Anmeldung für persönliche, lokale Nutzung ausdrücklich
erlaubt, ausgeschlossen oder nicht eindeutig?

**Bewertung: nicht eindeutig, mit Tendenz zu „nicht ausgeschlossen“.** Ein ausdrückliches Verbot für die eigene,
persönliche, lokale Nutzung steht nirgends. Eine ausdrückliche Erlaubnis für das SDK mit Abo-Anmeldung steht an
keiner der normativen Stellen, nur in einer Support-Aussage zur Abrechnung. Die Live-Läufe sind ausdrücklich vom
Projektinhaber beauftragt.

| Stelle | Wortlaut (Auszug) | Lesart für CCMAS |
| --- | --- | --- |
| [Agent SDK Überblick](https://code.claude.com/docs/en/agent-sdk/overview) | „Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods described in the Quickstart instead.“ | Betrifft Produkte, die Dritten claude.ai-Login oder Limits anbieten. CCMAS ist ein persönliches Werkzeug ohne Dritte. Gilt nur, solange CCMAS das bleibt. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), „Authentication and credential use“ | „Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication … Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, Max, or Ultra plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens.“ | Verbietet Weitergabe, Vermittlung und Speicherung von Zugangsdaten. CCMAS tut das nicht: unverändertes Binary, eigener `/login`, keine Credential-Extraktion. Das „should“ für Produkte ist keine Regel für den Eigengebrauch, aber auch keine Zusage. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), „Acceptable use“ | „Advertised usage limits for Pro, Max, and Ultra plans assume ordinary, individual usage of Claude Code and the Agent SDK.“ | Nennt das Agent SDK ausdrücklich im Zusammenhang mit Plan-Limits für „ordinary, individual usage“. Spricht eher für Zulässigkeit. Ob eine Orchestrierung mit bis zu 16 parallelen Workern noch „ordinary“ ist, ist **nicht geklärt**. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) | „Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription …“ | Stützt das Betriebsmodell (unverändertes Binary, eigene Anmeldung). |
| [Support: Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) | „… when you're signed in with your Claude plan, the Claude Agent SDK and `claude -p` still draw from your subscription limits.“ Nachtrag 15.06.2026: „We've paused the previously-announced changes to Claude Agent SDK usage. For now, nothing has changed …“ | Abrechnung über Abo-Limits ist aktuell bestätigt. „For now“ heißt: kann sich ändern. Monatliche API-Guthaben (seit 07.10.2026 für Max) gelten nur bei Nutzung mit API-Schlüssel, nicht bei Plan-Anmeldung. |
| [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (Stand 08.10.2025), Abschnitt 3 | Verboten ist der Zugriff „through automated or non-human means, whether through a bot, script, or otherwise“ außer „when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it“. | **Spannung:** Ein Skript-/SDK-Zugriff mit Plan-Anmeldung ist hier nicht ausdrücklich erlaubt. Die Support-Seite und die Legal-Seite zu Claude Code und Agent SDK sprechen dafür, dass Anthropic diese Nutzung vorsieht („otherwise explicitly permit“ ist damit plausibel, aber nicht wörtlich bestätigt). |

Restrisiko, das der Projektinhaber tragen muss: Anthropic kann die Aussage „for now“ ändern, und die Consumer Terms
enthalten die allgemeine Automatisierungsklausel. Jede Ausweitung auf Dritte (andere Nutzer, geteilte Konten) wäre
nach den oben zitierten Stellen **ausgeschlossen**. Ein stiller Wechsel auf API-Abrechnung findet nicht statt
(Abschnitt 3).

## 2. Anmeldung und Umgebung (nachgewiesen)

- `claude auth status` (JSON) liefert `loggedIn`, `authMethod`, `apiProvider`, `subscriptionType`. Hier:
  `loggedIn: true`, `authMethod: claude.ai`, `apiProvider: firstParty`, Max. Konto-, E-Mail- und Organisationsfelder
  enthält die Ausgabe auch; sie dürfen nie protokolliert werden. Mit leerem `CLAUDE_CONFIG_DIR` liefert derselbe
  Befehl `loggedIn: false`, `authMethod: none` und Exit-Code 1. Das ist eine echte Diagnose „kein Konto“ ohne
  Konto-Zugriff.
- Im Init-Frame jeder SDK-Sitzung steht `apiKeySource`. Wert `none` heißt: kein API-Schlüssel im Spiel (claude.ai-Login).
  Beobachtet in allen Läufen: `none`. Jeder andere Wert muss den Lauf sofort abbrechen.
- Das SDK vererbt `os.environ` an die CLI (nur `CLAUDECODE` wird entfernt) und kann Variablen über `options.env` nur
  überschreiben, nicht löschen. Der Runner bereinigt `os.environ` deshalb **vor** dem ersten Aufruf des Binarys selbst
  (Abschnitt „Der Runner in diesem Stand“) und startet einen echten Client nur mit `os.environ`. In der
  Entwicklungssitzung waren `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_*` und `CLAUDE_CODE_CHILD_SESSION`
  gesetzt; sie gehen nicht an die Kindsitzung. Belegt ist das ohne Konto durch einen Test über den echten
  SDK-Transport (Unterprozess, Stub-Binary protokolliert nur Variablennamen): keine der injizierten Variablen
  erreicht `-v`, `auth status` oder den Sitzungsstart; erlaubt sind nur die vom SDK selbst gesetzten
  `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_AGENT_SDK_VERSION` und `CLAUDE_CODE_SDK_READS_SESSION_STATE`.
- Die frühere Fassung der Live-Tests rief `main` im Testprozess mit einem bereinigten Dict auf. Das SDK hätte dort
  trotzdem die unbereinigte `os.environ` des Testprozesses (zum Beispiel `CLAUDE_CODE_*` einer übergeordneten
  Claude-Sitzung) an die CLI vererbt. Die Läufe der Interrupt-Probe und der Scope-Probe in den Belegen vom 2026-11-10
  entstanden in dieser Form; sie wurden nach der Umstellung auf Unterprozess und Allowlist wiederholt (Abschnitt 4).

## 3. Kompatibilität SDK 0.2.180 und CLI 2.1.311

**Nachgewiesen:** Die Kombination funktioniert in echten Läufen (Abschnitt 4): Start über `cli_path`, Init-Frame mit
`claude_code_version` 2.1.311 und den Fähigkeiten (unter anderem `interrupt_receipt_v1`), Werkzeugereignisse,
`ResultMessage`, Interrupt. Das SDK selbst prüft die CLI-Version nur auf `>= 2.0.0` und warnt nur; der Runner erzwingt
deshalb die Untergrenze 2.1.311 (ältere CLI → `sdk_cli_incompatible`) und die gepinnte SDK-Version. Eine
andere, neuere CLI ist ungetestet und wird **fail-closed** behandelt: schreibende Profile (`scoped_write`) enden in der
Vorprüfung terminal mit `cli_version_untested` (ohne Sitzung, ohne Modellaufruf), `readonly` läuft mit der nicht
terminalen Warnung `cli_version_untested`. Es gibt keinen CLI-Upgrade durch den Runner; lokal ist 2.1.311 installiert.

**Nicht nachgewiesen:** andere CLI-Versionen, insbesondere die im SDK gebündelte 2.1.321. Es gibt keine offizielle
Kompatibilitätsmatrix.

## 4. Echte SDK-Läufe und Belege

Alle Läufe: temporäres Fixture unter `$TMPDIR`, Haiku, Effort `low`, kein Fan-out. Insgesamt **11 echte Läufe**:
7 Vorversuche mit Wegwerf-Skripten (Basislauf und Isolationsvergleich, Abschnitt 5) und 4 Läufe über den Runner
(Roundtrip, Interrupt ×2, Scope-Probe). Der erste Interrupt-Lauf war ungültig, weil die CLI ein einzelnes `sleep 57`
sofort blockiert („Blocked: standalone sleep“); der Test wurde auf einen echten Kindprozess mit Positivkontrolle
umgestellt und wiederholt.

**Was der Beleg enthält und was nur der Test prüft:** Die JSONL-Dateien enthalten ausschließlich die Protokollereignisse.
Weitere Prüfungen führt der Live-Test selbst aus, sie stehen **nicht** in den Belegen und sind nicht aus ihnen
nachprüfbar: Dateistand im Fixture und Unverändertheit von Nachbar-, Secret- und Kontrollpfad-Attrappen
(`test_scope_probe`), Positivkontrolle „Kindprozess läuft vor dem Interrupt“ und „Kindprozess danach beendet“
(`test_live_interrupt`, per `pgrep`), Inhalt der angelegten Datei (`test_live_sdk_roundtrip`). Sie gelten für den
Testlauf am 2026-11-10; wer sie belegen will, muss den Live-Test wiederholen. Es wurden keine Felder nachträglich in
die Belegkopfzeilen geschrieben.

**Nachprüfung nach der Umstellung auf Unterprozess und Allowlist (2026-11-10, derselbe Tag, 3 weitere echte Läufe, Haiku,
Effort `low`):** `test_live_sdk_roundtrip` mit Dummy-`ANTHROPIC_API_KEY` in der Umgebung (Session
`9e71bb53-bc7e-4bed-8b18-85d6944d9d93`), `test_live_interrupt_ends_with_exactly_one_cancelled_result` (Session
`88408048-46f1-4d6d-8f66-be6b15bf30ca`) und `test_scope_probe` (Session `51a86faa-988d-4aac-aebf-8256456ac08f`)
bestanden. Die Belegdateien unten stammen noch von den früheren Läufen und werden in einem Folgeauftrag neu erzeugt.

Redigierte Belege (JSONL, Kopfzeile mit Datum, SDK-, CLI-Version und Modell; Pfade des Fixtures geschwärzt, keine
Konto-, E-Mail- oder Organisationsdaten; die Session-ID steht bewusst darin) unter `docs/architecture/evidence/`:

| Beleg | Inhalt |
| --- | --- |
| `live-roundtrip.jsonl` | `python -m claude_sdk_runner` als Unterprozess: `session_started` (Session-ID, wirksames Modell `claude-haiku-5-5`, `auth_source: claude_ai`, `permission_mode: dontAsk`, gewähltes Binary), `Write`-Werkzeugereignis (started, finished), genau ein `result` (`succeeded`) |
| `live-interrupt.jsonl` | Laufender Bash-Prozess (`python3 -c "…time.sleep(57)"`), Interrupt über das Protokoll, Werkzeug `failed`, genau ein `result` mit `cancelled`, Dauer bis zum Ergebnis 0,7 s (Kopfzeile). Positivkontrolle und „Kindprozess beendet“ sind nur vom Test geprüft |
| `live-scope-probe.jsonl` | Scope-Probe über den Runner (Abschnitt 5): 10 Werkzeugaufrufe, 2 erlaubt, 8 als `failed` gemeldet. Der Dateistand ist nur vom Test geprüft |

Beobachtung zu Limits: Ein `RateLimitEvent` (Status `allowed`, Fenster `five_hour`, Überziehung `rejected`, Grund
`org_level_disabled`) zeigt, dass keine Überziehungsabrechnung aktiv ist. Die gemeldeten Kosten werden lokal zum
Listenpreis berechnet; abgerechnet wurde nichts über eine API.

**Nicht nachgewiesen:** Das wirksame Effort-Niveau. Der Init-Frame enthält kein Effort-Feld. Der Runner meldet den
übergebenen Wert (`session_started.effort`); „wirksam“ ist für Effort per CLI nicht prüfbar. Modell (wirksam) und
Berechtigungsmodus stehen im Init-Frame.

## 5. Scope und Isolation: Befund

Aufbau: Fixture (erlaubt), Nachbarverzeichnis, Secret-Attrappe, Kontrollpfad-Attrappe (`control/contracts/`), alles in
einem temporären Baum, keine echten Secrets, keine Repo-Pfade. Neun (frühe Vorversuche) beziehungsweise zehn Schritte je Lauf, je Schritt ein Werkzeugaufruf:
Schreiben im Fixture mit `Write` und `Bash`, Schreiben im Nachbarn mit `Write`, `Bash` und `python3 -c`, Lesen der
Secret-Attrappe mit `Read` und `Bash`, Schreiben im Kontrollpfad mit `Write` und `Bash`, Auflisten des Nachbarn.
Maßgeblich ist der Dateistand nach dem Lauf, nicht die Auskunft des Modells.

| Konfiguration | Fixture schreiben | Nachbar/Kontrollpfad schreiben | Secret lesen | Befund |
| --- | --- | --- | --- | --- |
| Kontrolle: `bypassPermissions`, keine Sandbox | ja | **ja, alle Wege** | **ja** | Das Modell führt die Versuche aus; ohne Grenze gelingen sie. Damit sind die Sperren in den anderen Läufen echte Grenzen und keine Ablehnung durch das Modell. |
| Nur Permission-Regeln (Allow/Deny je Pfad, `dontAsk`) | ja | `Write`/`Edit`/`Read` verweigert. `Bash` verweigert nur bei sichtbarem Pfad im Befehlstext. **`python3 -c open(...)` schrieb in den Nachbarn.** | `Read`/`Bash` verweigert | Regeln sind eine Grenze für die Dateiwerkzeuge, aber **keine** Grenze für Shell-Prozesse. |
| Nur Sandbox (macOS Seatbelt, `allowWrite`/`denyRead`) | ja | `Bash` und `python3` verweigert („Operation not permitted“). **`Write` schrieb in Nachbar und Kontrollpfad.** | `Bash` verweigert, **`Read` las die Attrappe** | Die Sandbox gilt nur für Shell-Prozesse, nicht für die eingebauten Dateiwerkzeuge (so auch dokumentiert). |
| Sandbox plus Permission-Regeln (Deny je Pfad) | ja | alle neun Versuche verweigert | verweigert | Beide Ebenen zusammen halten für die getesteten Pfade. |
| Sandbox plus Standard-Verweigerung (`denyRead` auf den Elternpfad, `allowRead` und `allowWrite` nur Fixture, `permissions.blockReadsOutsideWorkingDirectories`, `dontAsk`, nur auf das Fixture begrenzte Allow-Regeln) | ja | alle verweigert, ohne dass die Pfade einzeln aufgezählt sind | verweigert, auch das Auflisten des Nachbarn | Beste Variante: verweigert alles, was nicht erlaubt ist. |
| Wie zuvor, zusätzlich `denyRead` auf das Home-Verzeichnis | ja | alle verweigert | verweigert | Das Bash-Werkzeug funktioniert weiterhin. |
| **Runner-Profil `scoped_write`** (Live-Test `test_scope_probe`, Beleg `live-scope-probe.jsonl`; erzeugt aus `policy.py` mit dem Fixture als Arbeitsverzeichnis, Standard-Verweigerung wie zuvor) | ja (beide Wege) | alle 8 verbotenen Zugriffe verweigert, Dateistand unverändert | verweigert, das Secret taucht in keinem Ereignis auf | Die Grenze gilt auch über den produktiven Runner-Pfad. |

Verwendete Mittel in der besten Variante: `permission_mode: dontAsk`, `allowed_tools` mit auf das Fixture begrenzten
`Write(//…/**)`, `Edit(…)`, `Read(…)` und `Bash`, `setting_sources: []`, Sandbox mit `enabled`, `failIfUnavailable`,
`allowUnsandboxedCommands: false`, `autoAllowBashIfSandboxed`, `filesystem.allowWrite`, `filesystem.denyRead`,
`filesystem.allowRead`. Pfade müssen als `realpath` angegeben werden (`/var/…` ist `/private/var/…`).

### Isolationsurteil

**Kurzurteil:** Die Dateisystemgrenze ist für die getesteten Fälle **technisch nachgewiesen** (Mittel: OS-Sandbox der CLI
für Shell-Prozesse plus Permission-Regeln mit `dontAsk` für die Dateiwerkzeuge, beides aus dem Profil `scoped_write`).
Ein vollständiger Isolationsnachweis liegt **nicht** vor; **autonome Ausführung ist nicht verfügbar**
(`policy.autonomous_execution_status()` meldet `available: false` mit Gründen, das Profil `autonomous` wird abgelehnt).

- **Technisch nachgewiesen (nur für die getesteten Fälle):** Auf macOS mit CLI 2.1.311 bilden die Kombination aus
  OS-Sandbox für Shell-Prozesse und Permission-Regeln für Dateiwerkzeuge eine technische Dateisystemgrenze. Schreiben im
  Fixture gelingt, Schreiben und Lesen in Nachbar-, Secret- und Kontrollpfad-Attrappen scheitern über alle getesteten
  Wege (`Write`, `Read`, `Bash`, `python3`). Eine einzelne Ebene reicht nicht.
- **Nicht nachgewiesen, daher gilt der autonome Modus weiterhin als nicht verfügbar:**
  1. Arbeiten im echten Worktree: `git commit` aus der Sandbox. Das `.git` eines Worktrees liegt außerhalb des
     Arbeitsverzeichnisses und ist ein geschützter Pfad. Entweder Go übernimmt Commit/Push, oder die Freigabe öffnet
     eine Kontrollstelle.
  2. Testläufe der Werkzeugkette (`go test`, `uv run pytest`, Cache- und Toolchain-Pfade unter dem Home-Verzeichnis).
  3. Netzwerk: In der Sandbox sind Verbindungen blockiert, solange `allowedDomains` leer ist. Das wurde nicht
     eigens getestet.
  4. Weitere Wege außerhalb der Sandbox: Hooks, MCP-Server, `excludedCommands`, Unterprozess-Start von `claude`,
     Zählung ungezählter Agentenprozesse.
  5. Verhalten bei Versionswechsel der CLI und auf anderen Plattformen als macOS.
  6. Der Beleg gilt nur für ein Fixture unter `$TMPDIR`. Der Fall `denyRead` auf das Home-Verzeichnis mit einem
     Arbeitsverzeichnis **unterhalb** des Home-Verzeichnisses (echter Worktree) ist nicht belegt. Im Vorversuch lag das
     Fixture außerhalb von Home.
  7. Das Profil `readonly` hat keinen eigenen Live-Beleg; es ist nur als Policy-Aufbau getestet.
  8. Die übrige Umgebung (`os.environ`, zum Beispiel `GH_TOKEN` oder andere Tokens) wird bisher an das Binary und die
     Bash-Kindprozesse weitergereicht; nur `ANTHROPIC_*`, `CLAUDE_CODE_*`, `CLAUDE_AGENT_SDK_*` und `CLAUDECODE` werden
     entfernt oder abgewiesen. Eine kontrollierte Umgebung per Allowlist ist für M04 nötig.
- **Lösungsvorschlag für M04:** Die Profil-Erzeugung (Standard verweigern, Fixture erlauben, `failIfUnavailable`, kein
  `dangerouslyDisableSandbox`) steht in `policy.py` und wird in M04 mit dem Katalog aus Go verbunden. Zusätzlich eine
  Betriebssystemgrenze außerhalb des SDK prüfen: dedizierter macOS-Benutzer oder Seatbelt-Profil (`sandbox-exec`) für den
  gesamten Runner-Prozess, um auch Hooks und MCP abzudecken. Git-Schreibaktionen über den Go-Dienst.

**Abnahmeexperimente je Lücke (M04, kurz, jeweils mit Kontrolllauf ohne Grenze und echtem Worktree unter dem
Home-Verzeichnis):**

| Lücke | Experiment | Bestanden, wenn |
| --- | --- | --- |
| Git im Worktree | `git commit` und `git status` im Worktree mit Profil `scoped_write`; danach Prüfung, ob `.git` des Hauptrepositorys außerhalb schreibbar wäre | Commit gelingt nur über den vorgesehenen Weg (Go-Dienst oder eng freigegebener Pfad), fremde Refs und Objekte bleiben unverändert |
| Werkzeugkette | `make test`, `go test ./...`, `uv run --frozen pytest` im Worktree innerhalb der Sandbox | Läufe gelingen mit explizit freigegebenen Lesepfaden (Toolchain, Caches), Schreiben nur im Worktree und `$TMPDIR` |
| Home-Lesegrenze | Wie Abschnitt 5, aber Arbeitsverzeichnis unter Home, `denyRead` auf Home, `allowRead` nur Worktree und Toolchain | Zugriff auf eine Attrappe unter Home, außerhalb des Worktrees, scheitert; Bash-Werkzeug funktioniert |
| Netzwerk | `curl` und `git fetch` aus der Sandbox gegen einen lokalen Testserver | Verbindung scheitert, solange nicht ausdrücklich erlaubt |
| Hooks/MCP/ausgenommene Befehle | Lauf mit Hook beziehungsweise MCP-Server, der außerhalb schreibt | Entweder verweigert durch die OS-Grenze um den Runner, oder diese Konfiguration wird vom Runner abgelehnt |
| Prozesse | Kindprozessbaum eines Laufs zählen und nach Interrupt, `stop` und SIGTERM prüfen | Kein Prozess bleibt übrig, Start weiterer `claude`-Prozesse scheitert oder wird gezählt |
| Umgebung | Lauf mit gesetztem Dummy-`GH_TOKEN` | Der Wert ist im Bash-Kindprozess nicht sichtbar (Allowlist) |

**Entscheidungskriterien dedizierter Benutzer gegenüber `sandbox-exec`-Profil:** Ein dedizierter macOS-Benutzer
(eigenes Home, eigene Dateirechte) trennt Dateien, Schlüsselbund und Prozesse dauerhaft und deckt Hooks und MCP ab; er
verlangt aber eine eigene claude.ai-Anmeldung in diesem Benutzer (nur durch den Nutzer selbst per `/login`,
keine Übertragung von Zugangsdaten), Rechtevergabe für Worktrees und Einrichtungsaufwand. Ein `sandbox-exec`-Profil um
den Runner ist ohne zweites Konto einsetzbar und nutzt die vorhandene Anmeldung, ist aber ein von Apple als veraltet
geführtes Werkzeug, schwerer zu pflegen und liefert keine Trennung des Schlüsselbunds. Empfehlung: zuerst das
`sandbox-exec`-Profil mit den Abnahmeexperimenten erproben; der dedizierte Benutzer wird gewählt, wenn die
Anmeldung im zweiten Konto zumutbar ist **oder** das Profil eine der Zeilen oben nicht besteht (insbesondere Hooks/MCP,
Umgebung oder Home-Lesegrenze).

## 6. Diagnosen ohne Konto-Zugriff

Jede Diagnose ist ein eigener Code im `error` (Art `runtime`, terminal, wenn nicht anders vermerkt), mit Test ohne Konto-Zugriff
(`tests/test_runtime_smoke.py` und `tests/test_runtime_units.py`; Doubles nur dort: Stub-Binary für `-v` und
`auth status`, Fake-Client mit echten SDK-Nachrichtentypen).

| Code | Auslöser | Test |
| --- | --- | --- |
| `not_logged_in` | `claude auth status` meldet `loggedIn: false`; auch Antwort `authentication_failed` in der Sitzung | `test_no_api_fallback_when_not_logged_in`, zusätzlich mit dem echten Binary und leerem `CLAUDE_CONFIG_DIR` (kein Netzwerk, kein Konto) |
| `not_subscription_login` | Anmeldung ist nicht claude.ai/firstParty | `test_no_api_fallback_when_login_is_not_claude_ai` |
| `api_credentials_removed` (**nicht terminal**) | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_USE_*` u. ä. waren in der Umgebung und wurden **entfernt**; nur Namen, nie Werte. Danach läuft die normale Vorprüfung mit der bereinigten Umgebung; ohne Anmeldung folgt `not_logged_in` | `test_credentials_in_environment_are_removed_with_a_warning_and_never_reach_the_cli`, `test_removed_credentials_and_not_logged_in_ends_with_terminal_not_logged_in`, `test_real_sdk_transport_passes_none_of_the_injected_variables_to_the_binary`; live: `test_live_sdk_roundtrip` |
| `cli_version_untested` | CLI ist neuer als die nachgewiesene 2.1.311: **terminal** für `scoped_write` (Vorprüfung, keine Sitzung), **nicht terminal** (Warnung) für `readonly` | `test_untested_cli_version_blocks_the_writing_profile_before_any_session`, `test_newer_but_consistent_cli_runs_readonly_with_untested_warning` |
| `internal_error` (Art `internal`) | Echter SDK-Start mit einem anderen Mapping als `os.environ` (die Umgebung wäre unbereinigt); nur durch Programmierfehler erreichbar | `test_real_sdk_client_is_never_started_with_a_foreign_environment` |
| `max_runtime_exceeded` | `max_runtime_seconds` überschritten, auch bei hängendem Verbindungsaufbau oder hängender Anfrage | `test_max_runtime_exceeded_is_terminal_error`, `test_max_runtime_also_bounds_connect_and_query`, `test_hanging_disconnect_after_timeout_still_ends_with_max_runtime_exceeded` |
| `api_billing_detected` | `apiKeySource` im Init-Frame ist nicht `none`: Abbruch im Init-Frame, **nach** dem Senden der Anfrage: garantiert ist, dass der Runner danach nichts mehr verarbeitet und unterbricht, nicht dass in der CLI kein Werkzeug anläuft | `test_api_key_source_in_init_aborts_immediately` |
| `sdk_cli_incompatible` | CLI älter als 2.1.311, Version nicht lesbar, SDK-Pin weicht ab, Fähigkeit `interrupt_receipt_v1` fehlt | `test_old_or_unreadable_cli_version_is_incompatible`, `test_missing_interrupt_capability_is_incompatibility`, `test_sdk_pin_is_consistent` |
| `cli_unavailable` | Binary fehlt, nicht ausführbar, relativ oder die im SDK gebündelte CLI | `test_missing_binary_is_cli_unavailable`, `test_bundled_sdk_cli_is_rejected` |
| `billing_error` | Antwort `billing_error` in der Sitzung | `test_billing_error_in_session_is_terminal_error_without_fallback` |
| `subscription_limit` (Art `subscription_limit`, **nicht terminal**) | `RateLimitEvent` mit Status `rejected`, `rate_limit` oder HTTP 429; danach terminales `result` `controlled_failure` | `test_subscription_limit_is_non_terminal_error_then_controlled_failure` |

Es gibt keinen API-Fallback und keine Token-Vermittlung. Der Abo-Limit-Pfad ist nur mit einem Fake-Client belegt; ein
echter Limit-Lauf wurde bewusst nicht herbeigeführt.

**Umgebung:** Credential- und Provider-Variablen werden entfernt und als nicht terminale Warnung gemeldet (so
entschieden am 10.11.2026; die frühere Fassung brach mit `api_credentials_in_environment` ab, dieser Code existiert
nicht mehr). Alle übrigen `ANTHROPIC_*`-, `CLAUDE_CODE_*`- und `CLAUDE_AGENT_SDK_*`-Variablen sowie `CLAUDECODE`
werden ebenfalls entfernt, weil das SDK Variablen nur überschreiben, nicht löschen kann (sonst gelangten zum Beispiel
`CLAUDE_CODE_SESSION_ID` und `CLAUDE_CODE_MESSAGING_*` einer übergeordneten Claude-Sitzung in die Kindsitzung).
`CLAUDE_CONFIG_DIR` bleibt erhalten. Das Entfernen ist keine Anmeldung: Ohne aktive claude.ai-Anmeldung endet der
Versuch mit `not_logged_in`.

## 7. Remote Control: Vorprüfung (nur gelesen, nichts aktiviert)

Quellen: [Remote Control](https://code.claude.com/docs/en/remote-control), `claude --help`, `claude remote-control --help`.

**Nachgewiesen (dokumentiert, nicht ausgeführt):**
- Remote Control verbindet claude.ai/code beziehungsweise die Claude-App mit einer **lokalen Claude-Code-Sitzung**;
  Ausführung und Dateien bleiben auf dem Rechner, der Prozess muss weiterlaufen. Die Verbindung geht nur ausgehend
  (HTTPS, keine offenen Ports).
- Voraussetzungen: Pro, Max, Ultra, Team oder Enterprise, claude.ai-Anmeldung. „API keys are not supported.“ Ein
  `claude setup-token`-Token oder `CLAUDE_CODE_OAUTH_TOKEN` kann keine Remote-Control-Sitzung aufbauen.
  Nicht verfügbar mit Bedrock, Vertex, Foundry, fremdem `ANTHROPIC_BASE_URL` oder Gateway.
- Startwege: `claude remote-control` (Servermodus, Alias `rc`, Optionen u. a. `--name`, `--permission-mode`,
  `--spawn`, `--capacity`), `claude --remote-control` (interaktive Sitzung), `/remote-control` in Desktop und VS Code.
  `claude --remote-control` beschreibt die Hilfe als „interactive session“.
- Die Einstellung `remoteControlAtStartup` kann Remote Control beim Start automatisch einschalten. Für SDK-Läufe ist
  `setting_sources: []` zu setzen, damit Benutzereinstellungen nicht geladen werden (siehe Abschnitt 5).

**Nicht nachgewiesen:**
- Ob eine **SDK-Sitzung** (headless, vom Runner gestartet) in Remote Control erscheinen kann. Die Dokumentation nennt
  nur interaktive Sitzungen, den Servermodus und Desktop/VS Code, das Agent SDK wird dort nicht erwähnt. Ein
  Ausführungsversuch steht aus und wurde bewusst nicht gemacht.

**Grundlage für M10:** Der offizielle Weg ist mit Max und der eigenen Anmeldung grundsätzlich möglich, aber für
interaktive CLI-Sitzungen. Für SDK-Sitzungen gibt es keinen dokumentierten Weg. Wahrscheinlicher Plan B (so auch in
der Spezifikation vorgesehen): zusätzliche native Claude-Sitzung im Servermodus mit eng begrenztem
CCMAS-Steuerungs-Skill, der nur über die CCMAS-CLI liest und antwortet. M10 muss `claude remote-control` real mit dem
Konto erproben und dabei die Kombination mit dem Isolationsprofil prüfen.

## Offene Punkte für M04 (CC-40) und folgende

- **Interrupt-Race:** Ein Interrupt/Stop, der vor dem Senden der Anfrage eintrifft, wird vorgemerkt und direkt nach
  `client.query` angewendet (Test mit Fake-Client). Nicht live belegt: ob die CLI einen Interrupt unmittelbar nach dem
  Senden der Anfrage immer vor dem ersten Modellaufruf wirksam abbricht.
- **Abo-Wartezustand in Go:** Der Runner sendet einen nicht terminalen `error` (`kind: subscription_limit`) und danach
  ein terminales `result` `controlled_failure`. Wie Go das als Wartezustand ohne Crash-Zählung behandelt, ist offen.
- **EOF auf stdin** bedeutet „keine weiteren Kommandos“, der Lauf läuft weiter. Erkennung verwaister Runner und
  Beendigung ganzer Prozessgruppen (Schlaf, SIGTERM) gehören zu M04.
- **Fragen/Antworten, Teilnachrichten-Streaming, Resume** sind nicht angebunden beziehungsweise nicht ausgeführt.
- **Isolation:** Git-Schreibaktionen, Werkzeugkette in der Sandbox, Netzwerkgrenze, Hooks/MCP und eine OS-Grenze um
  den ganzen Runner (Abschnitt 5, Lösungsvorschlag) vor der Freigabe des autonomen Modus.
- **Laufzeitgrenze:** Die Aufräumzeit eines abgebrochenen `connect()` im SDK (`disconnect()`) ist nicht vom Runner
  begrenzt; auf einem Rechner im Schlaf oder bei hängendem CLI-Prozess bleibt die Prozessgruppen-Beendigung durch
  Go (M04) maßgeblich.
- **Policy-Profile** sind bis zur Einführung des Katalogs in Go (M03) im Runner festgelegt (`policy.py`).
- **Skills** (`skill_ids`) werden noch nicht angewendet.
- **Bedingungen:** Die Unschärfe aus Abschnitt 1 bleibt offen und wird vom Projektinhaber getragen.
