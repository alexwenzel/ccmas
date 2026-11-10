package domain

// LeaseState ist der Zustand einer Reservierung eines Arbeitsauftrags für einen Agenten.
//
// Vorläufig: Nur die Zustandsliste steht fest (sie ist die CHECK-Liste der Tabelle leases). Eine Übergangstabelle
// und CanTransition gibt es noch nicht; sie kommen mit der Reservierungslogik in CC-42.
type LeaseState string

const (
	LeaseActive   LeaseState = "active"
	LeaseReleased LeaseState = "released"
	LeaseExpired  LeaseState = "expired"
	LeaseRevoked  LeaseState = "revoked"
)

// LeaseStates liefert alle Zustände.
func LeaseStates() []LeaseState {
	return []LeaseState{LeaseActive, LeaseReleased, LeaseExpired, LeaseRevoked}
}

// Valid meldet, ob s ein bekannter Zustand ist.
func (s LeaseState) Valid() bool { return contains(LeaseStates(), s) }
