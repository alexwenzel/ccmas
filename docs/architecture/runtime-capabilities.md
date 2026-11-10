# CCMAS – Runtime-Fähigkeiten und technische Nachweise (CC-17, CC-35)

Stand: Ticket CC-17 (Meilenstein M01), Recherche und Läufe am **2026-11-10**; Abschnitt 9 (Skills in der Sitzung) kam mit CC-35 hinzu. Führend ist die Spezifikation
„Multi Agent Setup“ (Version 1.4.0), Abschnitte „Go und SDK zusammenführen“, „Runner-Protokoll“, „Anmeldung und
Abonnement“ (beide Entscheidungen des Projektinhabers vom 10.11.2026), „Berechtigungen und technische Isolation“
(Entscheidung „Isolationsarchitektur für M04“), „Technische Nachweise und Freigabepunkte“ und „Remote Control, mobile
Geräte und Desktop“. Diese Datei hält die Befunde fest und ist keine Kopie der Spezifikation.

Eingesetzte Versionen bei allen Läufen: `claude-agent-sdk` 0.2.180 (gepinnt), lokales Claude Code 2.1.311
(`cli_path` ausdrücklich gesetzt, nicht die gebündelte CLI), Python 3.13, macOS (Seatbelt), Modell
`claude-haiku-5-5` (angefordert: `haiku`), Effort `low`, Anmeldeart claude.ai (Max-Abonnement).

## Überblick der Fähigkeiten

| Fähigkeit | nachgewiesen | nicht nachgewiesen | blockiert |
| --- | --- | --- | --- |
| Start mit lokalem Binary und eigener Anmeldung | Echte Sitzung über `python -m claude_sdk_runner` und `ClaudeSDKClient`, `cli_path` = lokales Binary, `apiKeySource: none`, Anmeldeart claude.ai (Beleg `live-roundtrip.jsonl`) | andere CLI-Versionen, auch die im SDK gebündelte 2.1.321 | – |
| Ereignisstrom | `session_started`, `activity`, `tool_event` (started, finished, failed), genau ein `result` (Belege) | Teilnachrichten-Streaming (`include_partial_messages`) – M04 | – |
| Wirksame Konfiguration | Modell, Berechtigungsmodus, CLI-Pfad und -Version, Anmeldeart kommen aus dem Init-Frame der CLI beziehungsweise der Vorprüfung | **Wirksames Effort:** der Init-Frame enthält kein Effort-Feld, der Runner meldet nur den übergebenen Wert | – |
| Strukturierte Ausgabe (SDK-Fähigkeit für M02) | `output_format` (JSON-Schema) liefert `ResultMessage.structured_output` (Beleg `live-structured-output.jsonl`, Abschnitt 8) | Anbindung im Runner und im Protokoll (**M02**), größere Schemata, Verhalten bei Schemaverstoß | – |
| Interrupt/Stop | Interrupt über das Protokoll während eines laufenden Bash-Prozesses: Prozess beendet, genau ein terminales `result` mit `cancelled` (Beleg `live-interrupt.jsonl`) | Stop als eigener Sitzungsabschluss (der Runner behandelt `stop` wie `interrupt`); Prozessbaum bei Schlaf/Absturz | – |
| Fragen/Antworten | – | `answer` wird mit nicht terminalem `error` `answer_unsupported` beantwortet; Fragen aus der Sitzung (`AskUserQuestion`, `can_use_tool`) sind nicht angebunden (M04, CC-40) | – |
| Resume | `resume_session_id` wird an das SDK durchgereicht | Wiederaufnahme wurde nicht ausgeführt | – |
| Fremde Quellen (Projektdateien, Connectors, Plugins, Skills) | Im Runner-Lauf (`setting_sources=[]`) laden weder Hook, Projekt-Skill noch Benutzer-Skills/-Plugins aus dem Konfigurationsverzeichnis; `mcp_servers` ist 0; Positivkontrolle mit Quelle `project` zeigt Hook und Skill (Belege `live-roundtrip.jsonl`, `live-structured-output.jsonl`, Abschnitt 5) | Wirkung von `disableClaudeAiConnectors` (Anzahl ist auch ohne die Einstellung 0), Nachladen von Connectors nach dem Init-Frame, Herkunft von 3 Plugins mit Pfad außerhalb des Konfigurationsverzeichnisses, `CLAUDE.md` (Positivkontrolle nicht deterministisch: Marker erschien in einem Positivlauf nicht, im Review-Lauf schon; Abschnitt 5) | – |
| Skills in der Sitzung (CC-35) | Genau die Skills eines von Go geschriebenen, vom Runner nachgerechneten Snapshots sind geladen und aufrufbar (Plugin `ccmas`, `ccmas:<id>`); ein Skill außerhalb der Allowlist wird vom Skill-Werkzeug abgewiesen (Beleg `live-skill-snapshot.jsonl`, Abschnitt 9) | Ressourcen-Dateien neben `SKILL.md` (Lesezugriff nicht freigeschaltet), Sitzung ohne Snapshot live (nur mit Fake-Client belegt: `skills=[]`, kein `Skill`-Werkzeug), Lauf mit `scoped_write`, Agents und Task-Werkzeug | – |
| Isolation (Dateisystem) | Abschnitt 5: Sandbox plus Permission-Regeln über den Runner (Beleg `live-scope-probe.jsonl`) | Abschnitt 5: Git, Werkzeugkette, Netzwerk, Hooks/MCP, Prozesszählung, andere Plattformen, Lesen unter `~/.claude` | **Unbeaufsichtigter Modus:** nicht freigegeben (`scoped_write` ist ein Prüfprofil; Profil `autonomous` wird abgelehnt) |
| Diagnosen ohne Konto | Abschnitt 6 (Tests ohne Konto-Zugriff) | Abo-Limit gegen die echte CLI (nur mit Fake-Client belegt) | – |
| Remote Control | nur Dokumentation und `--help` gelesen (Abschnitt 7) | SDK-Sitzungen in Remote Control | – |
| SDK mit Max-Anmeldung (Zulässigkeit) | Entscheidung des Projektinhabers vom 10.11.2026: Betrieb angenommen (Abschnitt 1) | eindeutige Erlaubnis in den Bedingungen (bleibt als Hintergrund) | – |

## Der Runner in diesem Stand

Dateien im Paket `claude_sdk_runner` (`runners/claude_sdk/src/claude_sdk_runner/`): `__main__.py` (Prozess-Einstieg),
`entrypoint.py` (JSON-Lines-Protokoll), `runtime.py` (Vorprüfung und SDK-Sitzung), `environment.py` (Umgebung),
`policy.py` (Policy-Profile), `diagnostics.py` (Fehlerarten, Schwärzung), `skill_snapshot.py` (Prüfung des Skill-Snapshots,
seit CC-35). Tests: `runners/claude_sdk/tests/`
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
- **Vorprüfung** (Reihenfolge): Umgebung bereinigen, Policy, **Skill-Snapshot** (seit CC-35, Abschnitt 9), Binary (absolut, ausführbar, nicht die gebündelte
  CLI), SDK-Pin, `claude -v` (Mindestversion 2.1.311), **Versionssperre** (siehe unten), `claude auth status` (nur
  `loggedIn`, `authMethod`, `apiProvider` werden gelesen). Kein Modellaufruf. Im Init-Frame prüft der Runner
  `apiKeySource == none`, die Fähigkeit `interrupt_receipt_v1` und die CLI-Version.
- **Fail-closed bei ungetesteter CLI-Version:** Maßgeblich ist das **rohe erste Token** von `claude -v`; nur exakt
  `2.1.311` gilt als getestet. `2.1.311-rc.1`, `2.1.311.7` oder `2.1.311+build5` sind ungetestet, auch wenn ihre
  Zahlenfolge der Untergrenze entspricht. Bei ungetesteter, aber nicht älterer CLI bricht ein schreibendes Profil
  (`scoped_write`, `policy.WRITING_PROFILES`) in der Vorprüfung terminal mit `cli_version_untested` ab (die Meldung nennt
  beide Versionen), ohne Sitzung und ohne Modellaufruf. `readonly` läuft mit nicht terminaler Warnung
  `cli_version_untested` weiter. Ein Test legt fest, dass `readonly` das einzige nicht schreibende Profil ist
  (`set(PROFILES) - set(WRITING_PROFILES) == {"readonly"}`): ein neues Profil gilt als schreibend, bis es ausdrücklich
  anders eingeordnet ist.
