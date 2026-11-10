package domain

import (
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"
)

// Das Audit-Ereignis ist der Eintrag des Auditverlaufs (Spezifikation „Führender interner Zustand“,
// Entität messages/events). Es entsteht ausschließlich im Go-Dienst, in derselben Transaktion wie die
// Änderung, die es beschreibt (internal/persistence). Die Details bestehen nur aus kurzen, typisierten
// Feldern; Prompts, Umgebungsvariablen und Zugangsdaten gehören nie dazu.

// AuditLevel ist die Zustandsebene (oder der Bereich), zu der ein Ereignis gehört. Die Ebenen des
// Zustandsmodells (Auftrag, Ausführung, Prozess, Agent) erzeugen getrennte Ereignisse.
type AuditLevel string

const (
	AuditLevelConfig    AuditLevel = "config"     // Projektzuordnung, Profilversionen, Skill-Snapshots
	AuditLevelGoal      AuditLevel = "goal"       // Ziel (Ticket, Feature, Milestone)
	AuditLevelWorkOrder AuditLevel = "work_order" // fachlicher Auftragszustand und Auftragsdaten
	AuditLevelExecution AuditLevel = "execution"  // technischer Ausführungsversuch
	AuditLevelProcess   AuditLevel = "process"    // beobachteter Betriebssystem-Prozess
	AuditLevelAgent     AuditLevel = "agent"      // Agentenverfügbarkeit
	AuditLevelQuestion  AuditLevel = "question"   // Fragen und Antworten
	AuditLevelDecision  AuditLevel = "decision"   // Planungsergebnis und Validierungsentscheidung
	AuditLevelReview    AuditLevel = "review"     // Review-Berichte
	AuditLevelTask      AuditLevel = "task"       // Asana-Abbild
	AuditLevelLease     AuditLevel = "lease"      // Reservierungen (ab CC-42)
	AuditLevelMessage   AuditLevel = "message"    // Nachrichten (ab CC-45)
	AuditLevelOutbox    AuditLevel = "outbox"     // externe Aktionen (ab CC-45)
)

// AuditLevels liefert alle Ebenen.
func AuditLevels() []AuditLevel {
	return []AuditLevel{
		AuditLevelConfig, AuditLevelGoal, AuditLevelWorkOrder, AuditLevelExecution, AuditLevelProcess,
		AuditLevelAgent, AuditLevelQuestion, AuditLevelDecision, AuditLevelReview, AuditLevelTask,
		AuditLevelLease, AuditLevelMessage, AuditLevelOutbox,
	}
}

// Valid meldet, ob l eine bekannte Ebene ist.
func (l AuditLevel) Valid() bool { return contains(AuditLevels(), l) }

// Ereignisarten, die die Repositories in internal/persistence erzeugen. Eine Art hat die Form
// <gegenstand>.<ereignis> in Kleinbuchstaben.
const (
	AuditProjectCreated        = "project.created"
	AuditProjectConfigUpdated  = "project.config_updated"
	AuditProfileRecorded       = "profile.recorded"
	AuditSkillSnapshotRecorded = "skill_snapshot.recorded"
	AuditGoalCreated           = "goal.created"
	AuditGoalEvidenceSet       = "goal.evidence_set"
	AuditWorkOrderCreated      = "work_order.created"
	AuditWorkOrderStateChanged = "work_order.state_changed"
	AuditWorkOrderHeadSet      = "work_order.head_set"
	AuditWorkOrderResultSet    = "work_order.result_set"
	AuditWorkOrderRepairRounds = "work_order.repair_rounds_set"
	AuditWorkOrderConceptSet   = "work_order.concept_set"
	AuditWorkOrderPRSet        = "work_order.pull_request_set"
	AuditWorkOrderDependency   = "work_order.dependency_added"
	AuditWorkOrderCancelled    = "work_order.cancelled" // Abbruch durch einen Akteur, mit Grund (CC-42)
	AuditWorkOrderResumed      = "work_order.resumed"   // Wiederaufnahme eines blockierten Auftrags, mit Grund (CC-42)
	AuditTaskSynced            = "task.synced"
	AuditAgentCreated          = "agent.created"
	AuditAgentAvailability     = "agent.availability_changed"
	AuditExecutionCreated      = "execution.created"
	AuditAttemptStarted        = "execution.attempt_started"
	AuditExecutionStateChanged = "execution.state_changed"
	AuditAttemptFinished       = "execution.attempt_finished"
	AuditSessionSet            = "execution.session_set"
	AuditProcessObserved       = "process.observed"
	AuditQuestionCreated       = "question.created"
	AuditQuestionAnswered      = "question.answered"
	AuditQuestionDelivered     = "question.delivered"
	AuditQuestionStateChanged  = "question.state_changed"
	AuditPlanRecorded          = "plan.recorded"
	AuditReviewRecorded        = "review.recorded"
)

