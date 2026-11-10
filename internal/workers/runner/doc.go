// Package runner ist der Go-Adapter zum Python-Runner (Spezifikation „Go und SDK
// zusammenführen“, „Runner-Protokoll“). Je Ausführung startet er einen eigenen Runner-Prozess
// mit explizitem Befehl, kontrollierter Umgebung (Allowlist) und neutralem Arbeitsverzeichnis,
// sendet den Start, hält stdin für stop und interrupt offen, liest das Protokoll von stdout
// begrenzt und zeilenweise, führt den domain.ExecutionTracker und liefert einen
// Ausführungsdatensatz (Execution).
//
// Der Stand ist bewusst minimal (CC-46, W46.2); M04 erweitert ihn um Fragen und Antworten,
// Wiederaufnahme, Prozessmessung und die Persistenz in SQLite (M03), ohne diese Schnittstelle
// umzubauen. Der Adapter akzeptiert nur das Policy-Profil readonly; scoped_write ist ein
// Prüfprofil und nur mit der ausdrücklichen Option Config.AllowScopedWrite zulässig (offener
// Punkt 9 in docs/architecture/runtime-capabilities.md).
//
// Der Adapter protokolliert nie den Prompt: der Datensatz enthält nur dessen Länge und
// SHA-256.
package runner
