# CCMAS – Runtime-Fähigkeiten und technische Nachweise (CC-17)

Stand: **Zwischenstand von Ticket CC-17 (Meilenstein M01), Datum der Recherche und Läufe: 2026-11-10.**
Das Ticket ist **nicht abgeschlossen**: Recherche und Vorversuche liegen vor, der Runner-Code, die Tests und die
Belegdateien fehlen noch (Abschnitt „Weiter geht es hier“). Führend ist die Spezifikation „Multi Agent Setup“
(Version 1.2.1), Abschnitte „Go und SDK zusammenführen“, „Runner-Protokoll“, „Anmeldung und Abonnement“,
„Berechtigungen und technische Isolation“, „Technische Nachweise und Freigabepunkte“ und „Remote Control, mobile
Geräte und Desktop“. Diese Datei hält die Befunde fest und ist keine Kopie der Spezifikation.

Eingesetzte Versionen bei allen Läufen: `claude-agent-sdk` 0.2.180 (gepinnt), lokales Claude Code 2.1.311
(`cli_path` ausdrücklich gesetzt, nicht die gebündelte CLI), Python 3.13, macOS (Seatbelt), Modell
`claude-haiku-5-5` mit Effort `low`, Anmeldeart claude.ai (Max-Abonnement).

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
  überschreiben, nicht löschen. Der Runner muss `os.environ` deshalb **vor** dem Start selbst bereinigen. In der
  Entwicklungssitzung waren `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_*` und `CLAUDE_CODE_CHILD_SESSION`
  gesetzt; sie dürfen nicht an die Kindsitzung gehen.

## 3. Kompatibilität SDK 0.2.180 und CLI 2.1.311

**Nachgewiesen** (reale Läufe, siehe Abschnitt 4): Start über `cli_path` auf `/opt/homebrew/bin/claude`, Init-Frame mit
`claude_code_version` 2.1.311, Werkzeugereignisse, `ResultMessage`, Interrupt-Fähigkeit im Init
(`capabilities` enthält `interrupt_receipt_v1`). Das SDK prüft die CLI-Version nur auf `>= 2.0.0` und warnt nur.
Eine eigene Mindestversion muss der Runner selbst erzwingen (Vorschlag: genau die nachgewiesene Version als
Untergrenze, neuere Versionen als Warnung „ungetestet“).

**Nicht nachgewiesen:** funktionale Verträglichkeit mit anderen CLI-Versionen, insbesondere der im SDK gebündelten
2.1.321. Es gibt keine offizielle Kompatibilitätsmatrix.

## 4. Echte SDK-Läufe (Vorversuche, kleine Läufe)

Alle Läufe: temporäres Fixture unter `$TMPDIR`, `max_turns` 3 bis 16, kein Fan-out. Bisher **7 Läufe**.

| Lauf | Ergebnis |
| --- | --- |
| Basislauf (Write-Werkzeug, `dontAsk`) | Sitzung gestartet (Session-ID, Modell `claude-haiku-5-5`, `apiKeySource: none`), `Write`-Werkzeugereignis, Datei im Fixture angelegt, genau ein `ResultMessage` (`success`, `terminal_reason: completed`). Die gemeldeten Kosten sind lokal zum Listenpreis berechnet, es wurde nichts über API abgerechnet. |
| Isolationsläufe | siehe Abschnitt 5 |

Auch ein `RateLimitEvent` (Status `allowed`, Fenster `five_hour`, Überziehung `rejected`, Grund `org_level_disabled`)
wurde beobachtet. Der Event liefert damit die Grundlage für die Diagnose „Abo-Wartezustand“ (Status `rejected`) und
zeigt, dass keine Überziehungsabrechnung aktiv ist.

**Nicht nachgewiesen:** Das wirksame Effort-Niveau. Der Init-Frame enthält kein Effort-Feld. Der Runner kann nur den
übergebenen Wert melden; „wirksam“ ist für Effort nicht prüfbar. Modell (wirksam) und Berechtigungsmodus stehen im
Init-Frame.

## 5. Scope und Isolation: Befund

Aufbau: Fixture (erlaubt), Nachbarverzeichnis, Secret-Attrappe, Kontrollpfad-Attrappe (`control/contracts/`), alles in
einem temporären Baum, keine echten Secrets, keine Repo-Pfade. Zehn Schritte je Lauf (je ein Werkzeugaufruf):
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

Verwendete Mittel in der besten Variante: `permission_mode: dontAsk`, `allowed_tools` mit auf das Fixture begrenzten
`Write(//…/**)`, `Edit(…)`, `Read(…)` und `Bash`, `setting_sources: []`, Sandbox mit `enabled`, `failIfUnavailable`,
`allowUnsandboxedCommands: false`, `autoAllowBashIfSandboxed`, `filesystem.allowWrite`, `filesystem.denyRead`,
`filesystem.allowRead`. Pfade müssen als `realpath` angegeben werden (`/var/…` ist `/private/var/…`).