- **Policy-Profile** (`RunSpec.policy_profile`): `readonly`, `scoped_write` (Abschnitt 5, ein **Prüfprofil**, kein
  freigegebener unbeaufsichtigter Modus), `autonomous` wird mit `isolation_not_proven` abgelehnt, alles andere mit
  `unknown_policy_profile`. `setting_sources` ist immer leer, damit Benutzer- und Projekteinstellungen (zum Beispiel
  `remoteControlAtStartup`, Hooks, MCP, `CLAUDE.md`, Projekt-Skills) nicht in Läufe gelangen. Beide Profile übergeben
  über `--settings` zusätzlich `disableClaudeAiConnectors: true` (Scope „Any file“) und
  `permissions.blockReadsOutsideWorkingDirectories`. `syncClaudeAiSkills` und `syncClaudeAiPlugins` setzt der Runner
  **bewusst nicht** (ein Test hält das fest): laut Settings-Doku verschiebt `false` in Benutzer- oder verwalteten
  Einstellungen bereits synchronisierte Skills und Plugins nach `~/.claude/skills/.trash` beziehungsweise
  `~/.claude/plugins/.trash`; der Runner darf die echte Konfiguration des Benutzers nicht verändern. Für Einstellungen
  per `--settings` nennt die Doku diese Verschiebung nicht; das wurde nicht geprüft.
- **Init-Zusammenfassung:** Beim Init-Frame schreibt der Runner eine Zeile auf stderr (kein Protokollfeld):
  `init: mcp_servers=N plugins=N skills=N agents=N slash_commands=N plugins_in_config_dir=N skill_names=<Namen>`.
  `plugins_in_config_dir` zählt Plugins, deren Pfad unter dem Konfigurationsverzeichnis des Benutzers liegt (installiert
  oder synchronisiert), `?` bei einem Eintrag ohne Pfad. Namen nennt die Zeile nur für Skills, je höchstens 40 Zeichen und
  zusammen 400 Zeichen (danach `…(+N)`); Namen von MCP-Servern, Connectors, Plugins und Agents erscheinen nie. Die
  Belege enthalten daraus nur Anzahlen.
- **Systemprompt:** Ohne `system_prompt` startet das SDK die CLI mit `--system-prompt ""` (SDK-Quelltext 0.2.180,
  `_build_command`). Runner-Sitzungen laufen damit ohne den Standardprompt von Claude Code; ob und wie ein eigener Prompt
  gesetzt wird, ist eine Wahl für **M02**. Der wirksame Prompt wurde nicht geprüft.
- **Wirksame Konfiguration:** `session_started` trägt die optionalen Felder `permission_mode`, `claude_binary`,
  `auth_source` (Vertragserweiterung in CC-17) sowie bei einem Skill-Snapshot `skill_snapshot_id` und `skills`
  (CC-35, siehe `contracts.md`).
- **Grenzen:** `max_runtime_seconds` gilt für Verbindungsaufbau, Anfrage und Antworten zusammen. Bei Ablauf: Interrupt
  (nur bei bereiter Sitzung, höchstens 10 s), dann terminaler `error` `max_runtime_exceeded`. Das Schließen der Sitzung
  (Disconnect, beendet den CLI-Prozess) hat eine eigene Grenze von 10 s (`runtime.CLOSE_TIMEOUT`) und ändert das
  Ergebnis nie; ein Fehler dabei wird nur protokolliert. Der schlechteste Fall liegt also bei
  `max_runtime_seconds` + 20 s. Wird ein hängender `__aenter__` abgebrochen, räumt der SDK-Client den gestarteten
  Prozess selbst auf (`connect()` ruft bei jedem Abbruch `disconnect()` auf); diese SDK-interne Aufräumzeit hat der
  Runner nicht begrenzt. `skill_ids` werden seit CC-35 aus einem geprüften Snapshot angewendet (Abschnitt 9); die
  frühere Warnung `skills_not_applied` gibt es nicht mehr.

Prüfbefehle:

```bash
cd runners/claude_sdk
uv run --frozen pytest tests/test_runtime_smoke.py -q                                      # ohne Konto, ohne Netzwerk
CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q -s        # echte Läufe (Abo-Kontingent), -s zeigt die Session-IDs
CCMAS_LIVE_TESTS=1 CCMAS_EVIDENCE_DIR=<Ziel> uv run --frozen pytest tests/test_runtime_smoke.py -m live -q   # mit Belegen
CCMAS_LIVE_TESTS=1 CCMAS_EVIDENCE_DIR=<Ziel> uv run --frozen pytest -q -m live -k skill -s  # Skill-Läufe (CC-35, 2 Sitzungen)
```

Die Live-Tests laufen nie in CI und nie in `make py-test`. Roundtrip, Interrupt und Scope-Probe starten den Runner als
Unterprozess (`python -m claude_sdk_runner`; Interrupt-Test mit `Popen`, Kommandos über stdin, `--max-turns 4`;
Scope-Probe mit `--max-turns 20`); der vierte Test (Positivkontrolle und strukturierte Ausgabe) startet das
test-eigene Probe-Skript `tests/probe_sources.py` (kein Produktionscode) als Unterprozess. Alle vier geben dem Prozess
eine **Allowlist-Umgebung**, nie die Umgebung des Testprozesses: `PATH` (Binary-Suche,
Shell-Werkzeuge), `HOME` (Konfiguration, Schlüsselbund), `USER`/`LOGNAME` (Kontoname), `SHELL` (Bash-Werkzeug),
`TMPDIR` (macOS-Temp, Sandbox), `LANG`, `__CF_USER_TEXT_ENCODING` (macOS). `CLAUDE_CONFIG_DIR` gehört nicht dazu: die
Live-Läufe verwenden `~/.claude`. Der Roundtrip-Test setzt zusätzlich einen
Dummy-`ANTHROPIC_API_KEY` (`ccmas-dummy-not-a-key`) und prüft, dass er gemeldet und entfernt wird und die Sitzung über
claude.ai (`auth_source: claude_ai`) läuft. Ohne Konto-Zugriff prüft `test_real_sdk_transport_passes_none_of_the_injected_variables_to_the_binary`
über den echten SDK-Transport mit einem Stub-Binary, dass keine injizierte Variable das Binary erreicht.

## 1. Bedingungen für das SDK mit der eigenen claude.ai-Anmeldung

Frage: Ist der Einsatz des SDK mit der eigenen claude.ai-Max-Anmeldung für persönliche, lokale Nutzung ausdrücklich
erlaubt, ausgeschlossen oder nicht eindeutig?

**Ergebnis (Entscheidung des Projektinhabers vom 10.11.2026):** Der SDK-Betrieb mit dem eigenen Max-Login ist
**angenommen**. Grundlage: CCMAS ist ein Open-Source-Programm zur persönlichen Nutzung, mit eigenem Login und
unverändertem lokalem Binary. Die Unschärfe der Bedingungen (siehe Bewertung unten) bleibt als dokumentierter
Hintergrund stehen und ist **keine Blockade** mehr. Unverändert gelten: kein API-Fallback, Abo-Limits ergeben sichtbare
Wartezustände, keine Weitergabe von Anmeldedaten.

**Bewertung der Quellen (Recherche am 2026-11-10): nicht eindeutig, mit Tendenz zu „nicht ausgeschlossen“.** Ein
ausdrückliches Verbot für die eigene, persönliche, lokale Nutzung steht nirgends. Eine ausdrückliche Erlaubnis für das
SDK mit Abo-Anmeldung steht an keiner der normativen Stellen, nur in einer Support-Aussage zur Abrechnung. Die
Live-Läufe sind ausdrücklich vom Projektinhaber beauftragt.

