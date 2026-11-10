package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// SnapshotReader ist, was BuildSnapshot aus dem führenden Zustand liest. *persistence.ReadTx und *persistence.Tx erfüllen
// es beide, damit der Snapshot sowohl in einer Lesetransaktion als auch innerhalb der Schreibtransaktion einer Reservierung
// entsteht (dann sieht er die eigenen, noch nicht festgeschriebenen Änderungen und dieselbe Revision).
type SnapshotReader interface {
	Project(projectID string) (persistence.ProjectRecord, error)
	Goal(projectID, goalID string) (persistence.GoalRecord, error)
	WorkOrders(projectID string) ([]persistence.WorkOrderRecord, error)
	LastReview(projectID, workOrderID string) (*supervisor.ReviewRecord, error)
	ActiveExecutions(projectID string) ([]supervisor.ActiveExecution, error)
	OpenQuestions(projectID string) ([]supervisor.OpenQuestion, error)
}

var (
	_ SnapshotReader = (*persistence.ReadTx)(nil)
	_ SnapshotReader = (*persistence.Tx)(nil)
)

// SnapshotOffers sind die Angaben im Snapshot, die nicht aus SQLite kommen: die erlaubten Rollenprofile und die
// freigegebenen Skills (supervisor.BuildRoleOffers und supervisor.BuildSkillOffers aus Konfiguration und Skill-Katalog).
type SnapshotOffers struct {
	Roles  []supervisor.RoleOffer
	Skills []supervisor.SkillOffer
}

// BuildSnapshot baut den ProjectSnapshot für Projekt und Ziel aus r (Inhalt und Regeln: Paketkommentar). Ein unbekanntes
// Projekt oder Ziel (auch das eines anderen Projekts) ist ein Fehler mit dem Code not_found der Persistenzschicht. Das
// Ergebnis ist kanonisch.
func BuildSnapshot(r SnapshotReader, projectID, goalID string, offers SnapshotOffers) (supervisor.ProjectSnapshot, error) {
	proj, err := r.Project(projectID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}
	goal, err := r.Goal(projectID, goalID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}
	records, err := r.WorkOrders(projectID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}
	active, err := r.ActiveExecutions(projectID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}
	questions, err := r.OpenQuestions(projectID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}

	included, err := snapshotOrderSet(records, goalID)
	if err != nil {
		return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
	}
	activeOf := map[string]string{} // Auftrag -> kleinste aktive Ausführungs-ID
	for _, a := range active {
		if a.WorkOrderID == "" {
			continue
		}
		if cur, ok := activeOf[a.WorkOrderID]; !ok || a.ExecutionID < cur {
			activeOf[a.WorkOrderID] = a.ExecutionID
		}
	}

	orders := make([]supervisor.Order, 0, len(included))
	for _, rec := range records {
		if !included[rec.WorkOrderID] {
			continue
		}
		review, err := r.LastReview(projectID, rec.WorkOrderID)
		if err != nil {
			return supervisor.ProjectSnapshot{}, snapshotErr(projectID, goalID, err)
		}
		orders = append(orders, snapshotOrder(rec, review, activeOf[rec.WorkOrderID]))
	}
	snapshotInheritConcepts(orders)

	open := []supervisor.OpenQuestion{}
	for _, q := range questions {
		if q.WorkOrderID == "" || included[q.WorkOrderID] {
			open = append(open, q)
		}
	}

	snap := supervisor.ProjectSnapshot{
		SchemaVersion:   supervisor.SnapshotSchemaVersion,
		ProjectID:       projectID,
		StateRevision:   proj.StateRevision,
		Goal:            goal.Goal,
		IntegrationMode: goal.IntegrationMode,
		Capacity:        supervisor.Capacity{MaxWorkers: proj.Config.MaxWorkers, Active: active},
		Orders:          orders,
		OpenQuestions:   open,
		Roles:           slices.Clone(offers.Roles),
		Skills:          slices.Clone(offers.Skills),
	}
	return snap.Canonical(), nil
}

func snapshotErr(projectID, goalID string, err error) error {
	return fmt.Errorf("orchestrator: Snapshot für Projekt %q, Ziel %q: %w", projectID, goalID, err)
}