// AuditKinds liefert alle Ereignisarten dieser Version.
func AuditKinds() []string {
	kinds := []string{
		AuditProjectCreated, AuditProjectConfigUpdated, AuditProfileRecorded, AuditSkillSnapshotRecorded,
		AuditGoalCreated, AuditGoalEvidenceSet, AuditWorkOrderCreated, AuditWorkOrderStateChanged,
		AuditWorkOrderHeadSet, AuditWorkOrderResultSet, AuditWorkOrderRepairRounds, AuditWorkOrderConceptSet,
		AuditWorkOrderPRSet, AuditWorkOrderDependency, AuditTaskSynced, AuditAgentCreated,
		AuditAgentAvailability, AuditExecutionCreated, AuditAttemptStarted, AuditExecutionStateChanged,
		AuditAttemptFinished, AuditSessionSet, AuditProcessObserved, AuditQuestionCreated,
		AuditQuestionAnswered, AuditQuestionDelivered, AuditQuestionStateChanged, AuditPlanRecorded,
		AuditReviewRecorded, AuditWorkOrderCancelled, AuditWorkOrderResumed,
	}
	return append(kinds, LeaseAuditKinds()...)
}

// Grenzen der Details: klein und typisiert, damit weder Prompts noch große Nutzdaten im Auditverlauf landen.
const (
	MaxAuditDetailsBytes = 4096 // serialisierte Details insgesamt
	MaxAuditValueBytes   = 512  // ein Detailwert
	maxAuditKeyBytes     = 64
	maxAuditEntityIDLen  = 300
)

var (
	auditKindPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}\.[a-z][a-z0-9_]{0,31}$`)
	auditNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	auditStatePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// AuditEvent ist ein Eintrag des Auditverlaufs. EventID vergibt die Datenbank (0 vor dem Schreiben).
// StateRevision ist die Zustandsrevision des Projekts nach der Änderung, die das Ereignis beschreibt;
// bei reinen Journaleinträgen (Plan, Profil) die unveränderte aktuelle Revision.
type AuditEvent struct {
	EventID       int64             `json:"event_id"`
	ProjectID     string            `json:"project_id"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Actor         string            `json:"actor"`
	Kind          string            `json:"kind"`
	EntityType    string            `json:"entity_type"`
	EntityID      string            `json:"entity_id"`
	Level         AuditLevel        `json:"level"`
	FromState     string            `json:"from_state,omitempty"`
	ToState       string            `json:"to_state,omitempty"`
	StateRevision int64             `json:"state_revision"`
	Details       map[string]string `json:"details,omitempty"`
}

// DetailsJSON liefert die Details deterministisch (sortierte Schlüssel); ohne Details {}.
func (e AuditEvent) DetailsJSON() ([]byte, error) {
	if len(e.Details) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(e.Details) // Schlüssel einer Map werden sortiert ausgegeben
}

// plainText meldet, ob s gültiges UTF-8 ohne Steuerzeichen ist (allowLayout erlaubt \n und \t).
func plainText(s string, allowLayout bool) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			if allowLayout {
				continue
			}
			return false
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// Check prüft Form und Grenzen eines Ereignisses. Ob die Details frei von Zugangsdaten sind, kann ein
// Wertetest nicht beweisen; deshalb baut sie nur die Persistenzschicht aus typisierten Feldern.
func (e AuditEvent) Check() error {
	if err := CheckID(e.ProjectID); err != nil {
		return err
	}
	if e.OccurredAt.IsZero() {
		return newError(CatTimestamp, "/occurred_at", "Ereigniszeit fehlt")
	}
	if err := CheckID(e.Actor); err != nil {
		return err
	}
	if !auditKindPattern.MatchString(e.Kind) {
		return newError(CatValue, "/kind", "Ereignisart %q hat nicht die Form gegenstand.ereignis", e.Kind)
	}
	if !auditNamePattern.MatchString(e.EntityType) {
		return newError(CatValue, "/entity_type", "Entitätsart %q ungültig", e.EntityType)
	}
	if e.EntityID == "" || len(e.EntityID) > maxAuditEntityIDLen || !plainText(e.EntityID, false) {
		return newError(CatID, "/entity_id", "Entitäts-ID ungültig")
	}
	if !e.Level.Valid() {
		return newError(CatValue, "/level", "unbekannte Ebene %q", e.Level)
	}
	for _, s := range []struct{ name, v string }{{"from_state", e.FromState}, {"to_state", e.ToState}} {
		if s.v != "" && !auditStatePattern.MatchString(s.v) {
			return newError(CatState, "/"+s.name, "Zustandsname %q ungültig", s.v)
		}
	}
	if e.StateRevision < 0 {
		return newError(CatRevision, "/state_revision", "negative Zustandsrevision %d", e.StateRevision)
	}
	for k, v := range e.Details {
		if !auditNamePattern.MatchString(k) {
			return newError(CatValue, "/details", "Detailschlüssel %q ungültig", k)
		}
		if len(v) > MaxAuditValueBytes {
			return newError(CatSize, "/details/"+k, "Detailwert hat %d Bytes, erlaubt sind %d", len(v), MaxAuditValueBytes)
		}
		if !plainText(v, false) {
			return newError(CatValue, "/details/"+k, "Detailwert enthält Steuerzeichen oder ungültiges UTF-8")
		}
	}
	raw, err := e.DetailsJSON()
	if err != nil {
		return newError(CatValue, "/details", "Details nicht serialisierbar: %v", err)
	}
	if len(raw) > MaxAuditDetailsBytes {
		return newError(CatSize, "/details", "Details haben %d Bytes, erlaubt sind %d", len(raw), MaxAuditDetailsBytes)
	}
	return nil
}