| Stelle | Wortlaut (Auszug) | Lesart für CCMAS |
| --- | --- | --- |
| [Agent SDK Überblick](https://code.claude.com/docs/en/agent-sdk/overview) | „Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods described in the Quickstart instead.“ | Betrifft Produkte, die Dritten claude.ai-Login oder Limits anbieten. CCMAS ist ein persönliches Werkzeug ohne Dritte. Gilt nur, solange CCMAS das bleibt. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), „Authentication and credential use“ | „Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication … Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, Max, or Ultra plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens.“ | Verbietet Weitergabe, Vermittlung und Speicherung von Zugangsdaten. CCMAS tut das nicht: unverändertes Binary, eigener `/login`, keine Credential-Extraktion. Das „should“ für Produkte ist keine Regel für den Eigengebrauch, aber auch keine Zusage. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), „Acceptable use“ | „Advertised usage limits for Pro, Max, and Ultra plans assume ordinary, individual usage of Claude Code and the Agent SDK.“ | Nennt das Agent SDK ausdrücklich im Zusammenhang mit Plan-Limits für „ordinary, individual usage“. Spricht eher für Zulässigkeit. Ob eine Orchestrierung mit bis zu 16 parallelen Workern noch „ordinary“ ist, ist **nicht geklärt**. |
| [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) | „Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription …“ | Stützt das Betriebsmodell (unverändertes Binary, eigene Anmeldung). |
| [Support: Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) | „… when you're signed in with your Claude plan, the Claude Agent SDK and `claude -p` still draw from your subscription limits.“ Nachtrag 15.06.2026: „We've paused the previously-announced changes to Claude Agent SDK usage. For now, nothing has changed …“ | Abrechnung über Abo-Limits ist aktuell bestätigt. „For now“ heißt: kann sich ändern. Monatliche API-Guthaben (seit 07.10.2026 für Max) gelten nur bei Nutzung mit API-Schlüssel, nicht bei Plan-Anmeldung. |
| [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (Stand 08.10.2025), Abschnitt 3 | Verboten ist der Zugriff „through automated or non-human means, whether through a bot, script, or otherwise“ außer „when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it“. | **Spannung:** Ein Skript-/SDK-Zugriff mit Plan-Anmeldung ist hier nicht ausdrücklich erlaubt. Die Support-Seite und die Legal-Seite zu Claude Code und Agent SDK sprechen dafür, dass Anthropic diese Nutzung vorsieht („otherwise explicitly permit“ ist damit plausibel, aber nicht wörtlich bestätigt). |

Restrisiko, das der Projektinhaber mit seiner Entscheidung trägt: Anthropic kann die Aussage „for now“ ändern, und die
Consumer Terms enthalten die allgemeine Automatisierungsklausel. Ob eine Orchestrierung mit bis zu 16 parallelen
Workern noch „ordinary, individual usage“ ist, ist nicht geklärt. Jede Ausweitung auf Dritte (andere Nutzer, geteilte
Konten) wäre nach den oben zitierten Stellen **ausgeschlossen** und von der Entscheidung nicht gedeckt. Ein stiller
Wechsel auf API-Abrechnung findet nicht statt (Abschnitt 3).

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
- Deshalb laufen die Live-Tests als Unterprozess mit Allowlist-Umgebung (Abschnitt „Der Runner in diesem Stand“): ein
  Aufruf von `main` im Testprozess mit einem bereinigten Dict würde die unbereinigte `os.environ` des Testprozesses
  (zum Beispiel `CLAUDE_CODE_*` einer übergeordneten Claude-Sitzung) an die CLI vererben.

## 3. Kompatibilität SDK 0.2.180 und CLI 2.1.311

**Nachgewiesen:** Die Kombination funktioniert in echten Läufen (Abschnitt 4): Start über `cli_path`, Init-Frame mit
`claude_code_version` 2.1.311 und den Fähigkeiten (unter anderem `interrupt_receipt_v1`), Werkzeugereignisse,
`ResultMessage`, Interrupt. Das SDK selbst prüft die CLI-Version nur auf `>= 2.0.0` und warnt nur; der Runner erzwingt
deshalb die Untergrenze 2.1.311 (ältere CLI → `sdk_cli_incompatible`) und die gepinnte SDK-Version. Jede
andere CLI-Version, auch ein Token wie `2.1.311-rc.1` oder `2.1.311.7`, ist ungetestet und wird **fail-closed** behandelt:
schreibende Profile (`scoped_write`) enden in der Vorprüfung terminal mit `cli_version_untested` (ohne Sitzung, ohne
Modellaufruf), `readonly` läuft mit der nicht terminalen Warnung `cli_version_untested`. Es gibt keinen CLI-Upgrade durch
den Runner und keinen in diesem Ticket: lokal bleibt 2.1.311 installiert. Damit stehen Einstellungen, die die Settings-Doku
erst für neuere Versionen nennt, nicht zur Verfügung, zum Beispiel `permissions.blockWritesOutsideWorkingDirectories`
(laut Doku ab 2.1.314).

**Nicht nachgewiesen:** andere CLI-Versionen, insbesondere die im SDK gebündelte 2.1.321. Es gibt keine offizielle
Kompatibilitätsmatrix.

## 4. Echte SDK-Läufe und Belege

Alle Läufe: temporäres Fixture unter `$TMPDIR`, Haiku, Effort `low`, kein Fan-out. In CC-17 sind insgesamt **25 echte
Sitzungen** gelaufen: 7 Vorversuche mit Wegwerf-Skripten (Basislauf und Isolationsvergleich, Abschnitt 5), 7 frühere Läufe
über den Runner (ersetzt, siehe unten), 7 in der Abschlussrunde vom 2026-11-10 und 4 im unabhängigen Abschluss-Review
(siehe „Stand der Läufe“). Die eingecheckten Belege stammen ausschließlich aus der Abschlussrunde (4 Belegläufe); die
übrigen drei Sitzungen der Runde waren Diagnoseläufe ohne Beleg: die rohe Read-Meldung (Abschnitt 5), ein erster
Roundtrip-Lauf, dessen Quellenurteil zum Befund über die Plugins führte, und die Gegenprobe ohne
`disableClaudeAiConnectors` (Abschnitt 5).

**Was der Beleg enthält und was nur der Test prüft:** Die Protokoll-Belege enthalten die Protokollereignisse nach der
Kopfzeile. Weitere Prüfungen führt der Live-Test selbst aus; sie stehen nicht oder nur teilweise in den Belegen:
Dateistand im Fixture und Unverändertheit von Nachbar-, Secret- und Kontrollpfad-Attrappen (`test_scope_probe`),
Positivkontrolle „Kindprozess läuft vor dem Interrupt“ und „Kindprozess danach beendet“ (`test_live_interrupt`, per
`pgrep`), Inhalt der angelegten Datei (`test_live_sdk_roundtrip`). Wer sie belegen will, muss den Live-Test wiederholen.
Neu seit dieser Runde stehen in der Kopfzeile des Roundtrip-Belegs die Messwerte der Quellen-Probe (`sources_probe`:
Anzahlen aus dem Init-Frame, Ja/Nein-Angaben zu Hook, Skill und Marker, keine Namen), und jede Kopfzeile nennt den
Aufrufweg (`invocation`).

Alle Belege entstanden mit `claude-agent-sdk` 0.2.180 und Claude Code 2.1.311 (Modell `claude-haiku-5-5`, angefordert
`haiku`), jeweils als Unterprozess mit Allowlist-Umgebung; Pfade des Fixtures sind geschwärzt, es gibt keine Konto-,
E-Mail- oder Organisationsdaten und keine Namen von Skills, Plugins oder Connectors; die Session-ID steht bewusst
darin. Die Dateien liegen unter `docs/architecture/evidence/` (alle vom 2026-11-10):

| Beleg | Session | Inhalt |
| --- | --- | --- |
| `live-roundtrip.jsonl` | `74a3280b-3b45-4b40-8bf2-b16e1f526327` | `python -m claude_sdk_runner`, Profil `scoped_write`, Dummy-`ANTHROPIC_API_KEY` in der Umgebung: nicht terminaler `error` `api_credentials_removed` (nur der Name), `session_started` (wirksames Modell `claude-haiku-5-5`, `auth_source: claude_ai`, `permission_mode: dontAsk`, gewähltes Binary), `Write`-Werkzeugereignis (started, finished), genau ein `result` (`succeeded`). Das Fixture enthält `CLAUDE.md` (Marker), Hook und Skill; Messwerte in `sources_probe` (Abschnitt 5) |
| `live-interrupt.jsonl` | `2dfe34da-7748-4734-b5d1-e80608de04d7` | Laufender Bash-Prozess (`python3 -c "…time.sleep(57)"`), Interrupt über das Protokoll, Werkzeug `failed`, genau ein `result` mit `cancelled`, Dauer bis zum Ergebnis 0,7 s (Kopfzeile). Positivkontrolle und „Kindprozess beendet“ sind nur vom Test geprüft |
| `live-scope-probe.jsonl` | `225a80b7-3490-4afc-b10e-08911f21158f` | Scope-Probe über den Runner (Abschnitt 5): 11 Werkzeugaufrufe, 2 erlaubt, 9 als `failed` gemeldet (darunter beide `Read`-Versuche an der Lesegrenze). Der Dateistand ist nur vom Test geprüft |
| `live-structured-output.jsonl` | `2755b1ab-0890-4d16-bd63-9b35b3a64d93` | Probe-Skript (kein Runner): Positivkontrolle der Quellen-Probe mit `setting_sources=["project"]` (Abschnitt 5) und strukturierte Ausgabe (Abschnitt 8); eine Datensatzzeile statt Protokollereignissen |

Seit CC-35 kommt `live-skill-snapshot.jsonl` hinzu (zwei Läufe über den Runner mit einem von Go geschriebenen Skill-Snapshot,
Session-IDs in Abschnitt 9). Anders als die übrigen Belege nennt er Skill-Namen: den des Test-Probe-Skills und die in der Aufforderung
genannten mitgelieferten Skills; Konto-, E-Mail- und persönliche Pfadangaben enthält er nicht.

**Stand der Läufe:** Der erste Lauf der Live-Suite in der Abschlussrunde bestand `test_live_interrupt…`,
`test_scope_probe` und `test_live_positive_control_and_structured_output`; `test_live_sdk_roundtrip` scheiterte am
Quellenurteil (Befund zu den Plugins, Abschnitt 5). Nach der Ergänzung der Messgröße `plugins_in_config_dir` in der
Init-Zeile bestand er im zweiten Lauf. Die Abschlussrunde hat die Suite wegen der Kontingentgrenze (höchstens 7
Sitzungen) nicht noch einmal als Ganzes ausgeführt; die eingecheckten Belege stammen aus diesen beiden Läufen.

Im unabhängigen Abschluss-Review am 2026-11-10 auf Commit 8171ff8 lief die gesamte Live-Suite dagegen einmal vollständig
und bestand **4 von 4** (4 Sitzungen). Session-IDs: Roundtrip `b8cf129b-b0d5-4795-aba3-4da64b386599`, Interrupt
`b354a083-f39b-4425-b4b5-077776fe57da`, Scope-Probe `be1125e2-3c58-44f1-b3a1-c4822211f6b4`, strukturierte
Ausgabe und Positivkontrolle `2779c89c-bd10-4e01-bf1e-2817a627798a`. Die Belege dieses Laufs lagen nur im Scratchpad des
Reviews und sind nicht eingecheckt; Aufbau und Schwärzung entsprachen den eingecheckten Belegen. Eine Abweichung der
Positivkontrolle für `CLAUDE.md` zwischen dem eingecheckten Beleg und dem Review-Lauf steht in Abschnitt 5.

Beobachtung zu Limits: Ein `RateLimitEvent` (Status `allowed`, Fenster `five_hour`, Überziehung `rejected`, Grund
`org_level_disabled`) zeigt, dass keine Überziehungsabrechnung aktiv ist. Die gemeldeten Kosten werden lokal zum
Listenpreis berechnet; abgerechnet wurde nichts über eine API.

**Nicht nachgewiesen:** Das wirksame Effort-Niveau. Der Init-Frame enthält kein Effort-Feld. Der Runner meldet den
übergebenen Wert (`session_started.effort`); „wirksam“ ist für Effort per CLI nicht prüfbar. Modell (wirksam) und
Berechtigungsmodus stehen im Init-Frame.

## 5. Scope und Isolation: Befund

Aufbau: Fixture (erlaubt), Nachbarverzeichnis, Secret-Attrappe, Kontrollpfad-Attrappe (`control/contracts/`), alles in
einem temporären Baum, keine echten Secrets, keine Repo-Pfade. Neun (frühe Vorversuche) beziehungsweise elf Schritte im
Runner-Lauf, je Schritt ein Werkzeugaufruf: Schreiben im Fixture mit `Write` und `Bash`, Schreiben im Nachbarn mit
`Write`, `Bash` und `python3 -c`, Lesen der Secret-Attrappe mit `Read` und `Bash`, Schreiben im Kontrollpfad mit `Write`
und `Bash`, Auflisten des Nachbarn, Lesen der Nachbardatei mit `Read` (Schritt 11, seit der Abschlussrunde). Maßgeblich ist
der Dateistand nach dem Lauf, nicht die Auskunft des Modells.

| Konfiguration | Fixture schreiben | Nachbar/Kontrollpfad schreiben | Secret lesen | Befund |
| --- | --- | --- | --- | --- |
| Kontrolle: `bypassPermissions`, keine Sandbox | ja | **ja, alle Wege** | **ja** | Das Modell führt die Versuche aus; ohne Grenze gelingen sie. Damit sind die Sperren in den anderen Läufen echte Grenzen und keine Ablehnung durch das Modell. |
| Nur Permission-Regeln (Allow/Deny je Pfad, `dontAsk`) | ja | `Write`/`Edit`/`Read` verweigert. `Bash` verweigert nur bei sichtbarem Pfad im Befehlstext. **`python3 -c open(...)` schrieb in den Nachbarn.** | `Read`/`Bash` verweigert | Regeln sind eine Grenze für die Dateiwerkzeuge, aber **keine** Grenze für Shell-Prozesse. |
| Nur Sandbox (macOS Seatbelt, `allowWrite`/`denyRead`) | ja | `Bash` und `python3` verweigert („Operation not permitted“). **`Write` schrieb in Nachbar und Kontrollpfad.** | `Bash` verweigert, **`Read` las die Attrappe** | Die Sandbox gilt nur für Shell-Prozesse, nicht für die eingebauten Dateiwerkzeuge (so auch dokumentiert). |
| Sandbox plus Permission-Regeln (Deny je Pfad) | ja | alle neun Versuche verweigert | verweigert | Beide Ebenen zusammen halten für die getesteten Pfade. |
| Sandbox plus Standard-Verweigerung (`denyRead` auf den Elternpfad, `allowRead` und `allowWrite` nur Fixture, `permissions.blockReadsOutsideWorkingDirectories`, `dontAsk`, nur auf das Fixture begrenzte Allow-Regeln) | ja | alle verweigert, ohne dass die Pfade einzeln aufgezählt sind | verweigert, auch das Auflisten des Nachbarn | Beste Variante: verweigert alles, was nicht erlaubt ist. |
| Wie zuvor, zusätzlich `denyRead` auf das Home-Verzeichnis | ja | alle verweigert | verweigert | Das Bash-Werkzeug funktioniert weiterhin. |
| **Runner-Profil `scoped_write`** (Live-Test `test_scope_probe`, Beleg `live-scope-probe.jsonl`; erzeugt aus `policy.py` mit dem Fixture als Arbeitsverzeichnis, Standard-Verweigerung wie zuvor) | ja (beide Wege) | alle 9 verbotenen Zugriffe verweigert, Dateistand unverändert | verweigert, weder Secret noch Nachbar-Inhalt (`NEIGHBOR-DUMMY`) taucht in einem Ereignis oder auf stderr auf | Die Grenze gilt auch über den produktiven Runner-Pfad. |

Verwendete Mittel in der besten Variante: `permission_mode: dontAsk`, `allowed_tools` mit auf das Fixture begrenzten
`Write(//…/**)`, `Edit(…)`, `Read(…)` und `Bash`, `setting_sources: []`, Sandbox mit `enabled`, `failIfUnavailable`,
`allowUnsandboxedCommands: false`, `autoAllowBashIfSandboxed`, `filesystem.allowWrite`, `filesystem.denyRead`,
`filesystem.allowRead`. Pfade müssen als `realpath` angegeben werden (`/var/…` ist `/private/var/…`).

### Read-Grenze und Meldung

Die Sperre der Dateiwerkzeuge (`permissions.blockReadsOutsideWorkingDirectories`) meldet im Fehlerereignis, zum Beispiel:
`<Ziel> is outside <Fixture>; the permissions.blockRe…`. Bezugsverzeichnis ist also das **Fixture als Arbeitsverzeichnis**.
Im älteren Beleg stand stattdessen `<SCOPE>/secret/secret.txt is outside <TMP>`; das sah wie ein anderes Bezugsverzeichnis
aus, war aber ein Artefakt von Kürzung und Schwärzung:

1. Der Runner kürzt Fehlermeldungen in `tool_event.summary` auf 200 Zeichen (`fit(..., 200)`). Die Meldung enthält zwei
   absolute Pfade; unter dem langen pytest-Pfad (`…/T/pytest-of-<user>/pytest-N/test_…`) wurde sie **mitten im zweiten
   Pfad** abgeschnitten.
2. Die Schwärzung ersetzte zuerst die vollständigen Pfade (`<SCOPE>`), den abgeschnittenen Rest erfasste danach das
   Temp-Muster (`/private/var/folders/…` bis zum nächsten Leerzeichen) und machte daraus `<TMP>`.

Nachgestellt (ohne Sitzung) reproduziert genau diese Kürzung mit anschließender Schwärzung die alte Zeile. Die rohe Meldung
wurde in einer Diagnosesitzung lokal angesehen (kurzer Pfad, nicht eingecheckt) und nennt das Fixture. Ein Unterschied
zwischen In-Process- und Unterprozess-Aufruf spielt dafür keine Rolle, nur die Pfadlänge. Seit der Abschlussrunde liegt
der Baum der Scope-Probe unter einem kurzen Temp-Pfad, die Schwärzung ersetzt längste Pfade zuerst (mit und ohne
`/private`), und der Test prüft, dass das in der Meldung genannte Bezugsverzeichnis das Fixture ist (oder ein Anfang
seines Pfads, wenn die Meldung gekürzt ist). Im neuen Beleg steht `… is outside <FIXTURE>; …` für beide `Read`-Versuche.

**Nicht geprüft, nur dokumentiert:** Laut Settings-Doku bleiben bei `blockReadsOutsideWorkingDirectories` Dateien lesbar,
die Claude Code selbst braucht: Skills, Plugins, Regeln, Agents, Commands und die `CLAUDE.md` unter `~/.claude/`. Die
Lesegrenze der Dateiwerkzeuge schützt `~/.claude` damit nicht. Das ist ein Punkt für M04 (Liste unten); eine Probe, die
Dateien unter `~/.claude` liest, wurde bewusst nicht gebaut.

### Fremde Quellen: Projektdateien, Connectors, Plugins, Skills

Frage: Gelangen Dinge in eine Runner-Sitzung, die der Benutzer oder das Projekt konfiguriert hat (Hooks, `CLAUDE.md`,
Skills, MCP-Server, claude.ai-Connectors, synchronisierte Skills und Plugins)? Maßnahmen: `setting_sources=[]`
(das SDK übergibt `--setting-sources=`), `disableClaudeAiConnectors: true` in den per `--settings` übergebenen
Einstellungen, **keine** Änderung an den Sync-Einstellungen (Abschnitt „Der Runner in diesem Stand“). Beleg ist der
Init-Frame der CLI, ausgewertet über die Zeile `init:` auf stderr (nur Anzahlen).

**Roundtrip-Fixture** (`live-roundtrip.jsonl`, Kopfzeile `sources_probe`): `CLAUDE.md` mit der Anweisung, jede Antwort mit
`ZEBRA17` zu beenden, `.claude/settings.json` mit einem Hook (SessionStart und UserPromptSubmit), der nur eine Markerdatei im
Fixture anlegt, und der Skill `ccmas-probe`. Ergebnis im Runner-Lauf:

| Messgröße | Wert | Aussage |
| --- | --- | --- |
| Hook-Markerdatei | nicht entstanden | Projekt-Hook wurde nicht ausgeführt |
| Skill `ccmas-probe` im Init-Frame | nein | Projekt-Skill nicht geladen |
| Marker `ZEBRA17` in den Ereignissen | nein | `CLAUDE.md` wurde nicht befolgt (schwächer belegt als Hook und Skill, da die Positivkontrolle nicht deterministisch ist, siehe Einschränkung unten) |
| `mcp_servers` | 0 | keine MCP-Server, keine Connectors im Init-Frame |
| `plugins_in_config_dir` | 0 (bei `plugins` = 3) | kein Plugin aus dem Konfigurationsverzeichnis des Benutzers, auch kein synchronisiertes |
| Skills des Benutzers (Konfigurationsverzeichnis, eigene und synchronisierte) im Init-Frame | 0 von 1 vorhandenen | Gegenprobe ist nicht leer: es liegt ein solcher Skill lokal vor und wurde nicht geladen |
| `skills` / `agents` / `slash_commands` | 19 / 5 / 55 | mitgelieferte Einträge der CLI und des Sitzungsstarts; im Positivlauf mit Quelle `project` waren es 20 Skills (genau der Probe-Skill mehr) |

**Positivkontrolle** (`live-structured-output.jsonl`, Probe-Skript `tests/probe_sources.py`, gleiches Fixture, gleiche Rechte
wie `readonly`, Bereinigung und Vorprüfung mit den Modulen des Runners, aber `setting_sources=["project"]`): Hook-Markerdatei
entstand, `ccmas-probe` stand im Init-Frame (20 statt 19 Skills), `mcp_servers` 0, `plugins` 3. Das Ausbleiben von Hook und Skill im Runner-Lauf
ist damit eine Wirkung der Konfiguration und nicht einer wirkungslosen Probe. Für `CLAUDE.md` gilt das nur
eingeschränkt: der Marker erschien in diesem Positivlauf nicht (siehe Einschränkungen unten).

**Einschränkungen und Befunde:**

- **`CLAUDE.md`: Positivkontrolle nicht deterministisch.** Im eingecheckten Beleg `live-structured-output.jsonl` erschien
  der Marker `ZEBRA17` im Positivlauf nicht im Text (`claude_md_marker_in_text` false; die Antwort war eine strukturierte
  Ausgabe). Im Review-Lauf `2779c89c-bd10-4e01-bf1e-2817a627798a` (Abschnitt 4, nicht eingecheckt) erschien er dagegen
  (Antwort „ok ZEBRA17“, `claude_md_marker_in_text` true). Mit Quelle `project` kann `CLAUDE.md` also wirken, ob der
  Marker in der Antwort steht, ist aber Modellverhalten und damit nicht deterministisch. Die Negativaussage „`CLAUDE.md`
  im Runner-Lauf nicht befolgt“ ist deshalb schwächer als die für Hook und Skill, die über Dateistand (Markerdatei)
  beziehungsweise Init-Frame belegt sind.
- **3 Plugins im Init-Frame, Herkunft offen (Befund).** `plugins` ist im Runner-Lauf 3, mit und ohne Quelle `project`, und
  der Pfad liegt bei allen drei außerhalb des Konfigurationsverzeichnisses, ohne Marketplace-Kennung (`@…`). Es handelt
  sich damit nicht um synchronisierte oder installierte Plugins des Benutzers (diese lägen unter `~/.claude/plugins/`; der
  Benutzer hat lokal solche). Vermutlich sind es mit der CLI gelieferte Plugins; das wurde nicht bestätigt. Die
  erste Fassung des Tests (`plugins == 0`) schlug deshalb fehl; die Messgröße ist seither `plugins_in_config_dir`. M04: Herkunft klären
  und, wenn nötig, abschalten.
- **Connectors: Wirkung von `disableClaudeAiConnectors` nicht belegt.** Der Init-Frame zeigte `mcp_servers` = 0, auch in der
  Gegenprobe **ohne** die Einstellung (Diagnosesitzung mit `setting_sources=[]` und sonst denselben Rechten), obwohl das
  Konto in interaktiven Sitzungen Connectors hat. Die Einstellung ist also Absicherung, ihre Wirkung ist hier nicht von der
  Wirkung der leeren Quellenliste oder des nicht interaktiven Starts zu trennen. Der Init-Frame ist eine Momentaufnahme: ein
  späteres Nachladen von Connectors wurde nicht ausgeschlossen. Schutz davor bietet die Rechtevergabe (`dontAsk`, in
  `allowed_tools` stehen keine MCP-Werkzeuge, `tools` ist eine Positivliste), nicht der Init-Frame. M04: Nachladen prüfen.
- **Synchronisierte Skills/Plugins.** Laut Skills-Doku („Where synced skills load“) und Plugin-Doku synchronisiert und
  lädt Claude Code nicht, wenn `--setting-sources` `user` auslässt. Die Messwerte oben stützen das (vorhandene
  synchronisierte Skills und Plugins wurden nicht geladen). Deshalb werden `syncClaudeAiSkills`/`syncClaudeAiPlugins` nicht
  auf `false` gesetzt, siehe oben.
- **Mitgelieferte Skills/Agents/Commands** (19, 5, 55) stehen im Init-Frame. Skills: mit der Allowlist aus CC-35 nicht
  über das Skill-Werkzeug aufrufbar (Abschnitt 9); Agents und das Task-Werkzeug sind weiterhin nicht geprüft (`tools` ist eine
  Positivliste ohne `Task`).

### Isolationsurteil

**Kurzurteil:** Die Dateisystemgrenze ist für die getesteten Fälle **technisch nachgewiesen** (Mittel: OS-Sandbox der CLI
für Shell-Prozesse plus Permission-Regeln mit `dontAsk` für die Dateiwerkzeuge, beides aus dem Profil `scoped_write`).
Ein vollständiger Isolationsnachweis liegt **nicht** vor. `scoped_write` ist ausdrücklich nur ein **Prüfprofil**; es gibt
**keinen freigegebenen unbeaufsichtigten Modus für echte Worktrees**. Ein solcher wird erst nach bestandener Scope-Probe
ausgewiesen. Das Profil `autonomous` wird abgelehnt (`policy.autonomous_execution_status()` meldet `available: false` mit
Gründen).

**Entscheidung des Projektinhabers vom 10.11.2026 (Isolationsarchitektur für M04):**

- **A, innere Grenze in der CLI:** Seatbelt-Sandbox für Shell-Prozesse und pfadgebundene Berechtigungsregeln für die
  Dateiwerkzeuge; Git-Schreibaktionen nur über Go; Toolchain-Caches je Ausführung im erlaubten Bereich; Netzwerk nur über
  eine Allowlist; claude.ai-Connectors sowie synchronisierte Skills und Plugins abgeschaltet.
- **B, Spike:** ein äußeres `sandbox-exec`-Profil um Runner und CLI.
- **Nicht gewählt:** ein eigener macOS-Benutzer und Container.
- Ein unbeaufsichtigter Modus wird erst nach bestandener Scope-Probe ausgewiesen.

Stand der Umsetzung zu A in diesem Ticket: Seatbelt plus Pfadregeln (nachgewiesen für die getesteten Fälle),
Connectors-Einstellung gesetzt (Wirkung nicht belegt), Sync-Quellen über `setting_sources=[]` aus (belegt). Offen:
Git über Go, Toolchain-Caches, Netzwerk-Allowlist; B ist nicht erprobt.

- **Technisch nachgewiesen (nur für die getesteten Fälle):** Auf macOS mit CLI 2.1.311 bilden die Kombination aus
  OS-Sandbox für Shell-Prozesse und Permission-Regeln für Dateiwerkzeuge eine technische Dateisystemgrenze. Schreiben im
  Fixture gelingt, Schreiben und Lesen in Nachbar-, Secret- und Kontrollpfad-Attrappen scheitern über alle getesteten
  Wege (`Write`, `Read`, `Bash`, `python3`). Eine einzelne Ebene reicht nicht.
- **Nicht nachgewiesen, daher kein unbeaufsichtigter Modus:**
  1. Arbeiten im echten Worktree: `git commit` aus der Sandbox. Das `.git` eines Worktrees liegt außerhalb des
     Arbeitsverzeichnisses und ist ein geschützter Pfad. Nach der Entscheidung laufen Git-Schreibaktionen nur über Go.
     (Die aktuelle Settings-Doku nennt, dass das gemeinsame `.git`-Verzeichnis eines verknüpften Worktrees für Sandbox-Befehle
     les- und schreibbar bleibt; für 2.1.311 ungeprüft. Das wäre eine Lücke, kein Nachweis.)
  2. Testläufe der Werkzeugkette (`go test`, `uv run pytest`, Cache- und Toolchain-Pfade unter dem Home-Verzeichnis).
  3. Netzwerk: In der Sandbox sind Verbindungen blockiert, solange `allowedDomains` leer ist. Das wurde nicht
     eigens getestet; die Allowlist ist nicht umgesetzt.
  4. Weitere Wege außerhalb der Sandbox: Hooks, MCP-Server, `excludedCommands`, Unterprozess-Start von `claude`,
     Zählung ungezählter Agentenprozesse.
  5. Verhalten bei Versionswechsel der CLI und auf anderen Plattformen als macOS.
  6. Der Beleg gilt nur für ein Fixture unter `$TMPDIR`. Der Fall `denyRead` auf das Home-Verzeichnis mit einem
     Arbeitsverzeichnis **unterhalb** des Home-Verzeichnisses (echter Worktree) ist nicht belegt. Im Vorversuch lag das
     Fixture außerhalb von Home.
  7. Das Profil `readonly` hat keinen eigenen Live-Beleg für die Dateigrenze; es ist nur als Policy-Aufbau getestet und
     lief als Rechtegrundlage der Positivkontrolle (Lesen im Fixture, keine Schreibprobe).
  8. Die übrige Umgebung (`os.environ`, zum Beispiel `GH_TOKEN` oder andere Tokens) wird bisher an das Binary und die
     Bash-Kindprozesse weitergereicht. Der Runner **entfernt** zwar `ANTHROPIC_*`, `CLAUDE_CODE_*`, `CLAUDE_AGENT_SDK_*` und
     `CLAUDECODE` (Credential-Variablen werden entfernt und gemeldet, nicht abgewiesen), alles Weitere aber nicht. Eine
     kontrollierte Umgebung per Allowlist ist für M04 nötig (die Live-Tests nutzen sie bereits).
  9. `~/.claude` bleibt für die Dateiwerkzeuge lesbar (siehe „Read-Grenze und Meldung“).
  10. Ohne CLI-Upgrade steht `permissions.blockWritesOutsideWorkingDirectories` (laut Settings-Doku ab 2.1.314) nicht zur
      Verfügung; die Schreibgrenze der Dateiwerkzeuge beruht auf den pfadgebundenen Regeln.
  11. Fremde Quellen: Herkunft der 3 Plugins, Nachladen von Connectors, `CLAUDE.md` mit nicht deterministischer
      Positivkontrolle (siehe oben).

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
| Connectors und Plugins nach dem Start | Lauf, der nach dem Init-Frame wartet (Werkzeugliste prüfen), mit und ohne `disableClaudeAiConnectors`; Herkunft der 3 Plugins über die Einträge des Init-Frames (`source`) klären | Keine claude.ai-Connectors und kein MCP-Werkzeug in der Werkzeugliste, auch nicht verspätet; die Plugins sind erklärt oder abgeschaltet |
| Lesbarkeit von `~/.claude` | Lauf in der Sandbox mit `denyRead` auf `~/.claude` und Prüfung, ob die Dateiwerkzeuge (`Read`, `Grep`, `Glob`) dort trotz Lesegrenze lesen (Attrappe in einem Test-Konfigurationsverzeichnis, `CLAUDE_CONFIG_DIR`, nie das echte) | Zugriff auf Attrappen unter dem Konfigurationsverzeichnis scheitert, oder die Ausnahme ist bewusst akzeptiert und begrenzt |
| Gemeinsames `.git` im Worktree | `git`-Schreibaktion aus der Sandbox in einem verknüpften Worktree (Wegwerf-Repository) | Schreiben in das gemeinsame `.git` ist nicht möglich, oder die CLI-Version bestätigt die Ausnahme und Go übernimmt Git trotzdem allein |

**Spike B (äußeres `sandbox-exec`-Profil) und Abgrenzung:** Ein Profil um Runner und CLI soll Hooks, MCP, `~/.claude` und
die Umgebung abdecken, die die innere Grenze nicht erreicht. Es ist ohne zweites Konto einsetzbar und nutzt die vorhandene
Anmeldung, ist aber ein von Apple als veraltet geführtes Werkzeug, schwerer zu pflegen und trennt den Schlüsselbund nicht.
Ein eigener macOS-Benutzer (eigenes Home, eigene Anmeldung per `/login`) und Container wurden nicht gewählt.

## 6. Diagnosen ohne Konto-Zugriff

Jede Diagnose ist ein eigener Code im `error` (Art `runtime`, terminal, wenn nicht anders vermerkt), mit Test ohne Konto-Zugriff
(`tests/test_runtime_smoke.py` und `tests/test_runtime_units.py`; Doubles nur dort: Stub-Binary für `-v` und
`auth status`, Fake-Client mit echten SDK-Nachrichtentypen).

| Code | Auslöser | Test |
| --- | --- | --- |
| `not_logged_in` | `claude auth status` meldet `loggedIn: false`; auch Antwort `authentication_failed` in der Sitzung | `test_no_api_fallback_when_not_logged_in`, zusätzlich mit dem echten Binary und leerem `CLAUDE_CONFIG_DIR` (kein Netzwerk, kein Konto) |
| `not_subscription_login` | Anmeldung ist nicht claude.ai/firstParty | `test_no_api_fallback_when_login_is_not_claude_ai` |
| `api_credentials_removed` (**nicht terminal**) | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_USE_*` u. ä. waren in der Umgebung und wurden **entfernt**; nur Namen, nie Werte. Danach läuft die normale Vorprüfung mit der bereinigten Umgebung; ohne Anmeldung folgt `not_logged_in` | `test_credentials_in_environment_are_removed_with_a_warning_and_never_reach_the_cli`, `test_removed_credentials_and_not_logged_in_ends_with_terminal_not_logged_in`, `test_real_sdk_transport_passes_none_of_the_injected_variables_to_the_binary`; live: `test_live_sdk_roundtrip` |
| `cli_version_untested` | rohes Versions-Token von `claude -v` ist nicht exakt `2.1.311` (neuere Version, aber auch `2.1.311-rc.1`, `2.1.311.7`): **terminal** für `scoped_write` (Vorprüfung, keine Sitzung), **nicht terminal** (Warnung) für `readonly` | `test_untested_cli_version_blocks_the_writing_profile_before_any_session`, `test_newer_but_consistent_cli_runs_readonly_with_untested_warning`, `test_only_the_exact_verified_version_token_counts_as_tested`, `test_readonly_runs_with_a_warning_for_a_lookalike_version_token` |
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

## 8. Strukturierte Ausgabe als SDK-Fähigkeit (für M02)

**Nachgewiesen:** `ClaudeAgentOptions(output_format={"type": "json_schema", "schema": …})` (das SDK übergibt `--json-schema`)
liefert in der `ResultMessage` das Feld `structured_output`. Beleg `live-structured-output.jsonl` (Session
`2755b1ab-0890-4d16-bd63-9b35b3a64d93`, Haiku, Effort `low`, SDK 0.2.180, CLI 2.1.311): Schema
`{"answer": string, "count": integer}` (beide Pflicht, keine weiteren Felder), Ergebnis `{"answer": "ok", "count": 3}`, Untertyp
`success`, 2 Runden. Der Test prüft die Antwort mit `jsonschema` gegen das Schema. Die Sitzung lief mit den Rechten des Profils
`readonly` (nur `Read`) und `permission_mode: dontAsk`; das interne Werkzeug für die strukturierte Ausgabe war damit ohne
zusätzliche Freigabe nutzbar.

**Nicht nachgewiesen:** Anbindung im Runner. Es gibt keinen Vertrag, kein `RunSpec`-Feld und keinen Ereignisweg dafür; der
Nachweis lief über ein test-eigenes Probe-Skript (`tests/probe_sources.py`), nicht über `python -m claude_sdk_runner`. Offen
sind außerdem komplexe Schemata, das Verhalten bei Schemaverstoß oder Abbruch und das Zusammenspiel mit Schreibprofilen.
**Anbindung in M02** (Vertragsänderung dort entscheiden, zum Beispiel für `review_report`).

## 9. Skills in der Sitzung (CC-35)

Frage: Gelangen genau die freigegebenen Skills einer Ausführung, und keine anderen, in die SDK-Sitzung des Runners? Der
Runner startet mit `setting_sources=[]`; Projekt-, Benutzer- und synchronisierte Skills sind damit aus (Abschnitt 5). Skills
müssen ausdrücklich eingebracht werden. Aufbau: [`skill-migration.md`](skill-migration.md), „Einbringung in die Laufzeit“;
Vertrag und Fehlercodes: [`contracts.md`](contracts.md), „Skills im Protokoll“.

**Mechanismus (SDK 0.2.180, Quelltext gelesen, bestätigt die SDK-Doku):** `ClaudeAgentOptions` hat `plugins` (Liste
`{"type":"local","path":…}`, das SDK übergibt `--plugin-dir`) und `skills` (Liste, `"all"` oder `None`). `skills=[Namen]` hängt je Name
die Regel `Skill(<name>)` an `--allowedTools` und sendet die Liste im Initialize-Request an die CLI (Filter der Auflistung);
`skills=None` ändert nichts (CLI-Standard, also **nicht** „aus“), `skills=[]` ist die ausdrückliche Form „kein Skill“. Namen werden vor
dem Start geprüft: leere Namen, Klammern, Kommas, Steuerzeichen, `*`, Namen mit Suffix `:*` oder ` *`, führender `/` und Leerzeichen am
Rand lösen eine Ausnahme aus; `plugin:skill` ist ausdrücklich zulässig. Laut SDK-Doku ist das ein **Kontextfilter, keine Sandbox**:
die Dateien nicht gelisteter Skills bleiben auf der Platte und über Read/Bash erreichbar. Bei einer expliziten `tools`-Liste muss das
Skill-Werkzeug in dieser stehen. Der Init-Frame führt `plugins` (mit `name` und `path`) und `skills`. Abweichende Befunde gegenüber der
Aufgabenstellung: keine.

**Umsetzung im Runner:**

- Ohne Snapshot (oder mit einem Snapshot ohne Skills): `skills=[]`, keine Plugins, kein `Skill` in `tools`.
- Mit Snapshot: `plugins=[{"type":"local","path":<realpath>}]`, `skills=["ccmas:<id>", …]`, `tools` = Profil-Werkzeuge plus `Skill`.
  Die Rechte des Profils (`allowed_tools`, Sandbox, Settings, `dontAsk`) sind unverändert; das Snapshot-Verzeichnis wird nirgends
  zum Lesen freigegeben. Die Live-Läufe liefen nur mit `readonly`; für beide Profile sind die Optionen mit Fake-Client geprüft.
- Init-Frame (`runtime.check_skills_loaded`): Plugin `ccmas` mit genau dem aufgelösten Pfad des Snapshots, jeder `ccmas:<id>` in
  `skills`, kein weiterer `ccmas:`-Eintrag, kein weiteres Plugin namens `ccmas`; fehlende Listen gelten als nicht bestanden. Sonst
  `skills_not_loaded` über den `_Abort`-Weg. Wie bei `api_billing_detected` ist garantiert, dass der Runner danach nichts mehr
  verarbeitet und unterbricht, nicht dass die CLI die Anfrage nicht schon entgegengenommen hat.
- `tool_event.summary` des Skill-Werkzeugs nennt den aufgerufenen Skill-Namen (zum Beispiel `ccmas:ccmas-skillprobe`), nie Inhalte.

**Live-Nachweis** (`tests/test_live_skills.py`, Beleg `live-skill-snapshot.jsonl`; CLI 2.1.311, SDK 0.2.180, Modell
`claude-haiku-5-5`, Effort `low`, Profil `readonly`, `--max-turns 6`, Allowlist-Umgebung). Der Test-Katalog mit Probe-Skill
(`ccmas-skillprobe`, Markierung nur in seinem Inhalt) und einem zweiten Skill (`ccmas-skillother`, nicht im Snapshot) entsteht zur
Laufzeit im Temp-Verzeichnis, nicht im Repository; der Snapshot stammt von der echten Go-CLI (`ccmas skills snapshot`), die Hashes im
Test-Manifest berechnet das Python-Modul.

| Lauf | Session | Ergebnis |
| --- | --- | --- |
| a) Snapshot-Skill läuft | `8f8205af-7229-4b5e-964f-e2d17e203230` | `session_started.skills` = `["ccmas-skillprobe"]`, `skill_snapshot_id` = Kennung aus Go; Skill-Werkzeug `started` und `finished` für `ccmas:ccmas-skillprobe`; die Markierung steht im `result` (`succeeded`); genau ein terminales Ergebnis. Die Markierung erscheint nicht im Log (stderr). |
| b) Nicht gelisteter Skill | `368ed56c-ca36-4b70-9f55-d4e3e0d22eb6` | Drei Aufrufe, alle `failed`, keiner erfolgreich: `code-review` (mitgeliefert): „Skill code-review is not in this session's skills allowlist“; `verify` (mitgeliefert): „… cannot be used with Skill tool due to disable-model-invocation“ (eine Eigenschaft dieses Skills, kein Beleg für die Allowlist); `ccmas-skillother` (im Katalog, nicht im Snapshot): „Unknown skill“. Der Inhalt des nicht freigegebenen Skills kam nie an. |

Init-Frame in beiden Läufen: `mcp_servers` 0, `plugins` 4 (die 3 bekannten plus das Plugin des Snapshots), `skills` 20 (die 19
mitgelieferten plus der Probe-Skill), `agents` 5, `slash_commands` 56, `plugins_in_config_dir` 0. Die mitgelieferten Skills stehen also
weiter im Init-Frame, sind aber über das Skill-Werkzeug nicht aufrufbar, solange `skills` gesetzt ist; die Prüfung des Runners
betrachtet deshalb nur `ccmas:`-Einträge. Das Plugin trägt im Init-Frame den Namen `ccmas` und den aufgelösten Pfad (sonst wäre die
Prüfung des Runners nicht bestanden). In CC-35 liefen insgesamt **4 echte Sitzungen**: zwei Entwicklungsläufe (a:
`7bff06ea-2de2-4137-84a3-a771c09cf1ce`, b: `e267e18d-353a-4ee7-b6a3-8f38e1c6d438`, gleiches Ergebnis) und die beiden Abschlussläufe
oben, deren Beleg eingecheckt ist.

**Nicht nachgewiesen:**

- **Ressourcen neben `SKILL.md`** (Skripte, Dokumente): Der Probe-Skill besteht nur aus `SKILL.md`. Dateien daneben müsste das Modell mit
  `Read`/`Bash` lesen; die Lesegrenze (`blockReadsOutsideWorkingDirectories`, Sandbox) erlaubt das Snapshot-Verzeichnis nicht, und der
  Runner schaltet es nicht frei. Ob ein Skill mit Ressourcen so nutzbar ist, ist offen; ein Freischalten ändert die Berechtigungsgrenzen
  und braucht die Freigabe des Projektinhabers.
- **Sitzung ohne Snapshot live:** `skills=[]` und das Fehlen des `Skill`-Werkzeugs sind nur mit Fake-Client belegt; dass die CLI dann
  wirklich keinen Skill aufrufbar macht, wurde nicht live geprüft.
- **Profil `scoped_write` mit Skills**, Wirkung der Allowlist auf Agents und das Task-Werkzeug: nicht geprüft.
- **Veränderung nach der Prüfung:** Zwischen Prüfung und Lesen durch die CLI schützt nur das Schreibschutz-Layout des Snapshots.
- **Herkunft der 3 weiteren Plugins** (M04, Punkt 1) bleibt offen; sie tragen nicht den Namen `ccmas` (sonst wäre die Prüfung
  fehlgeschlagen).

## Offene Punkte für M04 (CC-40) und folgende

1. **Plugins unbekannter Herkunft (Abschnitt 5, „Fremde Quellen“):** Der Init-Frame nennt 3 Plugins mit Pfad außerhalb des
   Konfigurationsverzeichnisses, ohne Marketplace-Kennung; vermutlich mit der CLI geliefert, unbestätigt. Herkunft klären
   (Einträge des Init-Frames, `source`) und, wenn nötig, abschalten.
2. **Sync-Schalter `syncClaudeAiSkills`/`syncClaudeAiPlugins` bewusst nicht gesetzt (Abschnitt „Der Runner in diesem
   Stand“, Abschnitt 5):** `false` in Benutzer- oder verwalteten Einstellungen verschiebt laut Settings-Doku bereits
   synchronisierte Skills und Plugins nach `.trash`. Per `--settings` prüfen, ob die Schalter ohne Verschiebung wirken (nur
   dann wäre ein Setzen zu erwägen).
3. **Connectors (Abschnitt 5, „Fremde Quellen“):** Die Wirkung von `disableClaudeAiConnectors` ist nicht belegt (`mcp_servers`
   ist auch ohne die Einstellung 0). Ein spätes Nachladen von Connectors nach dem Init-Frame ist nicht ausgeschlossen und
   zu prüfen.
4. **`CLAUDE.md` und mitgelieferte Skills/Agents (Abschnitt 5, „Fremde Quellen“):** Die Positivkontrolle für `CLAUDE.md` ist
   nicht deterministisch (Modellverhalten, siehe Einschränkungen); die Negativaussage im Runner-Lauf bleibt schwächer als
   die für Hook und Skill. Mitgelieferte Skills (19) sind mit der Allowlist aus CC-35 über das Skill-Werkzeug nicht aufrufbar
   (Abschnitt 9, Lauf b: `code-review` mit Allowlist-Meldung, `verify` mit `disable-model-invocation`); offen bleibt, ob die
   mitgelieferten Agents (5) in einer Sitzung mit Positivliste in `tools` nutzbar sind.
5. **`skill_names` in der Init-Zeile auf stderr (Abschnitt „Der Runner in diesem Stand“):** Würde je eine Benutzer- oder
   Projektquelle geladen, stünden persönliche Skill-Namen im Log, auch in Test-Fehlermeldungen über `proc.stderr`. Die
   Namen nur hinter einem Diagnose-Schalter ausgeben.
6. **Lesbarkeit von `~/.claude` (Abschnitt 5, „Read-Grenze und Meldung“):** Die Dateiwerkzeuge können `~/.claude` trotz
   `blockReadsOutsideWorkingDirectories` lesen (laut Settings-Doku Skills, Plugins, Regeln, Agents, Commands und
   `CLAUDE.md`). Nur dokumentiert, nicht geprobt; die Wirkung (zum Beispiel Konfiguration mit Inhalten, die nicht in eine
   Sitzung gehören) und die Gegenmaßnahme (eigenes Konfigurationsverzeichnis je Ausführung über `CLAUDE_CONFIG_DIR`,
   Sandbox-`denyRead`) sind zu entscheiden.
7. **Schreibgrenze ohne CLI-Upgrade (Abschnitt 3, Abschnitt 5):** `permissions.blockWritesOutsideWorkingDirectories` gibt es
   laut Settings-Doku erst ab CLI 2.1.314; die lokale 2.1.311 hat sie nicht, und ein Upgrade ist nicht beschlossen. Vor einem
   Upgrade wären die Versionssperre (`VERIFIED_CLI_VERSION`) und der gesamte Nachweis zu wiederholen.
8. **Isolationslücken nach Entscheidung A plus Spike B vom 10.11.2026 (Abschnitt 5, „Isolationsurteil“):** Git-Schreibaktionen
   nur über Go, Toolchain-Caches je Ausführung im erlaubten Bereich, Netzwerk nur über eine Allowlist, Werkzeugkette in der
   Sandbox, Hooks/MCP, Spike B (äußeres `sandbox-exec`-Profil um Runner und CLI) und eine kontrollierte Umgebung per
   Allowlist, bevor ein unbeaufsichtigter Modus ausgewiesen wird.
9. **`scoped_write` ist ein Prüfprofil, das in Go erzwungen werden muss (Abschnitt „Der Runner in diesem Stand“, Abschnitt 5):**
   Das Profil ist technisch für jedes Arbeitsverzeichnis startbar. Der Status „Prüfprofil“ ist in Go durchzusetzen, bis ein
   unbeaufsichtigter Modus nach bestandener Scope-Probe freigegeben ist.
10. **Laufzeitgrenze (Abschnitt „Der Runner in diesem Stand“, „Grenzen“):** Die Aufräumzeit eines abgebrochenen `connect()` im
    SDK (`disconnect()`) ist nicht vom Runner begrenzt (unbegrenzte SDK-Aufräumzeit); auf einem Rechner im Schlaf oder bei
    hängendem CLI-Prozess bleibt die Prozessgruppen-Beendigung durch Go (M04) maßgeblich. Randfall: Läuft die Zeitgrenze ab,
    während ein `_Abort` (zum Beispiel `api_billing_detected`) den Interrupt auslöst, kann der Timeout die Ursache
    überdecken; das Ergebnis ist dann `max_runtime_exceeded` statt der eigentlichen Diagnose.
11. **Systemprompt (Abschnitt „Der Runner in diesem Stand“):** Ohne `system_prompt` startet das SDK mit `--system-prompt ""`;
    ob Runner-Sitzungen einen eigenen Prompt oder den Standardprompt von Claude Code bekommen, ist in M02 zu wählen.
12. **Live-Tests nur auf diesem Rechner:** `REAL_CLAUDE` in `tests/support.py` ist fest auf `/opt/homebrew/bin/claude`
    gesetzt. Die Live-Tests und der Test mit leerem `CLAUDE_CONFIG_DIR` laufen nur auf einem Rechner mit diesem Binary
    (sonst werden sie übersprungen).
13. **Interrupt-Race:** Ein Interrupt/Stop, der vor dem Senden der Anfrage eintrifft, wird vorgemerkt und direkt nach
    `client.query` angewendet (Test mit Fake-Client). Nicht live belegt: ob die CLI einen Interrupt unmittelbar nach dem
    Senden der Anfrage immer vor dem ersten Modellaufruf wirksam abbricht.
14. **Abo-Wartezustand in Go:** Der Runner sendet einen nicht terminalen `error` (`kind: subscription_limit`) und danach
    ein terminales `result` `controlled_failure`. Wie Go das als Wartezustand ohne Crash-Zählung behandelt, ist offen.
15. **EOF auf stdin** bedeutet „keine weiteren Kommandos“, der Lauf läuft weiter. Erkennung verwaister Runner und
    Beendigung ganzer Prozessgruppen (Schlaf, SIGTERM) gehören zu M04.
16. **Fragen/Antworten, Teilnachrichten-Streaming, Resume** sind nicht angebunden beziehungsweise nicht ausgeführt.
17. **Strukturierte Ausgabe:** SDK-Fähigkeit nachgewiesen (Abschnitt 8); Vertrag und Anbindung in M02.
18. **Policy-Profile** sind bis zur Einführung des Katalogs in Go (M03) im Runner festgelegt (`policy.py`).
19. **Skills (Abschnitt 9):** `skill_ids` werden aus einem geprüften Snapshot angewendet. Offen: Lesezugriff auf Ressourcen neben
    `SKILL.md` (nicht freigeschaltet, Freigabe des Projektinhabers nötig), Live-Beleg für eine Sitzung ohne Snapshot, Lauf mit
    `scoped_write`, der Go-Adapter, der `RunSpec.skill_snapshot` setzt (CC-46), und das Verhalten der Allowlist gegenüber Agents.
20. **Bedingungen:** Die Unschärfe aus Abschnitt 1 bleibt als Hintergrund dokumentiert; der Betrieb ist durch die Entscheidung
    des Projektinhabers vom 10.11.2026 angenommen und blockiert M02 bis M04 nicht. Zu beobachten bleibt die Aussage „for now“ der
    Support-Seite.
