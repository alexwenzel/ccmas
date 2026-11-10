package domain

// OutboxState ist der Zustand einer externen Aktion in der Outbox (zum Beispiel ein Schreibzugriff auf Asana oder
// GitHub).
//
// Übergangstabelle (CC-45):
//
//	pending         -> claimed | cancelled
//	claimed         -> succeeded | retry_wait | reconciling | needs_attention
//	retry_wait      -> claimed | cancelled | needs_attention
//	reconciling     -> reconciling | succeeded | pending | needs_attention
//	needs_attention -> pending | succeeded | cancelled      (nur als ausdrückliche, auditierte Klärung)
//	succeeded, cancelled: Endzustände
//
// Begründung:
//
//   - Die Absicht steht vor der Ausführung in der Outbox (pending). Beanspruchen (claimed) ist der Beginn des Aufrufs und
//     zählt als Versuch. Ob ein Aufruf gelang, entscheidet der Ausgang: Erfolg -> succeeded; ein sicherer, wiederholbarer
//     Fehler -> retry_wait (mit Zeitpunkt, begrenzt, Backoff); ein dauerhafter Fehler oder die Höchstzahl der Versuche ->
//     needs_attention.
//   - Ein ungewisser Ausgang (Timeout, abgebrochene Verbindung, Absturz während des Aufrufs, abgelaufener Claim) beweist
//     weder Erfolg noch Misserfolg. Er führt nach reconciling, nie zurück nach pending oder retry_wait: Erst der Abgleich
//     prüft das externe System (GIDs, PRs, Marker). claimed -> pending und claimed -> claimed fehlen deshalb mit Absicht.
//   - reconciling -> reconciling ist ein weiterer, nicht schlüssiger Abgleich (Zähler +1). reconciling -> pending gilt nur,
//     wenn der Abgleich sicher belegt, dass das externe Objekt nicht existiert; reconciling -> succeeded, wenn er es findet.
//     Nach einer begrenzten Zahl nicht schlüssiger Abgleiche folgt needs_attention.
//   - needs_attention ist sichtbarer Klärungsbedarf. Hinaus führt nur eine ausdrückliche Klärung mit Akteur und Grund:
//     erneut versuchen (pending, Zähler zurückgesetzt), als erledigt bestätigen (succeeded mit externer Referenz) oder
//     abbrechen (cancelled). Kein automatischer Weg verlässt diesen Zustand.
//   - Aus succeeded und cancelled gibt es keinen Weg zurück; eine erledigte Aktion wird nie erneut ausgeführt.
//
// Die Datenbank erzwingt dieselbe Tabelle durch Trigger (Migration 003).
type OutboxState string

const (
	OutboxPending        OutboxState = "pending"
	OutboxClaimed        OutboxState = "claimed"
	OutboxRetryWait      OutboxState = "retry_wait"
	OutboxReconciling    OutboxState = "reconciling"
	OutboxSucceeded      OutboxState = "succeeded"
	OutboxNeedsAttention OutboxState = "needs_attention"
	OutboxCancelled      OutboxState = "cancelled"
)

// OutboxStates liefert alle Zustände.
func OutboxStates() []OutboxState {
	return []OutboxState{OutboxPending, OutboxClaimed, OutboxRetryWait, OutboxReconciling, OutboxSucceeded, OutboxNeedsAttention, OutboxCancelled}
}

// Valid meldet, ob s ein bekannter Zustand ist.
func (s OutboxState) Valid() bool { return contains(OutboxStates(), s) }

var outboxTransitions = map[OutboxState][]OutboxState{
	OutboxPending:        {OutboxClaimed, OutboxCancelled},
	OutboxClaimed:        {OutboxSucceeded, OutboxRetryWait, OutboxReconciling, OutboxNeedsAttention},
	OutboxRetryWait:      {OutboxClaimed, OutboxCancelled, OutboxNeedsAttention},
	OutboxReconciling:    {OutboxReconciling, OutboxSucceeded, OutboxPending, OutboxNeedsAttention},
	OutboxNeedsAttention: {OutboxPending, OutboxSucceeded, OutboxCancelled},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist (siehe Typkommentar).
func (s OutboxState) CanTransition(to OutboxState) bool { return contains(outboxTransitions[s], to) }

// IsTerminal meldet, ob s ein Endzustand ist (erledigt oder abgebrochen).
func (s OutboxState) IsTerminal() bool { return s == OutboxSucceeded || s == OutboxCancelled }

// Ereignisarten des Auditverlaufs für die Outbox (Ebene AuditLevelOutbox). Sie stehen bewusst hier und nicht in audit.go;
// OutboxAuditKinds liefert sie.
const (
	AuditOutboxEnqueued       = "outbox.enqueued"        // Absicht gespeichert
	AuditOutboxClaimed        = "outbox.claimed"         // Aufruf beginnt (Versuch +1)
	AuditOutboxSucceeded      = "outbox.succeeded"       // erledigt, mit externer Referenz
	AuditOutboxRetryScheduled = "outbox.retry_scheduled" // sicherer, wiederholbarer Fehler: Wartezeit
	AuditOutboxUncertain      = "outbox.uncertain"       // ungewisser Ausgang: Abgleich nötig
	AuditOutboxClaimExpired   = "outbox.claim_expired"   // Claim abgelaufen: Ausgang unbekannt, Abgleich nötig
	AuditOutboxReconcileRetry = "outbox.reconcile_retry" // Abgleich nicht schlüssig, bleibt reconciling
	AuditOutboxRequeued       = "outbox.requeued"        // Abgleich belegt „nicht vorhanden“: erneuter Versuch zulässig
	AuditOutboxNeedsAttention = "outbox.needs_attention" // Klärungsbedarf (dauerhafter Fehler oder Grenze erreicht)
	AuditOutboxResolved       = "outbox.resolved"        // ausdrückliche Klärung mit Akteur und Grund
	AuditOutboxCancelled      = "outbox.cancelled"       // vor dem Aufruf abgebrochen
)

// OutboxAuditKinds liefert die Ereignisarten der Outbox.
func OutboxAuditKinds() []string {
	return []string{
		AuditOutboxEnqueued, AuditOutboxClaimed, AuditOutboxSucceeded, AuditOutboxRetryScheduled, AuditOutboxUncertain,
		AuditOutboxClaimExpired, AuditOutboxReconcileRetry, AuditOutboxRequeued, AuditOutboxNeedsAttention,
		AuditOutboxResolved, AuditOutboxCancelled,
	}
}
