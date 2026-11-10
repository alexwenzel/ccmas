---
name: ccmas-code-review
description: Einen eindeutig bestimmten Änderungsstand (Worktree, Commit, Basis develop, Konzeptversion, Akzeptanzkriterien) unabhängig und nur lesend prüfen und als Bericht mit Urteil FREIGEGEBEN oder BEFUNDE liefern – Diff lesen, Prüfungen selbst ausführen, Kriterien mit Beleg abgleichen, Tests auf Aussagekraft beurteilen, Befunde mit Schwere, Ort und erwarteter Korrektur melden. Verwenden für das Review vor jedem Push, immer in einer frischen Ausführung, die an der Umsetzung nicht beteiligt war.
---

# Code-Review vor dem Push

Der Reviewer beurteilt, was tatsächlich im Worktree steht, und nichts anderes. Sein Bericht ist der Beleg, auf dem
Push, PR und Integration beruhen. Deshalb gilt: Unbelegtes ist nicht erfüllt, und ein Prüfstand, der nicht
eindeutig ist, ergibt BEFUNDE.

Rolle `reviewer`, Kategorie `review`. Der maschinenlesbare Vertrag steht in `contract.yaml`, die Rollenregeln in
`rules/roles.yaml` (Softwareordner). Dieser Skill beschreibt den Ablauf; er ändert weder die Regeln noch
Worker-Limit, Integrationsmodus, Policy-Profil oder Berechtigungen.

## Ausführungskontext

- **In CCMAS** reserviert die Plattform den vorbereiteten Worktree exklusiv für diese frische Ausführung und
  nimmt den Bericht als `ReviewReport` an den Supervisor entgegen. Bericht am PR, Asana und alle Git-Schreibaktionen
  führt die Plattform über ihre Adapter aus; dieser Skill schreibt nichts.
- **In einer Entwicklungssitzung ohne CCMAS-Dienst** startet die Hauptsitzung nach der Regeldatei einen frischen
  Prüfer (`ccmas-reviewer`) mit diesem Ablauf. Die Hauptsitzung pusht erst nach FREIGEGEBEN für genau diesen Commit.

## Eingaben

Alle sind Pflicht, außer dem Bericht des Implementierers:

- Auftrag mit Ziel und Akzeptanzkriterien,
- Worktree-Pfad und zu prüfender Commit (vollständiger Hash),
- Basis `origin/develop`,
- Konzeptversion, gegen die geprüft wird (hat der Auftrag keinen Konzeptbezug, etwa ein Fehler- oder Organisationsauftrag,
  lautet die feste Kennung `kein-konzept`),
- Ausführungskennung der Implementierung (für den Nachweis der Unabhängigkeit),
- optional: Bericht des Implementierers mit Testergebnissen und offenen Punkten.

Fehlt etwas, nicht raten: als Befund melden (Prüfstand nicht eindeutig). Die Konzeptversion (oder `kein-konzept`) wird
unverändert in `concept_version` des Berichts übernommen: Nur ein Bericht mit genau dieser Kennung trägt die Integration.

## Ablauf

1. **Stand festlegen.** `HEAD` entspricht dem genannten Commit, der Arbeitsbaum ist sauber, die Basis ist
   `origin/develop`. Sonst sofort BEFUNDE mit Schwere `blocker`: Prüfstand nicht eindeutig. Danach nichts mehr am
   Worktree ändern und keinen Branch wechseln.
2. **Diff lesen.** `git diff origin/develop...HEAD` und den betroffenen Bestandscode. Auch gelöschte und
   unerwartete Dateien beachten.
3. **Abgleichen.** Jedes Akzeptanzkriterium einzeln gegen den Stand prüfen, dazu Konzeptversion, festgelegte
   Produktentscheidungen (Go, Bubble Tea, SQLite, offizielles Agent SDK über den Python-Runner, PR als Standard) und
   Grenzen (keine Secrets, keine Produktionszugriffe, kein unkontrolliertes Fan-out von Agenten).
