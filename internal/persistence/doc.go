// Package persistence ist der führende interne Zustand von CCMAS in SQLite (Spezifikation „Führender interner
// Zustand“): Projekte, Profile, Aufträge, Ausführungen, Entscheidungen, fachliche Fragen und der Auditverlauf. Nur der
// Go-Dienst schreibt; Python-Runner melden Ereignisse, Asana ist keine Queue.
//
// # Treiber, Pools, Dateien
//
// Der Treiber ist modernc.org/sqlite (reines Go, kein cgo). Open öffnet zwei Pools auf derselben Datei: einen
// Schreibpool mit genau einer Verbindung, dessen Transaktionen mit BEGIN IMMEDIATE beginnen, und einen getrennten
// Lesepool (query_only). Jede Verbindung trägt über den DSN busy_timeout, foreign_keys und synchronous=FULL;
// mehrere Anweisungen in einem _pragma sind abgeschaltet (sqlite.StrictPragmas). Läuft der busy_timeout ab, meldet das
// Paket den stabilen Code busy (kein Panic). Das Verzeichnis der Datenbank wird beim Anlegen 0700, die Datei vor dem
// ersten Öffnen 0600 (die Dateien -wal und -shm erben das); der Pfad ist absolut und kanonisch.
//
// journal_mode=WAL steht bewusst nicht im DSN, weil es schon beim Öffnen in die Datei schriebe, auch bei einer
// fremden oder zu neuen Datenbank. WAL ist ein Merkmal der Datei und gilt dann für alle Verbindungen beider Pools. Eine
// von Open neu angelegte Datei stellt Open sofort auf WAL. Eine vorhandene Datei bleibt durch Open unverändert (kein
// Schreibzugriff, auch nicht auf den journal_mode); erst Migrate stellt sie auf WAL um, und zwar nach bestandener
// Plan- und Historienprüfung und vor dem ersten Schreiben. Ist eine vorhandene Datei nicht WAL und wurde nicht
// migriert, verweigern Update und View mit schema_outdated (bei einer von dieser Software migrierten Datei
// unerreichbar). Ihre Prüfung im Übrigen ist Sache der Statusprüfung.
//
// # Migrationen
//
// Open migriert nicht; Migrate ist ein expliziter Aufruf. Die Migrationen liegen unter migrations/ in diesem Paket
// (go:embed erlaubt keine Verzeichnisse oberhalb des Pakets). Jede läuft in einer eigenen IMMEDIATE-Transaktion samt
// ihrem Eintrag in schema_migrations(version, name, sha256, applied_at); PRAGMA user_version steht auf der höchsten
// angewandten Version. Regeln: bekannte Versionen steigen streng (Lücken sind erlaubt); die angewandten bilden ein
// Präfix der bekannten (sonst migration_history_mismatch); eine geänderte Prüfsumme ist migration_checksum_mismatch;
// eine höhere unbekannte Version ist schema_too_new, und zwar ohne jeden Schreibzugriff. Eine fremde Datenbank
// (Objekte in sqlite_master, aber keine Tabelle schema_migrations) verweigert Migrate mit foreign_database, ebenfalls
// ohne jeden Schreibzugriff; die Prüfung auf zu neu geht vor. Es gibt kein Downgrade. Update und View verlangen eine
// migrierte Datenbank im WAL-Modus (sonst schema_outdated).
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
// # Nachrichten und Zustellung (CC-45)
//
// Nachrichten (messages) und ihre Zustellungen (message_deliveries, je Empfänger eine Zeile mit eigenem Zustand) sind
// Journal und kein Planungszustand: Sie stehen nicht im ProjectSnapshot und erhöhen state_revision nicht; Fragen und
// Antworten bleiben in ihren eigenen Tabellen und sind Planungszustand. Jede Änderung schreibt trotzdem ihr Ereignis
// (Ebene message, nie mit den Nutzdaten) in derselben Transaktion. Tx.CreateMessage legt Nachricht und Zustellungen an
// (gleiche ID mit gleichem Inhalt ist ein No-op ohne Ereignis, mit anderem Inhalt conflict) und prüft in Go, dass die
// Ausführung eines Versuchs zum Auftrag der Nachricht gehört; die Datenbank bindet nur das Projekt. Tx.TransitionDelivery
// führt die Zustellung nach der Übergangstabelle von domain.DeliveryState (erwarteter Ist-Zustand sonst conflict, erlaubter
// Übergang sonst state; die Wiederholung delivered -> delivered zählt einen weiteren Versuch). Gelesen werden Nachricht,
// Zustellungen und die noch nicht bestätigten Zustellungen eines Empfängers in fester Reihenfolge (PendingDeliveries).
// Migration 003 sichert dies in der Datenbank: Trigger auf messages.type (domain.MessageKinds), auf die Übergänge der
// Zustellung und auf die Form von outbox.kind (<system>.<aktion>, domain.CheckOutboxKind); Nachrichten sind unveränderlich.
//
// # Outbox für externe Aktionen (CC-45)
//
// Die Outbox (outbox) hält die Absicht, in einem externen System (Asana, GitHub) zu schreiben, bevor sie ausgeführt wird. Sie
// ist Journal und kein Planungszustand: Eine Aktion erscheint nicht im ProjectSnapshot und erhöht state_revision nicht. Der
// Grund ist derselbe wie bei den Nachrichten: Der Lebenslauf einer Aktion (Beanspruchen, Wiederholen, Abgleichen) ist Betrieb
// des Dienstes und ändert nichts an dem, worauf Pläne und Entscheidungen beruhen; erhöhte jeder Versuch die Revision, ließe
// er jeden Plan veralten. Die auslösende fachliche Änderung ist dagegen Planungszustand und erhöht die Revision wie
// bisher: Tx.EnqueueOutbox läuft in derselben Transaktion, ein Rollback nimmt Absicht und Änderung gemeinsam zurück.
// Jede Änderung der Outbox schreibt ihr Ereignis (Ebene outbox, Arten domain.OutboxAuditKinds) in derselben Transaktion; es
// trägt Zustände, Zähler und die Fehlerkennung (Code), nie Nutzdaten oder Fehlermeldungen. Der Lebenslauf folgt
// domain.OutboxState (erwarteter Ist-Zustand sonst conflict, unerlaubter Übergang sonst state): ClaimOutbox, CompleteOutbox,
// FailOutbox, MarkOutboxUncertain, ExpireOutboxClaims, RetryOutboxReconcile, RequeueOutbox, EscalateOutbox, CancelOutbox und
// ResolveOutbox (ausdrückliche Klärung mit Akteur und Grund; der Freitext wird maskiert). CompleteOutbox, FailOutbox,
// MarkOutboxUncertain und EscalateOutbox verlangen den Versuchszähler des Claims (Fencing): Das späte Ergebnis eines
// abgelaufenen Claims ändert einen neueren Claim nicht (conflict). Ein abgelaufener Claim gilt als ungewiss und geht in den
// Abgleich, nie zurück nach pending. Gespeichert wird nur die begrenzte Fehlerkennung (domain.OutboxError, höchstens 322
// Bytes, ohne Steuerzeichen); Adapter geben keine Zugangsdaten in Meldungen. Migration 003 sichert Startwerte, Schlüsselform
// (<projekt>/<art>/…), Übergangstabelle, Zähler und die Form je Zustand durch Trigger und ergänzt die Spalte
// reconcile_attempts. Ablauf, Schlüssel und Marker: docs/architecture/messaging.md.
//
// # Typen und Zeit
//
// Das Paket verwendet die Typen aus internal/domain und internal/supervisor; Dokumente mit Schemavertrag werden vor dem
// Speichern gegen ihr Schema geprüft (fail-closed). Zeitstempel sind RFC 3339 in UTC mit neun Nachkommastellen; die Uhr
// ist über WithClock austauschbar. Spalten für Zugangsdaten gibt es nicht (Leitplanke: TestNoSecretColumns).
//
// # Nur lesende Prüfung
//
// Inspect(ctx, path) prüft eine bestehende Datenbank, ohne etwas zu schreiben (Grundlage von „ccmas status“): Sie legt
// weder Datei noch Verzeichnis an (fehlt die Datei, steht das im Bericht, kein Fehler), setzt kein journal_mode, ändert
// keine Rechte, migriert nicht und lässt nach der Rückkehr keine neue Datei liegen. Der Bericht (Report, stabile
// JSON-Namen) nennt Datei, Größe, Rechte von Verzeichnis, Datei, -wal und -shm, journal_mode, Schemaversion
// (user_version und schema_migrations, bekannte und ausstehende Migrationen), das Ergebnis von quick_check und
// foreign_key_check, Grunddaten (Projekte mit Zustandsrevision, Aufträge je Zustand, aktive Ausführungen, offene Fragen,
// Outbox je Zustand, Zeit des letzten Ereignisses) und Hinweise. Befunde (Finding) sind schema_too_new,
// migration_checksum_mismatch, migration_history_mismatch, foreign_database (Tabellen ohne schema_migrations),
// quick_check_failed, foreign_key_violations, insecure_permissions (Gruppen- oder Weltrechte), unreadable (kaputte oder
// fremde Datei, Fehler beim Lesen) und busy. Ausstehende Migrationen und ein iCloud-Pfad („Mobile Documents“) sind
// Hinweise, keine Befunde. Alle Abfragen laufen in einer einzigen Lesetransaktion: der Bericht zeigt einen
// festgeschriebenen Stand, auch wenn ein Schreiber offen ist; WAL-Leser blockieren Schreiber nicht.
//
// Verhalten von mode=ro bei einer WAL-Datenbank (gemessen mit modernc.org/sqlite v1.61.0, SQLite 3.54.1, macOS):
//   - Ohne vorhandene -wal und -shm legt mode=ro beide Dateien an (-wal leer, -shm 32 KiB, Rechte der Datenbank) und
//     lässt sie nach dem Schließen liegen. Die Hauptdatei bleibt byte-identisch, aber „keine neue Datei“ gilt nicht.
//     In einem nicht beschreibbaren Verzeichnis scheitert das Öffnen mit „attempt to write a readonly database“.
//   - Mit vorhandenem -wal und -shm (typisch: ein Schreiber ist offen) liest mode=ro den festgeschriebenen Stand samt
//     noch nicht zurückgeschriebener WAL-Daten und legt nichts an, auch bei nicht beschreibbarem Verzeichnis oder
//     schreibgeschützter Datei.
//   - Mit -wal, aber ohne -shm (Reste ohne offenen Schreiber) legt mode=ro ein -shm an und lässt es liegen; im nicht
//     beschreibbaren Verzeichnis scheitert es mit „unable to open database file“.
//   - immutable=1 nimmt keine Sperren und öffnet weder -wal noch -shm: es entsteht nichts, auch im nicht beschreibbaren
//     Verzeichnis. Es ignoriert aber ein vorhandenes WAL (Daten nur im WAL fehlen, journal_mode erscheint als „delete“).
//     Der journal_mode wird deshalb aus dem Dateikopf gelesen.
//
// Daraus folgt die Wahl des Lesewegs (Report.ReadMode): ohne -wal und ohne -journal immutable=1 (keine Schreiber aktiv);
// mit -wal und -shm mode=ro auf dem Original; mit -wal ohne -shm mode=ro auf einer privaten Kopie von Datei und -wal in
// einem 0700-Temp-Verzeichnis, das danach gelöscht wird (bis 256 MiB); mit einer -journal-Datei mode=ro. Alle Wege
// öffnen mit query_only(1) und kurzem busy_timeout. Grenzen: (1) immutable liest ohne Sperre. Wird währenddessen ein
// Schreiber aktiv, erkennt Inspect das am Erscheinen von -wal/-journal oder an einer Änderung von Größe oder Zeit der
// Hauptdatei, verwirft den Versuch und wiederholt ihn (höchstens dreimal, sonst Befund busy); ein Schreiber, der
// innerhalb des Lesefensters ohne jede Änderung der Hauptdatei beginnt und endet, ist nicht auszuschließen, ändert aber
// auch nichts am Gelesenen. (2) Auf dem mode=ro-Weg kann sich der Inhalt von -shm (Wal-Index, Lesemarken) ändern; Hauptdatei
// und -wal bleiben byte-identisch (Test TestInspectWhileWriterOpen). Endet der letzte Schreiber genau zwischen der Prüfung
// der Nachbardateien und dem Öffnen, kann mode=ro leere -wal/-shm anlegen; sie sind gewöhnliche, leere WAL-Dateien. (3)
// Gemessen ist nur macOS; Linux verhält sich nach der SQLite-Dokumentation gleich, ist hier aber nicht geprüft.
// Rechteprüfungen entfallen unter Windows.
//
// # Fehler
//
// Alle Fehler sind *Error mit stabilem Code (CodeOf): busy, schema_outdated, schema_too_new,
// migration_checksum_mismatch, migration_history_mismatch, foreign_database, not_found, conflict, state, invalid, constraint, closed,
// internal. Bei invalid und state ist die Ursache ein *domain.ContractError.
package persistence
