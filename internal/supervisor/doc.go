// Package supervisor enthält die Plattformseite des Supervisors: den ProjectSnapshot als Eingabe der
// Planung und den Validator, der ein Planungsergebnis (domain.DecisionBatch) auf Zulässigkeit prüft.
//
// Grundsatz (Spezifikation „Intelligente Arbeitsverteilung als Herzstück“): Go enthält die technischen
// Regeln für Zulässigkeit und Ausführung, nicht die fachliche Planung. Welche Aufgabe als Nächstes sinnvoll
// ist, in welcher Reihenfolge und mit welchem Aufwand, entscheidet der Claude-Supervisor; dieses Paket
// wählt nichts aus, priorisiert nichts und ergänzt keine Aufgaben. Es lehnt ab, was die Regeln nicht
// erlauben, und sagt dem Modell genau, warum (Rejection mit stabilem Code), damit es neu planen kann.
//
// Der Validator ist die Schnittstelle zum Orchestrator (M03): Er verändert nichts und startet nichts. Ein
// Batch ist atomar: Wird auch nur eine Entscheidung abgelehnt, wird keine ausgeführt. Einzelheiten, Codes
// und was Go entscheidet und was nicht: docs/architecture/supervisor.md.
package supervisor
