// Package orchestrator ist die Schicht des Go-Dienstes, die Planung und Zustand verbindet: Sie baut den ProjectSnapshot aus
// SQLite, reserviert Arbeit und führt Zustandsübergänge sicher aus. Sie entscheidet nichts inhaltlich: Was zulässig ist, sagt der
// Validator (internal/supervisor); was gespeichert wird und wie, regelt internal/persistence.
//
// Tasks (NewTasks) bündelt die Operationen: ReserveBatch und ReserveTask reservieren einen angenommenen Batch in einer
// Transaktion, mit erneutem Validator-Aufruf; ApplyEvent und ApplyProcessExit wenden Ereignisse der Runner an, abgesichert durch
// Lease-Generation (Fencing); CancelTask und ResumeTask brechen ab und nehmen blockierte Aufträge wieder auf. Die Auftragsübergänge
// je Rolle stehen als Tabelle in transitions.go. Regeln, Fehlerklassen und offene Punkte: docs/architecture/orchestrator.md.
//
// # Snapshot aus SQLite
//
// BuildSnapshot füllt supervisor.ProjectSnapshot für ein Projekt und ein Ziel aus einer Leseschnittstelle (SnapshotReader),
// die *persistence.ReadTx und *persistence.Tx beide erfüllen: Wer lesend plant, baut den Snapshot in Store.View, wer
// reserviert, in der Schreibtransaktion selbst. Alles kommt aus einer Transaktion, also aus einer einzigen Zustandsrevision.
// StoreSnapshotSource erfüllt supervisor.SnapshotSource über Store.View für ein festes Projekt und Ziel; ein anderes Projekt
// ist ein Fehler, keine stille Umleitung.
//
// Inhalt (docs/architecture/persistence.md, „Abbildung des ProjectSnapshot“):
//   - Aufträge: alle Aufträge des Ziels und, damit depends_on bekannt ist, die Aufträge außerhalb des Ziels, von denen sie
//     (auch über mehrere Stufen) abhängen. Unbeteiligte Aufträge anderer Ziele fehlen.
//   - Kapazität: max_workers aus der Projektkonfiguration; active sind alle aktiven Ausführungen des Projekts (auch die
//     anderer Ziele; sie belegen Plätze). active_execution_id eines Auftrags ist seine aktive Ausführung, bei mehreren die mit
//     der kleinsten ID: „aktiv“ bleibt gesetzt, der Validator startet dann nichts Zweites auf demselben Auftrag.
//   - Offene Fragen: die noch nicht zugestellten (offen oder beantwortet), soweit ihr Auftrag im Snapshot steht.
//   - Review, PR, Kopf-Commit, letztes Rollenergebnis, Reparaturrunden und Abschlussnachweise unverändert aus der Datenbank.
//   - Rollen und Skills sind nicht aus SQLite: Sie kommen als SnapshotOffers vom Aufrufer (supervisor.BuildRoleOffers und
//     BuildSkillOffers aus Konfiguration und Skill-Katalog).
//
// Das Ergebnis ist immer kanonisch (Canonical), und ein Snapshot einer konsistenten Datenbank besteht Check.
//
// # Zustandsrevision 0
//
// Revision 0 ist ein gewöhnlicher Wert, kein Sonderfall „unbekannt“. Ein Projekt, das in derselben Transaktion angelegt und
// weiter geändert wird, bleibt auf Revision 0 (die anlegende Transaktion zählt als eine Änderung mit dem Ergebnis 0). Ein
// Snapshot auf 0 ist gültig und gilt, bis die nächste Planungsänderung die Revision auf 1 setzt; ab dann ist er veraltet.
//
// # Konzeptstand abhängiger Aufträge
//
// Ein Umsetzungs- oder Fehlerauftrag, der von einem Konzeptauftrag abhängt, führt in der Datenbank keinen eigenen Konzeptstand:
// Der Konzeptstand gehört dem Konzeptauftrag. Damit der Validator die Freigabe prüfen kann (Spezifikation „Concept-Tickets in
// Asana“: abhängige Aufträge starten erst nach erfüllter Konzeptfreigabe), überträgt der Bau des Snapshots den freigegebenen
// Stand (Version, Einstufung, Kriterien, Freigabe) auf den abhängigen Auftrag. Die Datenbank bleibt dabei unverändert; die
// Übertragung ist abgeleitet und wird bei jedem Bau neu entschieden. Regeln, jeweils fail-safe:
//   - Ein eigener Konzeptstand des Auftrags hat Vorrang und wird nie überschrieben.
//   - Übertragen wird nur von Aufträgen der Art concept, und nur auf Aufträge der Art implementation oder bug (nur sie werden
//     umgesetzt und geprüft; eine Konzeptarbeit führt ihren eigenen Stand, Organisationsaufträge haben keinen Konzeptbezug).
//   - Freigegeben heißt: eine Freigabe liegt vor und gilt für genau die Version des Konzeptstands. Wer wen freigeben durfte
//     (Einstufung, Freigeber), prüft der Validator weiter selbst (ConceptApprovalValid), nicht dieser Bau.
//   - Hängt der Auftrag von genau einem Konzeptauftrag ab, wird dessen freigegebener Stand übertragen. Hängt er von mehreren ab,
//     nur dann, wenn alle freigegeben sind und denselben Stand tragen; bei unterschiedlichen Ständen oder wenn auch nur einer
//     nicht freigegeben ist, wird nichts übertragen. Der Auftrag bleibt dann ohne Konzeptstand mit Konzeptbezug, und der
//     Validator lehnt die Umsetzung ab. Raten, welcher Stand gilt, wäre eine stille Freigabe.
//
// # Bezeichner
//
// Dieses Paket teilen mehrere Tickets. Die Bezeichner der Snapshot-Seite beginnen mit Snapshot oder BuildSnapshot, die der
// Reservierung und der Ereignisse mit Tasks (Tasks, TasksPolicy, TasksGoalFunc, TaskError, TaskClass, ReserveTask, ReserveBatch,
// ApplyEvent, ApplyProcessExit, CancelTask, ResumeTask), die Tabelle der Auftragsübergänge mit order, interne Hilfen mit task.
// Allgemeine Namen wie Service, New, Option, Config oder Clock sind absichtlich nicht belegt.
package orchestrator
