# Technische Umsetzung in Claude Code

Stand: Plugin `ccmas` 0.3.0 im Repository `alexwenzel/ccmas`, Ordner `claude-plugin/`. Es setzt diese Spezifikation um. In einer echten Sitzung mit Board ist es noch nicht erprobt ([Offene Punkte](offene-punkte.md)).

## Wie Claude Verhalten lernt

Claude wird nicht trainiert, sondern angeleitet. Das Verhalten steht in Textdateien, die im Projekt liegen oder per Plugin kommen.

| Baustein | Was es ist | Wann es wirkt |
| --- | --- | --- |
| Regeldatei (`CLAUDE.md`) | Dauerhafte Regeln des Projekts: Referenzen, Prüfbefehle, Arbeitsweise, Grenzen. Projektspezifisch | In jeder Sitzung, von Anfang an |
| Skill | Anleitung (`SKILL.md`) für genau einen Ablauf, bei Bedarf mit weiteren Dateien. Liegt im Projekt oder kommt per Plugin | Wenn er beim Namen aufgerufen wird (`/ccmas:<name>`) oder die Aufgabe zu seiner Beschreibung passt |
| Agent | Eine Rollendefinition: Auftrag, Modell, Grenzen. Liegt im Projekt oder kommt per Plugin | Als ganze Sitzung (`claude --agent`) oder als Subagent einer Sitzung |
| Plugin | Ein Paket aus Skills und Agents, das je Verzeichnis oder global aktiviert wird | Solange es aktiviert ist |

- **Der Skill sagt, wie etwas gemacht wird.** Von jedem Skill ist zunächst nur die Kurzbeschreibung geladen, der volle Text erst beim Aufruf. Das ganze Plugin kostet so nur wenige hundert Tokens je Sitzung.
- **Der Agent sagt, wer es macht.** In CCMAS ist er eine schmale Hülle: Rolle, Modell, Grenzen und der Verweis auf seinen Skill. Die eigentliche Arbeit steht im Skill.
- **Agents im Plugin sind eingeschränkt.** Claude Code ignoriert bei Agents aus einem Plugin die Angaben `permissionMode`, `hooks` und `mcpServers`. Rechte kommen deshalb vom Start der Sitzung (`--permission-mode`), nicht aus der Agent-Datei.
- **Mit `--agent` ersetzt der Text des Agents den Standard-Systemprompt von Claude Code vollständig.** Die `CLAUDE.md` wird trotzdem geladen. Ob die knappen Agent-Texte als ganzer Systemprompt genügen, ist noch nicht erprobt ([Offene Punkte](offene-punkte.md)).
- **Subagents dürfen in Claude Code eigene Subagents starten,** in der Voreinstellung drei Ebenen tief. CCMAS nutzt das nicht: Jede Rolle ist eine eigene Sitzung, und jeder Agent hat die Vorgabe, keine Subagents zu starten.

## Agents und Skills je Phase

| Phase                                                                   | Rolle      | Skills                                | Stand     |
| ----------------------------------------------------------------------- | ---------- | ------------------------------------- | --------- |
| Projekt einrichten (einmalig)                                           | Mensch     | `init`; das Board legt er von Hand an | im Plugin |
| Projekt in Milestones zerlegen | Project Planner mit dem Menschen | `project-planning`, `ticket` | im Plugin |
| Milestone konzipieren und in Tasks zerlegen | Feature Planner mit dem Menschen | `feature-planning`, `ticket` | im Plugin |
| Board bereinigen, Worktrees aufräumen, nächste Arbeit bestimmen | Supervisor | `supervisor`, `ticket` | im Plugin |
| Ticket ausplanen                                                        | Planner    | `task-planning`, `ticket`             | im Plugin |
| Umsetzen und PR erstellen                                               | Worker     | `feature-parallel`, `ticket`          | im Plugin |
| PR prüfen                                                               | Reviewer   | `code-review`, `ticket`               | im Plugin |
| Integrieren                                                             | Merger     | `merge`, `ticket`                     | im Plugin |
| Milestone abschließen | Supervisor | `supervisor`, `ticket` | im Plugin |

## Noch zu bauen

| Skill                 | Zweck                                                                       | Stand                                                                                               |
| --------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Risikoregeln am Diff  | Feste Regeln statt Modell-Urteil für die Risikoklasse                       | Ausbau, als Skript im Skill `merge`                                                                 |

Rollen, Startbefehle, Skills und Aufbau des Plugins stehen in der [README des Plugins](../../claude-plugin/README.md). Was `/ccmas:init` im Projekt anlegt, steht in der [README des Projekts](../../README.md#was-im-projekt-liegt).
