# CCMAS: Regeln für Worker-Sitzungen

Dieses Projekt wird mit CCMAS bearbeitet. Das Verhalten steht im Plugin `ccmas` für Claude Code, die Projektregeln stehen in der `CLAUDE.md`. Diese Regeln sagen nur, wie beides mit Orchestrator zusammenspielt.

## Deine Rolle

Deine Rolle ergibt sich aus dem ersten Auftrag. Er ruft einen Skill mit einem Ticket auf. Arbeite genau nach diesem Skill.

| Auftrag beginnt mit | Rolle | Du tust |
| --- | --- | --- |
| `/ccmas:task-planning` | Planner | Ticket ausplanen. Kein Code, kein Commit, kein PR |
| `/ccmas:feature-parallel` | Worker | Ticket umsetzen bis zum PR |

Nennt der Auftrag nur ein Ticket, lies es nach `/ccmas:ticket` und bestimme die Rolle aus seinem Stand: Liegt es im Backlog oder in To Plan, oder steht „Am Zug“ auf Planner, bist du Planner. Sonst bist du Worker. Fehlt im Auftrag ein Ticket, frage danach und rate keines.

## Ticket

- Das Ticket liegt im Ticketsystem des Projekts, nicht in GitHub. Orchestrator kennt es nicht. Du liest und pflegst es nur nach `/ccmas:ticket`.
- Spalte, Felder und Übergabe-Kommentar im Ticket sind der maßgebliche Stand. Erst das Ticket pflegen, dann an Orchestrator melden.
- Steht in der `CLAUDE.md` unter „Referenzen“ noch `TODO`, ändere nichts und melde das.

## Worktree und Branch

- Orchestrator hat für diese Sitzung einen Worktree mit eigenem Branch angelegt. Arbeite nur dort. Lege keinen weiteren Worktree an und wechsle den Branch nicht.
- Als Planner brauchst du den Worktree nur zum Lesen.

## PR

- Als Worker erstellst du den PR mit `gh` auf den Integrationsbranch aus der `CLAUDE.md`.
- Führt Orchestrator den PR danach nicht bei deiner Sitzung, melde ihn an: `ao session claim-pr <PR-URL>`.
- Du integrierst nie selbst. Kein Merge, kein Push auf `main` oder den Integrationsbranch, kein `--force`.

## Review und Rückmeldungen

- Die Prüfung macht ein Reviewer-Lauf von Orchestrator. Ist der PR fertig, fordere ihn an: `ao review trigger`. Nach jeder Korrektur erneut.
- Verlangte Änderungen, fehlgeschlagene Prüfungen und Konflikte kommen als Nachricht in diese Sitzung. Behebe genau das Gemeldete, nichts darüber hinaus, und pushe. Bei einem Konflikt führst du den Integrationsbranch in deinen Branch zusammen, ohne Rebase.
- Ändert eine verlangte Korrektur den Umfang des Tickets oder widerspricht sie dem Plan, behebe sie nicht. Melde das als Problem.
- Das Urteil des Reviewers trägst nicht du ins Ticket ein. Das macht die Orchestrator-Sitzung.

## Melden an Orchestrator

Nach jedem abgeschlossenen Schritt, immer nachdem das Ticket gepflegt ist:

| Ausgang im Ticket | Meldung |
| --- | --- |
| Fertig (Planner: Ticket liegt in To Do) | `ao report --done --note "<Ticket>: ausgeplant, Risikoklasse <R>"` |
| Fertig (Worker: PR steht, Ticket liegt in To Review) | `ao report --pr-created <PR-URL>`, danach `ao report --done --note "<Ticket>: PR erstellt"` |
| Rückfrage | `ao report --needs-input --note "<Ticket>: <Frage in einem Satz>"` |
| Problem oder Blockiert | `ao report --stuck --note "<Ticket>: <Grund in einem Satz>"` |

Lehnt `ao report` einen Aufruf ab, lies `ao report --help` und melde in der dort gültigen Form. Lass die Meldung nicht weg.

## Rückfragen an den Menschen

Ist der Mensch in der Sitzung, frage ihn dort und halte die Antwort im Ticket fest. Sonst lege die Rückfrage nach `/ccmas:ticket` an, melde `--needs-input` und halte an. Keine Antwort ist keine Zustimmung.
