# CCMAS: Regeln für Worker-Sitzungen

Dieses Projekt arbeitet mit CCMAS. Dein Auftrag ruft einen Skill des Plugins `ccmas` auf; arbeite nach ihm. Diese Regeln sagen nur, wie die Verben des Skills mit Orchestrator umgesetzt werden. Wo sie einen Weg nennen, gilt er statt der Spalte „Ohne Runner“ im Skill.

## Worktree und Branch

- Orchestrator hat dieser Sitzung einen Worktree mit eigenem Branch gegeben. Arbeite nur dort. Lege keinen weiteren an und wechsle den Branch nicht.

## Review holen

- Nach dem Erstellen des PR: `ao review trigger --pr <URL>`. Nach jedem weiteren Push: `ao review trigger`.
- Befunde kommen als Nachricht. Die Freigabe kommt nicht als Nachricht: Warte, bis `ao review ls --json` für deinen aktuellen Commit (`targetSha`) einen abgeschlossenen Lauf zeigt. `verdict: approved` ist FREIGEGEBEN, `changes_requested` ist BEFUNDE.
- Nach dem Beheben von Befunden: pushen, `ao pr resolve-comments <Nummer>`, dann das Review erneut holen.
- Die Zahl der Runden ist die Zahl der Reviews am PR (`gh pr view <Nummer> --json reviews`).

## Ereignisse

- Vor der Meldung prüfst du selbst, ob die Checks grün sind und kein Konflikt besteht (`gh pr checks <Nummer>`, `gh pr view <Nummer> --json mergeable`).
- Was danach rot wird oder in Konflikt gerät, kommt wie Review-Kommentare als Nachricht. Behebe genau das.
- Bei einem Konflikt arbeitest du den Integrationsbranch per Merge ein, auch wenn die Nachricht einen Rebase nennt. Kein Rebase, kein `--force`. Danach holst du das Review erneut.

## Melden

Immer erst das Ticket pflegen, dann melden, und nur in diesen Fällen:

| Fall | Befehl |
| --- | --- |
| Rückfrage | `ao report --needs-input --note "<Ticket>: <Frage in einem Satz>"` |
| Freigegeben und grün | `ao report --done --pr-created <URL> --note "<Ticket>: freigegeben, Commit <Hash>"` |
| Nichts geht mehr | `ao report --stuck --note "<Ticket>: <Ursache>"` |

## Grenzen

- Deine Sitzung bleibt bis zum Merge bestehen. Beende sie nicht; nur so erreichen dich Befunde und Konflikte.
- Du mergst nie.