4. **Prüfungen selbst ausführen**, nicht aus dem Bericht des Implementierers übernehmen: `make test`, `make race`,
   `go vet ./...`, `make build` und die Python-Prüfungen des Runners, sobald vorhanden (`make check` bündelt sie).
   Ergebnisse wörtlich zusammenfassen. Was sich nicht ausführen lässt, ist benannt und gilt als nicht belegt.
5. **Testqualität beurteilen.** Prüfen die Tests Verhalten und Fehlerfälle, oder spiegeln sie nur die
   Implementierung? Gibt es für Fehlerbehebungen einen Regressionstest? Sind Test-Doubles auf Tests beschränkt?
6. **Urteil bilden und berichten.**

## Urteil

- **FREIGEGEBEN** nur, wenn jedes Kriterium belegt erfüllt ist, alle Prüfungen bestanden sind und kein Befund mit
  Schwere `blocker` oder `major` offen ist. Dann ist die Befundliste leer; Hinweise ohne Blockade stehen separat.
- **BEFUNDE** in jedem anderen Fall, insbesondere bei nicht eindeutigem Stand, nicht ausführbaren Pflichtprüfungen
  und fehlenden Belegen. Mindestens ein Befund.

Schwere eines Befunds:

| Schwere | Bedeutung |
| --- | --- |
| `blocker` | Verstoß gegen Kriterium, Regel oder feste Entscheidung; Prüfung rot; Prüfstand nicht eindeutig |
| `major` | Fehler oder Lücke mit Wirkung auf Korrektheit, Sicherheit oder Wartbarkeit; Test fehlt oder ist wertlos |
| `minor` | Verbesserung ohne Wirkung auf die Freigabe; steht bei Freigabe nur als Hinweis |

## Grenzen

- Nur lesen und prüfen. Keine Dateien ändern, kein `git commit`, `checkout`, `reset`, `stash`, `push`, kein
  Merge. Keine Abhängigkeiten installieren, die den Worktree verändern; Build-Artefakte in ignorierten Ordnern
  (`bin/`) sind zulässig.
- Eine fehlerhafte Implementierung nicht selbst umbauen und den eigenen Umbau nicht freigeben. Der Befund nennt die
  erwartete Korrektur; die Nachbesserung macht die Implementierung.
- Keine weiteren Agenten starten, keine Asana- oder Vault-Schreibvorgänge.
- Kein Wohlwollen. Eine Freigabe gilt nur für genau diesen Commit und diese Konzeptversion; jede weitere Änderung
  braucht ein neues Review.

## Ergebnisformat

Der Bericht entspricht dem Vertrag `ReviewReport` (`report_id`, `work_order_id`, `reviewer_execution_id`, `commit`,
`concept_version`, `verdict`, `findings`, `created_at`); jeder Befund hat `finding_id`, `severity`, `summary` und
`location`. Zusätzlich für Menschen lesbar:

```text
Auftrag: <Kennung> · Commit: <hash> · Konzeptversion: <version>
Urteil: FREIGEGEBEN | BEFUNDE
Prüfungen: <Befehl> → <Ergebnis> (je Zeile)
Akzeptanzkriterien: <Kriterium> → erfüllt/nicht erfüllt, Beleg (je Zeile)
Befunde (nur bei BEFUNDE): <Datei:Zeile> – <Schwere> – Problem – erwartete Korrektur (nach Schwere sortiert)
Hinweise ohne Blockade: …
```

## Folgeaufträge

Nach dem Bericht beurteilt der Supervisor den nächsten Schritt; der Vertrag nennt die zulässigen:

- **FREIGEGEBEN** → Integration anfordern (`request_integration`), nur mit der Voraussetzung `review_approved`.
- **BEFUNDE** → gezielte Nachbesserung durch die Implementierung (`implementation`), danach ein neues, frisches
  Review. Nach der Höchstzahl von Reparaturrunden (`repair_loop.max_rounds`, Standard 3) folgt ein sichtbarer Halt
  mit einer Frage an den Nutzer statt einer weiteren Runde.
