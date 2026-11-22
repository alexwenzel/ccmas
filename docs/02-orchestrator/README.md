# CCMAS mit Orchestrator

Orchestrator (CLI `ao`, [orchestrator.inc](https://orchestrator.inc)) ist der vorgesehene [Runner](../01-spezifikation/runner.md) für CCMAS. Er hält den Ablauf am Laufen: Sitzungen, Worktrees, Ereignisse aus dem PR, Reviewer-Läufe, Benachrichtigungen. Rollen, Board, Planung und Freigaben kommen unverändert aus dem Plugin ([Spezifikation](../01-spezifikation/README.md)).

Zwei Regeldateien unter [`orchestrator/`](../../orchestrator/) bringen die Zuordnung in die Sitzungen. Wie sie eingetragen werden, steht in [einrichtung.md](einrichtung.md).

**Stand:** Die Zuordnung stammt aus der Dokumentation von Orchestrator und aus einer Prüfung am 2026-11-22 mit Orchestrator 0.15.3 ([Geprüft](#geprüft)). Die Regeldateien in dieser Fassung sind noch in keinem Lauf erprobt ([Noch offen](#noch-offen)).

## Zuordnung

| Verb | Orchestrator |
| --- | --- |
| Starten | Supervisor: der Projekt-Orchestrator. Orchestrator lässt je Projekt genau einen zu; `ao spawn --project <Projekt> --kind orchestrator --name supervisor` startet ihn oder liefert den laufenden zurück. Er bekommt `/ccmas:supervisor [Milestone]` als Nachricht. Worker: `ao spawn --kind worker --name "<Ticket>" --branch ticket/<Ticket> --prompt "/ccmas:worker <Ticket>"`. Hält eine laufende Sitzung den Branch, scheitert ein zweiter Start |
| Fortsetzen | Nachricht an die Sitzung: `ao send --session <Sitzung> --message "…"`. Beendete Sitzung: `ao session restore <Sitzung>`; sie behält Worktree, Branch und Verlauf. Ein neuer Start mit demselben Branch übernimmt nur den Branch |
| Laufendes erkennen | `ao session ls --json`: je Sitzung Aktivität, Status, PR, Prüfungen und offene Kommentare. Das Urteil des Reviewers steht dort nicht. Eine Sitzung heißt wie ihr Ticket |
| Review holen | Der Reviewer-Lauf von Orchestrator, vom Worker angefordert: nach dem PR `ao review trigger --pr <URL>`, nach jedem Push `ao review trigger`. Ein Lauf gilt für einen Commit; derselbe Commit wird nicht zweimal geprüft. Befunde kommen als Nachricht beim Worker an, die Freigabe nicht: Er wartet, bis `ao review ls --json` für seinen Commit einen abgeschlossenen Lauf zeigt |
| Urteil lesen | `ao review ls <Sitzung> --json`: `latestRun.verdict` und `targetSha`. Der Supervisor mergt nur, wenn das Urteil `approved` lautet und `targetSha` der Commit des PR ist |
| Ereignisse | Eingebaut: Review-Kommentare und Konflikte gehen als Nachricht an die Sitzung, der der PR gehört; ein Konflikt kam in der Prüfung nach 46 Sekunden an. Für rote Checks gilt das laut Dokumentation ebenso. Der Supervisor bekommt diese Ereignisse nicht, und nach dem Beenden der Sitzung bekommt sie niemand |
| Melden | Erst das Ticket pflegen, dann `ao report` mit `--note "<Ticket>: …"`. Rückfrage: `--needs-input`, sofort zugestellt. Freigegeben und grün: `--done --pr-created <URL>`, zugestellt fünf Minuten nach dem Absenden. Nichts geht mehr: `--stuck`. Alles andere kommt erst mit der nächsten Meldung oder nach bis zu einer Stunde an |
| Mergen | `ao pr merge <Nummer> --url <URL> --expected-head-sha <Commit>`. Der Befehl prüft eigene Bedingungen, die er nicht nennt: An einem Repository ohne CI verweigerte er (`PR_PRECONDITIONS_UNMET`), in einem Projekt mit CI lief er. Verweigert er, obwohl die Merge-Bedingungen der Spezifikation erfüllt sind: `gh pr merge <Nummer> --squash --match-head-commit <Commit>` |
| Aufräumen | Der Supervisor beendet die Sitzung des gemergten Tickets mit `ao session kill <Sitzung>`; dabei läuft `preRemove`, und der Worktree verschwindet. In der Prüfung baute Orchestrator nach dem Merge nicht von selbst ab. Der Branch bleibt lokal und auf `origin` stehen und wird wie ohne Runner gelöscht. Scheitert `preRemove`, bleibt der Worktree: Ursache beheben, dann `ao session cleanup` |
| Runde auslösen | Jede zugestellte Meldung. Der Projekt-Orchestrator läuft dauerhaft |

## Was sich gegenüber Claude Code allein ändert

- **Ein Supervisor je Projekt.** Orchestrator lässt nur einen Projekt-Orchestrator zu, und alle Meldungen gehen an ihn. Läuft er nicht, bleiben sie liegen und kommen nach dem Fortsetzen an. Der Supervisor nimmt das ganze Board oder die Milestones nacheinander.
- **Der Worker bleibt bis zum Merge bestehen.** `--done` beendet die Sitzung nicht. Nach dem Beenden erreichen Befunde und Konflikte niemanden mehr.
- **Zwei Meldungen je Ticket.** Der Supervisor erfährt von einer Rückfrage und vom freigegebenen, grünen PR. Was dazwischen am PR geschieht, regeln Worker, Reviewer und Orchestrator ohne ihn. Die zweite Meldung braucht fünf Minuten.
- **Review.** Der Skill `reviewer` wird nicht gebraucht. Der Reviewer von Orchestrator schreibt ein Review mit Zusammenfassung und je Pflicht-Befund einen Kommentar an der Zeile. Er lädt die `CLAUDE.md` des Projekts und richtet sich nach ihrem Abschnitt „Review“. Einen Prüfauftrag, der nur in der Beschreibung des PR steht, beachtet er nicht.
- **Der Worker wartet auf sein Review.** Befunde werden ihm zugestellt, die Freigabe nicht, und auch der Supervisor erfährt sie nicht von selbst. Der Worker fragt deshalb `ao review ls` ab, bevor er meldet.
- **Die Freigabe des Reviewers ist intern.** Bei GitHub steht sie als Review mit dem Status `COMMENTED`, nicht als Freigabe, und sie erfüllt keinen Branch-Schutz, der Reviews verlangt. Der Integrationsbranch setzt deshalb keine GitHub-Reviews voraus.
- **Behobene Befunde schließen.** Nach dem Beheben markiert der Worker die Kommentare mit `ao pr resolve-comments <Nummer>` als erledigt.
- **Konflikte per Merge.** Die Nachricht von Orchestrator verlangt einen Rebase, und in der Prüfung hat der Worker ihn ausgeführt. Für CCMAS gilt die Spezifikation: den Integrationsbranch per Merge einarbeiten, kein Rebase, kein `--force`. Die Regeldatei sagt das ausdrücklich.
- **Dritte Runde.** Orchestrator zählt keine Runden. Der Worker zählt die Reviews am PR (`gh pr view <Nummer> --json reviews`) und legt nach Befunden in der dritten die Rückfrage an.
- **Hauptverzeichnis.** Worktrees zweigen von `origin/<defaultBranch>` ab. Der Zustand des Hauptverzeichnisses spielt keine Rolle.
- **Vorab-Check.** Vor dem ersten Start einer Runde prüft der Supervisor mit `ao doctor`, ob der Runner bereit ist. Scheitert ein Start am Setup, meldet er die Ursache und startet nichts weiter.
- **Hängende Sitzung.** Erst `ao session interrupt <Sitzung>`; `ao session kill` nur, wenn sie nach etwa drei Minuten noch arbeitet.
- **Geänderte Regeln.** Eine laufende Sitzung sieht geänderte Regeldateien nicht. Nach einer Änderung wird der Supervisor neu gestartet.

## Was Orchestrator nicht übernimmt

- **Das Ticketsystem.** Orchestrator liest nur Issues aus GitHub und GitLab und schreibt nie zurück. Das Board pflegen die Skills über `ticket`.
- **Planung und Freigaben.** Beides steht im Plugin und in der `CLAUDE.md`.
- **Den Merge.** Orchestrator meldet „bereit zum Merge“ und mergt nie von selbst. Den Befehl gibt der Supervisor.

## Zusätzlich nutzbar

Nicht Teil des Grundablaufs; je Projekt zu entscheiden. Ausprobiert ist davon nur die Automation.

- **Runde ohne Meldung.** Eine Antwort im Ticketsystem weckt den Supervisor nicht. Der Mensch schreibt ihm, oder er startet an der Sitzung ein Cue „Runde“ mit dem Auftrag `/ccmas:supervisor`. Ohne den Menschen: eine Automation, die nach Zeitplan beantwortete Rückfragen sucht und sie mit `ao report --needs-input` meldet. Jeder Lauf startet eine Worker-Sitzung mit eigenem Worktree, die danach bestehen bleibt und beendet werden muss.
- **Sichtprüfung im Abnahmetest.** `ao preview start` startet die Anwendung aus `.ao/launch.json`, `ao browser` bedient sie und macht Screenshots.
- **Protokoll als Datei.** `ao artifact add <Datei>` macht das Protokoll des Abnahmetests zu einem Ergebnis der Sitzung.
- **Zweite Meinung.** `ao review trigger <Sitzung> --agent <anderer Agent> --rerun` stellt einen zweiten Reviewer auf denselben Commit, etwa als Bedingung, wenn der Supervisor eine Freigabe selbst erteilt.
- **Verlauf eines Workers lesen.** `ao session history <Sitzung>`. Nachrichten, die Orchestrator selbst zustellt, fehlen dort.
- **Limit eines Kontos.** `ao account switch <Sitzung> <Konto>` setzt die Sitzung auf einem anderen Konto fort.

## Geprüft

Am 2026-11-22 mit Orchestrator 0.15.3 und Claude Code 2.1.323 an einem Wegwerf-Repository ohne CI ausprobiert:

| Verhalten | Ergebnis |
| --- | --- |
| Zweiter Start mit demselben Branch | Scheitert, solange eine Sitzung den Branch hält (`BRANCH_CHECKED_OUT_ELSEWHERE`). Nach `ao session kill` ist der Branch frei |
| `ao session restore` | Bringt Worktree, Branch und Verlauf zurück |
| Skill-Aufruf als erster Auftrag und als Nachricht an den Projekt-Orchestrator | Der Skill läuft, im Terminal UI und im Chat |
| Regeldateien | Beide stehen im System-Prompt ihrer Sitzung. Der Schalter für die Regeln des Orchestrators heißt `--orchestrator-rules-file`. Eine laufende Sitzung sieht Änderungen erst nach einem Neustart |
| Meldungen | `--needs-input` sofort; `--done` immer nach fünf Minuten, auch ohne andere Worker; eine Meldung ohne Art nicht binnen zehn Minuten. Meldungen an einen angehaltenen Orchestrator kommen nach dem Fortsetzen an |
| Zweiter Orchestrator im Projekt | Gibt es nicht; `ao spawn --kind orchestrator` liefert die laufende Sitzung zurück |
| Automation | Jeder Lauf startet eine Worker-Sitzung mit eigenem Worktree, ihre Meldung kommt an, und die Sitzung bleibt danach bestehen |
| Reviewer-Lauf | Folgt der `CLAUDE.md`, nicht der Beschreibung des PR. Befunde als Review und Kommentar an der Zeile, dem Worker zugestellt; die Freigabe wird niemandem zugestellt. `ao review ls` zeigt Urteil und geprüften Commit. Zweiter Trigger ohne neuen Commit: `REVIEW_HEAD_ALREADY_REVIEWED` |
| `ao pr merge` | Falscher Commit: `PR_HEAD_CHANGED`. Richtiger Commit: `PR_PRECONDITIONS_UNMET`, obwohl GitHub den PR für mergebar hielt |
| Abbau nach dem Merge | Nicht von selbst binnen sechs Minuten. `ao session kill` führt `preRemove` aus und entfernt den Worktree; die Branches bleiben. Scheitert `preRemove`, bleibt der Worktree, und `ao session cleanup` räumt ihn später auf |
| Umgebung der Setup-Schritte | Minimaler PATH ohne die Werkzeuge aus nvm. Gesetzt sind `AO_WORKTREE_PATH` und `AO_SOURCE_TREE_PATH`. `.worktreeinclude` wertet Orchestrator nicht aus |
| Konflikt am PR | Der Worker bekommt nach 46 Sekunden eine Nachricht, die einen Rebase verlangt. Nach `ao session kill` kommt nichts mehr an |

## Noch offen

Zu entscheiden:

- **Meldung des fertigen PR.** `--done` passt zur Bedeutung und braucht fünf Minuten; so steht es in der Regeldatei. `--needs-input` käme sofort an, weist den Worker aber als wartend auf Eingabe aus.

Noch nicht ausprobiert:

- Die Regeldateien in dieser Fassung in einem Lauf.
- Ob der Reviewer von Orchestrator die Akzeptanzkriterien aus der Beschreibung des PR prüft, wenn die `CLAUDE.md` ihn dazu anweist.
- Ob der Worker verlässlich auf das Ende seines Reviews wartet.
- Welche Bedingung `ao pr merge` verlangt, und ob der Befehl anders als `gh pr merge` den Abbau der Sitzung auslöst.
- Ob rote Checks den Worker als Nachricht erreichen.
- Der Schritt in `postCreate`, der `.worktreeinclude` auswertet ([Einrichtung](einrichtung.md#setup-und-abbau-je-worktree)).
- Aus der Dokumentation übernommen: `--stuck`, `ao session interrupt`, der zweite Reviewer, Vorschau und Browser, Artefakte, der Kontowechsel.

## Quellen

- [Per-role agents](https://docs.orchestrator.inc/guides/per-role-agents/)
- [Review loop](https://docs.orchestrator.inc/guides/review-loop/)
- [Handle CI and review feedback](https://docs.orchestrator.inc/guides/reactions/)
- [Automation reference](https://docs.orchestrator.inc/configuration/lifecycle-automation/)
- [Projects](https://docs.orchestrator.inc/configuration/projects/)
- [CLI reference](https://docs.orchestrator.inc/cli/)
- [Architecture](https://docs.orchestrator.inc/architecture/)
- Die Befehlsbeschreibungen des Skills `using-ao`, den Orchestrator in sein Datenverzeichnis legt, und `ao <Befehl> --help`
