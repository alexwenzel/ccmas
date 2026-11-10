package domain

import "time"

// LeaseState ist der Zustand einer Reservierung eines Arbeitsauftrags für einen Agenten.
//
// Eine Lease beginnt „aktiv“ und endet in genau einem der drei Endzustände: freigegeben (der Besitzer gibt sie zurück,
// etwa nach dem Ende der Ausführung), abgelaufen (die Gültigkeit endete, ohne dass sie erneuert wurde) oder widerrufen
// (der Dienst nimmt sie zurück, etwa bei Abbruch oder Wiederherstellung). Aus einem Endzustand führt kein Weg zurück:
// Eine neue Reservierung ist eine neue Lease mit höherer Generation, nie eine wiederbelebte alte. Das ist die Grundlage
// dafür, dass eine verspätete Meldung einer alten Lease keine neue abschließt.
type LeaseState string

const (
	LeaseActive   LeaseState = "active"
	LeaseReleased LeaseState = "released"
	LeaseExpired  LeaseState = "expired"
	LeaseRevoked  LeaseState = "revoked"
)

// LeaseStates liefert alle Zustände (die CHECK-Liste der Tabelle leases).
func LeaseStates() []LeaseState {
	return []LeaseState{LeaseActive, LeaseReleased, LeaseExpired, LeaseRevoked}
}

// Valid meldet, ob s ein bekannter Zustand ist.
func (s LeaseState) Valid() bool { return contains(LeaseStates(), s) }

var leaseTransitions = map[LeaseState][]LeaseState{
	LeaseActive: {LeaseReleased, LeaseExpired, LeaseRevoked},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist.
func (s LeaseState) CanTransition(to LeaseState) bool {
	return contains(leaseTransitions[s], to)
}

// IsTerminal meldet, ob s ein Endzustand der Lease ist.
func (s LeaseState) IsTerminal() bool { return s.Valid() && s != LeaseActive }

// Ereignisarten des Auditverlaufs für Leases (Ebene AuditLevelLease). Sie stehen hier und nicht in AuditKinds, damit die
// Liste dort von parallel entstehenden Ebenen unberührt bleibt; LeaseAuditKinds liefert sie.
const (
	AuditLeaseAcquired     = "lease.acquired"
	AuditLeaseAttemptBound = "lease.attempt_bound"
	AuditLeaseReleased     = "lease.released"
	AuditLeaseExpired      = "lease.expired"
	AuditLeaseRevoked      = "lease.revoked"
)

// LeaseAuditKinds liefert die Ereignisarten der Lease-Ebene.
func LeaseAuditKinds() []string {
	return []string{AuditLeaseAcquired, AuditLeaseAttemptBound, AuditLeaseReleased, AuditLeaseExpired, AuditLeaseRevoked}
}

// ExecutionLease ist die Reservierung eines Arbeitsauftrags für einen Agenten.
//
// Generation zählt die Leases eines Auftrags lückenlos ab 1; die höchste ist die jüngste. Eine Meldung, die zu einer
// älteren Generation gehört, hat keine Wirkung auf die jüngere. ExecutionID und Attempt binden die Lease an einen
// Ausführungsversuch desselben Auftrags (beide oder keins; ohne sie ist reserviert, aber noch nicht gestartet).
// WorktreePath ist optional (kein Worktree, solange keiner vorbereitet ist); ein genannter Worktree gehört höchstens einer
// aktiven Lease. ReleasedAt ist die Zeit des Übergangs in einen Endzustand und nur dort gesetzt.
type ExecutionLease struct {
	LeaseID      string     `json:"lease_id"`
	ProjectID    string     `json:"project_id"`
	WorkOrderID  string     `json:"work_order_id"`
	ExecutionID  string     `json:"execution_id,omitempty"`
	Attempt      int        `json:"attempt,omitempty"`
	AgentID      string     `json:"agent_id"`
	Generation   int64      `json:"generation"`
	State        LeaseState `json:"state"`
	AcquiredAt   time.Time  `json:"acquired_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	ReleasedAt   time.Time  `json:"released_at,omitzero"`
	WorktreePath string     `json:"worktree_path,omitempty"`
}

// Check prüft die Invarianten einer Lease.
func (l ExecutionLease) Check() error {
	for _, id := range []string{l.LeaseID, l.ProjectID, l.WorkOrderID, l.AgentID} {
		if err := CheckID(id); err != nil {
			return err
		}
	}
	switch {
	case l.ExecutionID != "":
		if err := CheckID(l.ExecutionID); err != nil {
			return err
		}
		if l.Attempt < 1 {
			return newError(CatValue, "/attempt", "Versuch %d ungültig (ab 1)", l.Attempt)
		}
	case l.Attempt != 0:
		return newError(CatValue, "/attempt", "Versuch %d ohne Ausführung", l.Attempt)
	}
	if l.Generation < 1 {
		return newError(CatValue, "/generation", "Generation %d ungültig (ab 1)", l.Generation)
	}
	if !l.State.Valid() {
		return newError(CatState, "/state", "unbekannter Lease-Zustand %q", l.State)
	}
	if l.AcquiredAt.IsZero() {
		return newError(CatTimestamp, "/acquired_at", "Beginn der Lease fehlt")
	}
	if l.ExpiresAt.IsZero() {
		return newError(CatTimestamp, "/expires_at", "Ablaufzeit der Lease fehlt")
	}
	if !l.ExpiresAt.After(l.AcquiredAt) {
		return newError(CatTimestamp, "/expires_at", "die Lease läuft nicht nach ihrem Beginn ab")
	}
	switch {
	case l.State == LeaseActive && !l.ReleasedAt.IsZero():
		return newError(CatTimestamp, "/released_at", "eine aktive Lease hat keine Endzeit")
	case l.State != LeaseActive && l.ReleasedAt.IsZero():
		return newError(CatTimestamp, "/released_at", "eine beendete Lease (%s) braucht eine Endzeit", l.State)
	case !l.ReleasedAt.IsZero() && l.ReleasedAt.Before(l.AcquiredAt):
		return newError(CatTimestamp, "/released_at", "das Ende liegt vor dem Beginn der Lease")
	}
	if l.WorktreePath != "" {
		if err := CheckCanonicalPath(l.WorktreePath); err != nil {
			return err
		}
	}
	return nil
}
