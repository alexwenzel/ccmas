package persistence

import (
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// Aufzählungen, die nur die Persistenz kennt oder deren Go-Liste im Paket supervisor/skills nicht als Liste
// vorliegt. Die CHECK-Listen der Migration 001 entsprechen genau diesen Listen (TestEnumChecksMatchDomain).

// LeaseState ist der Zustand einer Reservierung (Logik folgt in CC-42).
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

// DeliveryState ist der Zustand der Zustellung einer Nachricht an einen Empfänger (Logik folgt in CC-45).
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

// OutboxState ist der Zustand einer externen Aktion (Logik folgt in CC-45).
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

// prStates, ciStates, conceptClasses und approvers sind die Werte aus supervisor und skills (dort ohne Listenfunktion).
func prStates() []supervisor.PRStatus {
	return []supervisor.PRStatus{supervisor.PROpen, supervisor.PRMerged, supervisor.PRClosed}
}

func ciStates() []supervisor.CIStatus {
	return []supervisor.CIStatus{supervisor.CIPassed, supervisor.CIFailed, supervisor.CIPending, supervisor.CIMissing}
}

func conceptClasses() []skills.ConceptClass {
	return []skills.ConceptClass{skills.ConceptRoutine, skills.ConceptStrategic}
}

func approvers() []skills.Approver {
	return []skills.Approver{skills.ApproverConceptReviewer, skills.ApproverUser}
}

func integrationStates() []skills.IntegrationState {
	return []skills.IntegrationState{skills.IntegrationNone, skills.IntegrationPROpen, skills.IntegrationMerged}
}

func mergeActors() []skills.MergeActor {
	return []skills.MergeActor{"", skills.MergedByHuman, skills.MergedByQueue}
}

func has[T comparable](list []T, v T) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func conceptClassOf(s string) skills.ConceptClass { return skills.ConceptClass(s) }

func approverOf(s string) skills.Approver { return skills.Approver(s) }
