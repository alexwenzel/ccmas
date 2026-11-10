package domain

// OutboxState ist der Zustand einer externen Aktion in der Outbox (zum Beispiel ein Schreibzugriff auf Asana oder
// GitHub).
//
// Vorläufig: Nur die Zustandsliste steht fest (sie ist die CHECK-Liste der Tabelle outbox). Eine Übergangstabelle
// und CanTransition gibt es noch nicht; sie kommen mit der Outbox-Logik in CC-45.
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