// snapshotOrderSet liefert die IDs der Aufträge im Snapshot: die des Ziels und alles, wovon sie (auch über mehrere Stufen)
// abhängen. Ein Verweis auf einen Auftrag, den es nicht gibt, ist ein Fehler der Datenbank (der Fremdschlüssel verhindert
// ihn); er bräche sonst depends_on im Snapshot.
func snapshotOrderSet(records []persistence.WorkOrderRecord, goalID string) (map[string]bool, error) {
	byID := make(map[string]persistence.WorkOrderRecord, len(records))
	for _, rec := range records {
		byID[rec.WorkOrderID] = rec
	}
	included := map[string]bool{}
	var queue []string
	for _, rec := range records {
		if rec.GoalID == goalID {
			included[rec.WorkOrderID] = true
			queue = append(queue, rec.WorkOrderID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, dep := range byID[id].DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("Auftrag %q hängt von %q ab, den es nicht gibt", id, dep)
			}
			if !included[dep] {
				included[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return included, nil
}

func snapshotOrder(rec persistence.WorkOrderRecord, review *supervisor.ReviewRecord, activeExecution string) supervisor.Order {
	o := supervisor.Order{
		ID: rec.WorkOrderID, Kind: rec.Kind, Title: rec.Title, State: rec.State, Mode: rec.Mode,
		DependsOn:  slices.Clone(rec.DependsOn),
		HeadCommit: rec.HeadCommit, Branch: rec.Branch,
		CodeRepairRounds: rec.CodeRepairRounds, ConceptRepairRounds: rec.ConceptRepairRounds,
		ActiveExecutionID: activeExecution,
		LastReview:        review,
	}
	if rec.Concept != nil {
		c := *rec.Concept
		c.Criteria = slices.Clone(c.Criteria)
		if c.Approval != nil {
			a := *c.Approval
			c.Approval = &a
		}
		o.Concept = &c
	}
	if rec.LastResult != nil {
		l := *rec.LastResult
		o.LastResult = &l
	}
	if rec.PR != nil {
		p := *rec.PR
		o.PR = &p
	}
	return o
}

// snapshotReleased meldet, ob ein Konzeptstand freigegeben ist: Eine Freigabe liegt vor und gilt für genau seine Version.
func snapshotReleased(c *supervisor.ConceptState) bool {
	return c != nil && c.Approval != nil && c.Approval.Version == c.Version
}

func snapshotSameConcept(a, b *supervisor.ConceptState) bool {
	if a.Version != b.Version || a.Class != b.Class || *a.Approval != *b.Approval {
		return false
	}
	ac, bc := slices.Clone(a.Criteria), slices.Clone(b.Criteria)
	sort.Strings(ac)
	sort.Strings(bc)
	return slices.Equal(ac, bc)
}

// snapshotInheritConcepts überträgt den freigegebenen Konzeptstand der Konzeptaufträge auf die abhängigen Umsetzungs- und
// Fehleraufträge ohne eigenen Konzeptstand (Regeln: Paketkommentar). Quellen sind nur die Konzeptstände aus der Datenbank,
// nie ein bereits übertragener Stand; deshalb kommt es auf die Reihenfolge nicht an.
func snapshotInheritConcepts(orders []supervisor.Order) {
	byID := make(map[string]*supervisor.ConceptState, len(orders))
	isConcept := make(map[string]bool, len(orders))
	for i := range orders {
		isConcept[orders[i].ID] = orders[i].Kind == supervisor.OrderConcept
		byID[orders[i].ID] = orders[i].Concept
	}
	for i := range orders {
		o := &orders[i]
		if o.Concept != nil || (o.Kind != supervisor.OrderImplementation && o.Kind != supervisor.OrderBug) {
			continue
		}
		var sources []*supervisor.ConceptState
		unclear := false
		for _, dep := range o.DependsOn {
			if !isConcept[dep] {
				continue
			}
			c := byID[dep]
			if !snapshotReleased(c) {
				unclear = true
				break
			}
			sources = append(sources, c)
		}
		if unclear || len(sources) == 0 {
			continue
		}
		same := true
		for _, c := range sources[1:] {
			same = same && snapshotSameConcept(sources[0], c)
		}
		if !same {
			continue
		}
		c := *sources[0]
		c.Criteria = slices.Clone(c.Criteria)
		a := *c.Approval
		c.Approval = &a
		o.Concept = &c
	}
}

// StoreSnapshotSource erfüllt supervisor.SnapshotSource über den Store: Jeder Aufruf baut den Snapshot in einer eigenen
// Lesetransaktion neu. Er gilt für ein festes Projekt und Ziel.
type StoreSnapshotSource struct {
	store     *persistence.Store
	projectID string
	goalID    string
	offers    SnapshotOffers
}

var _ supervisor.SnapshotSource = (*StoreSnapshotSource)(nil)

// NewStoreSnapshotSource erzeugt die Quelle für Projekt und Ziel. Die Angebote werden kopiert.
func NewStoreSnapshotSource(store *persistence.Store, projectID, goalID string, offers SnapshotOffers) (*StoreSnapshotSource, error) {
	if store == nil {
		return nil, errors.New("orchestrator: Store fehlt")
	}
	if err := domain.CheckID(projectID); err != nil {
		return nil, fmt.Errorf("orchestrator: Projekt: %w", err)
	}
	if err := domain.CheckID(goalID); err != nil {
		return nil, fmt.Errorf("orchestrator: Ziel: %w", err)
	}
	return &StoreSnapshotSource{store: store, projectID: projectID, goalID: goalID,
		offers: SnapshotOffers{Roles: slices.Clone(offers.Roles), Skills: slices.Clone(offers.Skills)}}, nil
}

// CurrentSnapshot liefert den aktuellen Zustand des Projekts. Ein anderes als das Projekt der Quelle ist ein Fehler.
func (s *StoreSnapshotSource) CurrentSnapshot(ctx context.Context, projectID string) (supervisor.ProjectSnapshot, error) {
	if projectID != s.projectID {
		return supervisor.ProjectSnapshot{}, fmt.Errorf("orchestrator: die Quelle gilt für Projekt %q, nicht für %q", s.projectID, projectID)
	}
	var snap supervisor.ProjectSnapshot
	err := s.store.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		snap, err = BuildSnapshot(r, s.projectID, s.goalID, s.offers)
		return err
	})
	if err != nil {
		return supervisor.ProjectSnapshot{}, err
	}
	return snap, nil
}
