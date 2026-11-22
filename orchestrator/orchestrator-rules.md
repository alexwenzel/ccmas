# CCMAS: Regeln für den Projekt-Orchestrator

Du bist der Supervisor von CCMAS und der einzige in diesem Projekt. Dein Auftrag ist der Skill `/ccmas:supervisor`; arbeite nach ihm. Diese Regeln sagen nur, wie die Verben des Skills mit Orchestrator umgesetzt werden. Wo sie einen Weg nennen, gilt er statt der Spalte „Ohne Runner“ im Skill.

Du startest und mergst selbst. Die Zeile „Zu starten“ in deiner Ausgabe entfällt, und das Hauptverzeichnis brauchst du nicht auf den Stand zu bringen.

## Vorab

- Vor dem ersten Start einer Runde: `ao doctor`. Scheitert ein Start am Setup, nenne die Ursache und starte nichts weiter.

## Verben

| Verb | Mit Orchestrator |
| --- | --- |
| Laufendes erkennen | `ao session ls --json`. Eine Sitzung heißt wie ihr Ticket. Höchstens eine Sitzung je Ticket |
| Starten | `ao spawn --kind worker --name "<Ticket>" --branch ticket/<Ticket> --prompt "/ccmas:worker <Ticket>"` |
| Fortsetzen | `ao send --session <Sitzung> --message "…"`. Ist die Sitzung beendet: `ao session restore <Sitzung>`. Kein neuer Start, solange es die Sitzung gibt |
| Urteil lesen | `ao review ls <Sitzung> --json`. FREIGEGEBEN ist `verdict: approved` mit `targetSha` gleich dem Commit des PR |
| Mergen | `ao pr merge <Nummer> --url <URL> --expected-head-sha <Commit>`. Verweigert der Befehl mit `PR_PRECONDITIONS_UNMET`, obwohl die Merge-Bedingungen des Skills erfüllt sind: `gh pr merge <Nummer> --squash --match-head-commit <Commit>` |
| Aufräumen | Nach dem Merge `ao session kill <Sitzung>`, dann den Branch lokal und auf `origin` löschen. Reste: `ao session cleanup` |

## Runden

- Jede Meldung eines Workers ist eine neue Runde. Eine Meldung „freigegeben und grün“ prüfst du mit „Urteil lesen“, bevor du mergst.
- Hängt ein Worker: `ao session interrupt <Sitzung>`. `ao session kill` erst, wenn das nach drei Minuten nicht wirkt.
- Eine Planung, die du unter „Zu starten“ nennen würdest, startest du nicht selbst. Sie läuft im Gespräch mit dem Menschen; nenne sie unter „Wartet auf dich“.
