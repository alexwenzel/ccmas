// Package profiles löst Modell, Effort und Skills einer Agentenausführung auf. Es hält die
// beiden Konfigurationsdateien (models.yaml: Fähigkeiten der Modelle; agents.yaml:
// Rollenprofile und individuelle Agenten), prüft sie streng und liefert aus ihnen und einem
// Skill-Katalog ein unveränderliches domain.AgentProfile.
//
// Rangfolge je Feld (Modell, Effort, Skills einzeln): ausdrücklicher Auftragswert >
// individueller Agentenwert > Wahl des Supervisors > Rollenstandard. Alles gilt nur innerhalb
// der Rollenerlaubnis. Modell-IDs stehen ausschließlich in der Konfiguration (config/*.example.yaml
// beziehungsweise models.yaml), nie im Code.
//
// Es gibt keinen unsichtbaren Fallback: ein vom Modell nicht unterstützter Effort wird mit
// unsupported_effort abgelehnt, außer models.yaml genehmigt die Reduktion für genau dieses
// Modell und genau diese angeforderte Stufe ausdrücklich. Die Reduktion steht dann im Profil
// (requested_effort, effort, fallback). Der wirksame Effort ist abgeleitet (aus Konfiguration
// und dokumentierter Regel), nicht gemessen: das SDK meldet ihn nicht.
//
// Das Paket schreibt keine Datenbank. Die Persistenz in SQLite folgt in M03; bis dahin sind die
// Dateien (Store) der Speicher. Einzelheiten: docs/architecture/profiles.md.
package profiles
