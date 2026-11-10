package domain

// DeliveryState ist der Zustand der Zustellung einer Nachricht an einen Empfänger.
//
// Vorläufig: Nur die Zustandsliste steht fest (sie ist die CHECK-Liste der Tabelle message_deliveries). Eine
// Übergangstabelle und CanTransition gibt es noch nicht; sie kommen mit der Nachrichtenlogik in CC-45.
type DeliveryState string

const (
	DeliveryPending      DeliveryState = "pending"
	DeliveryDelivered    DeliveryState = "delivered"
	DeliveryAcknowledged DeliveryState = "acknowledged"
)

// DeliveryStates liefert alle Zustände.
func DeliveryStates() []DeliveryState {
	return []DeliveryState{DeliveryPending, DeliveryDelivered, DeliveryAcknowledged}
}

// Valid meldet, ob s ein bekannter Zustand ist.
func (s DeliveryState) Valid() bool { return contains(DeliveryStates(), s) }
