// Package persistence ist der führende interne Zustand von CCMAS in SQLite (Spezifikation „Führender interner
// Zustand“): Projekte, Profile, Aufträge, Ausführungen, Entscheidungen, fachliche Fragen und der Auditverlauf. Nur der
// Go-Dienst schreibt; Python-Runner melden Ereignisse, Asana ist keine Queue.
//
// # Treiber, Pools, Dateien
//
// Der Treiber ist modernc.org/sqlite (reines Go, kein cgo). Open öffnet zwei Pools auf derselben Datei: einen
// Schreibpool mit genau einer Verbindung, dessen Transaktionen mit BEGIN IMMEDIATE beginnen, und einen getrennten
// Lesepool (query_only). Jede Verbindung trägt über den DSN busy_timeout, foreign_keys, journal_mode=WAL und
// synchronous=FULL; mehrere Anweisungen in einem _pragma sind abgeschaltet (sqlite.StrictPragmas). Läuft der
// busy_timeout ab, meldet das Paket den stabilen Code busy (kein Panic). Das Verzeichnis der Datenbank wird beim
// Anlegen 0700, die Datei vor dem ersten Öffnen 0600 (die Dateien -wal und -shm erben das); der Pfad ist absolut und
// kanonisch. Eine vorhandene Datei bleibt unverändert, ihre Prüfung ist Sache der Statusprüfung.
//
// # Migrationen
//
// Open migriert nicht; Migrate ist ein expliziter Aufruf. Die Migrationen liegen unter migrations/ in diesem Paket
// (go:embed erlaubt keine Verzeichnisse oberhalb des Pakets). Jede läuft in einer eigenen IMMEDIATE-Transaktion samt
// ihrem Eintrag in schema_migrations(version, name, sha256, applied_at); PRAGMA user_version steht auf der höchsten
// angewandten Version. Regeln: bekannte Versionen steigen streng (Lücken sind erlaubt); die angewandten bilden ein
// Präfix der bekannten (sonst migration_history_mismatch); eine geänderte Prüfsumme ist migration_checksum_mismatch;
// eine höhere unbekannte Version ist schema_too_new, und zwar ohne jeden Schreibzugriff. Es gibt kein Downgrade.
// Update und View verlangen eine migrierte Datenbank (sonst schema_outdated).
//
// # Arbeitsauftrag statt Task (Festlegung)
//
// Die reservierbare, ausführbare Einheit ist der Arbeitsauftrag (work_orders; ID = domain.WorkOrder.WorkOrderID =
// supervisor.Order.ID). Die Spezifikation nennt „tasks / dependencies“ mit fachlichem Zustand; ein Asana-Ticket ist aber
// nicht dasselbe wie das, was ein Agent ausführt: Ein Ticket kann Teil-, Korrektur- und Reparaturaufträge erzeugen, und
// Reservierung, Versuche, Reviews und Fragen beziehen sich auf den Auftrag. Deshalb liegen fachlicher Zustand
// (domain.WorkOrderState) und Abhängigkeiten (work_order_dependencies) am Auftrag. tasks ist das Asana-Abbild: interne
// Task-ID, eindeutige Asana-GID je Projekt, optional der eine Auftrag, Titel, Priorität, Section und Zeit der letzten
// Synchronisation. Die Abbildung ist damit eindeutig und ohne Doppelführung des Zustands.
//
// # Getrennte Zustandsebenen
//
// Auftragszustand (am Auftrag), Ausführungszustand (am Versuch), beobachteter Prozesszustand mit Beobachtungszeit und
// Prozessidentität (PID und Startzeit, beide oder keins; am Versuch) und Agentenverfügbarkeit (am Agenten) sind
// getrennte Spalten mit getrennten Typen und getrennten Methoden. Nichts wird abgeleitet oder gemeinsam geändert: Ein
// Prozessende mit Exit-Code 0 beendet weder Versuch noch Auftrag. Übergänge prüfen CanTransition der Ebene (sonst Fehler
// state) und den erwarteten Ist-Zustand (sonst conflict); jede Ebene schreibt ihr eigenes Audit-Ereignis.
//
// # Transaktionen, Audit und Zustandsrevision
//
// Store.Update(ctx, actor, fn) führt fn in einer Schreibtransaktion aus; mutierende Methoden gibt es nur auf *Tx. Jede
// fachliche Änderung schreibt ihr Audit-Ereignis in derselben Transaktion (Tabelle events, nur anhängbar, Details aus
// typisierten Feldern, höchstens 4 KiB, keine Prompts und keine Zugangsdaten); ein Rollback nimmt Änderung, Ereignis und
// Revision zurück. Eine Änderung ohne Wirkung (gleicher Wert) schreibt nichts. Scheitert ein Aufruf, ist die
// Transaktion verbraucht und wird nicht festgeschrieben, auch wenn der Callback den Fehler ignoriert.
//
// projects.state_revision (ab 0) steigt bei Festschreiben um genau 1, wenn die Transaktion mindestens eine Änderung des
// Planungszustands dieses Projekts enthält; mehrere Änderungen in einer Transaktion zählen einmal, eine Transaktion ohne
// Änderung erhöht nichts. Das ist die Revision, gegen die Decision, DecisionBatch und ProjectSnapshot geprüft werden.
// Planungszustand ist, was der ProjectSnapshot zeigt: Projektkonfiguration, Agenten, Ziele, Aufträge samt Konzeptstand,
// PR, Abhängigkeiten und Ergebnissen, Ausführungen und ihr Ausführungszustand, Fragen und Antworten, Review-Berichte.
// Journal- und Beobachtungseinträge ändern die Revision nicht, weil sonst jede Aufzeichnung den Plan veralten ließe, den
// sie festhält: Pläne und Entscheidungen (ein Plan gilt für genau die Revision, auf der er beruht), Profilversionen und
// Skill-Snapshots (unveränderliche Verweisziele), das Asana-Abbild (tasks), Session-ID und Prozessbeobachtung. Ihre
// Ereignisse tragen die unveränderte aktuelle Revision. Das Ereignis einer Änderung trägt die Revision nach der Änderung.
//
// Zusätzlich trägt jeder Auftrag eine Zeilenrevision (revision, ab 1) für Compare-and-Set (RequireWorkOrderRevision).
//
// # Typen und Zeit
//
// Das Paket verwendet die Typen aus internal/domain und internal/supervisor; Dokumente mit Schemavertrag werden vor dem
// Speichern gegen ihr Schema geprüft (fail-closed). Zeitstempel sind RFC 3339 in UTC mit neun Nachkommastellen; die Uhr
// ist über WithClock austauschbar. Spalten für Zugangsdaten gibt es nicht (Leitplanke: TestNoSecretColumns).
//
// # Fehler
//
// Alle Fehler sind *Error mit stabilem Code (CodeOf): busy, schema_outdated, schema_too_new,
// migration_checksum_mismatch, migration_history_mismatch, not_found, conflict, state, invalid, constraint, closed,
// internal. Bei invalid und state ist die Ursache ein *domain.ContractError.
package persistence