### Isolationsurteil (vorläufig)

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
- **Lösungsvorschlag für M04:** Sandbox-Konfiguration und Permission-Regeln aus einem Policy-Profil erzeugen (Standard
  verweigern, Fixture erlauben, `failIfUnavailable`, kein `dangerouslyDisableSandbox`). Zusätzlich eine
  Betriebssystemgrenze außerhalb des SDK prüfen: dedizierter macOS-Benutzer oder Seatbelt-Profil (`sandbox-exec`) für den
  gesamten Runner-Prozess, um auch Hooks und MCP abzudecken. Git-Schreibaktionen über den Go-Dienst.

## 6. Diagnosen ohne Konto-Zugriff (geplant, Tests offen)

Fehlerarten, die der Runner als eigene Codes ausgeben soll (Vertrag: `error`, Klassen `runtime` oder
`subscription_limit`): kein Konto/Login (`not_logged_in`), Anmeldung ist nicht claude.ai
(`not_subscription_login`), API-Schlüssel oder Provider-Variable in der Umgebung
(`api_credentials_in_environment`, nur Variablennamen, nie Werte), SDK/CLI-Inkompatibilität
(`sdk_cli_incompatible`), Abo-Wartezustand (nicht terminaler `error` mit `kind: subscription_limit`). Es gibt keinen
API-Fallback und keine Token-Vermittlung.

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

## Weiter geht es hier (Übergabe, Stand 2026-11-10)

Erledigt: Recherche der Bedingungen (Abschnitt 1), Kompatibilität des Zusammenspiels SDK 0.2.180 mit CLI 2.1.311 im
realen Lauf, Isolationsvorversuche (Abschnitt 5), Remote-Vorprüfung (Abschnitt 7). **Im Repository wurde kein Code
und kein Test geändert.** Die Vorversuche liefen mit Wegwerf-Skripten außerhalb des Repositorys.

Offen, in dieser Reihenfolge:
1. **Rot-Phase:** `runners/claude_sdk/tests/test_runtime_smoke.py` mit `test_live_sdk_roundtrip` (Marker `live`),
   `test_scope_probe` (live, plus nicht-live Prüfung der Policy-Optionen) und `test_no_api_fallback`, dazu Tests je
   Diagnose (Stub-Binary im Test, kein Konto). Marker `live` in `pyproject.toml` registrieren und standardmäßig
   überspringen (`CCMAS_LIVE_TESTS=1` schaltet ein).
2. **Code** im Paket `claude_sdk_runner`: `entrypoint.py` (JSON-Lines von stdin, Validierung mit `contracts`, nur
   Protokoll auf stdout, genau ein terminales Ergebnis), `runtime.py` (SDK-Sitzung über `ClaudeSDKClient`, Umgebung
   bereinigen, Vorprüfung mit `claude -v` und `claude auth status`, `apiKeySource` prüfen, Policy-Profile `readonly`
   und `scoped_write` nach Abschnitt 5, `autonomous` ablehnen), `__main__.py`. Zuordnung der SDK-Nachrichten: Init zu
   `session_started`, Text und Thinking zu `activity`, `ToolUseBlock`/`ToolResultBlock` zu `tool_event`, `ResultMessage`
   zu `result`, `RateLimitEvent` mit Status `rejected` zu `error` mit `kind: subscription_limit` (nicht terminal).
   Interrupt über `ClaudeSDKClient.interrupt()`; `terminal_reason` `aborted_*` ergibt `cancelled`.
3. **Vertragslücke (Go und Schema, minimal):** `session_started` hat keine Felder für wirksamen Berechtigungsmodus und
   gewählte CLI. Vorschlag: optionale Felder `permission_mode`, `claude_binary` und `auth_source` in
   `contracts/runner-v1.schema.json`, Go-Typ `SessionStarted`, Fixtures auf beiden Seiten, `contracts.md`.
4. **Live-Läufe** (je einer, klein): Roundtrip, Interrupt mit genau einem terminalen Ergebnis, Scope-Probe über den
   Runner. Redigierte Belege als JSONL unter `docs/architecture/evidence/` mit Datum, SDK-, CLI-Version und Modell.
5. Dieses Dokument vervollständigen (Abschnitte 4 bis 6 mit den echten Läufen), in `ccmas-design.md` nur einen
   Verweis ergänzen, dann `make check`, `gofmt -l .`, `go mod tidy -diff`, `uv lock --check`, frischer Klon.

Die Vorversuchs-Skripte lagen unter `$TMPDIR/ccmas17-scratch/` (nicht versioniert, gehen eventuell verloren). Ihr
Inhalt steht in Abschnitt 5 so weit beschrieben, dass er nachgebaut werden kann.
