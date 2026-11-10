package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// DefaultLeaseGrace ist der Zuschlag auf die Laufzeit eines Versuchs (attempt_runtime_minutes), den eine Lease zusätzlich
// gilt: Zeit für Start, Stopp und das Eintreffen des letzten Ereignisses, bevor die Wiederherstellung (M09) die Lease als
// abgelaufen behandelt.
const DefaultLeaseGrace = 5 * time.Minute

// TasksGoalFunc liefert zu einem Projekt das beauftragte Ziel, gegen das der Snapshot gebaut und der Batch erneut geprüft wird.
type TasksGoalFunc func(projectID string) (goalID string, err error)

// TasksFixedGoal ist die TasksGoalFunc für ein festes Ziel (gilt für jedes Projekt).
func TasksFixedGoal(goalID string) TasksGoalFunc {
	return func(string) (string, error) { return goalID, nil }
}

// TasksPolicy sind die Einstellungen von Tasks. Die Nullwerte gelten: Uhr time.Now, Zuschlag DefaultLeaseGrace.
type TasksPolicy struct {
	// Now ist die Uhr für Beginn und Ablauf einer Lease. Der Store hat seine eigene Uhr (Zeit der Transaktion); in Produktion
	// sind beide time.Now, in Tests teilen sie sich eine.
	Now func() time.Time
	// LeaseGrace ist der Zuschlag auf attempt_runtime_minutes (0: DefaultLeaseGrace).
	LeaseGrace time.Duration
}

// Tasks führt die Operationen auf Arbeitsaufträgen aus: reservieren (ReserveBatch, ReserveTask), Ereignisse der Runner
// anwenden (ApplyEvent, ApplyProcessExit), abbrechen und wieder aufnehmen (CancelTask, ResumeTask). Jede Operation ist
// eine Transaktion; Regeln, Übergänge und Fencing: Paketkommentar und docs/architecture/orchestrator.md. Tasks ist
// nebenläufig nutzbar, auch über mehrere Store-Instanzen auf derselben Datei.
type Tasks struct {
	store  *persistence.Store
	policy supervisor.Policy
	offers SnapshotOffers
	goal   TasksGoalFunc
	actor  string
	opts   TasksPolicy
}

// NewTasks erzeugt Tasks. policy und offers sind dieselben, mit denen geplant wurde (Beispielkonfiguration und Skill-Katalog);
// actor ist der Akteur im Auditverlauf für Reservierungen und Ereignisse der Runner (CancelTask und ResumeTask nennen ihren
// Akteur selbst).
func NewTasks(store *persistence.Store, policy supervisor.Policy, offers SnapshotOffers, goal TasksGoalFunc, actor string, opts TasksPolicy) (*Tasks, error) {
	switch {
	case store == nil:
		return nil, errors.New("orchestrator: Store fehlt")
	case goal == nil:
		return nil, errors.New("orchestrator: Ziel (TasksGoalFunc) fehlt")
	case opts.LeaseGrace < 0:
		return nil, errors.New("orchestrator: LeaseGrace darf nicht negativ sein")
	}
	if err := policy.Check(); err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}
	if err := domain.CheckID(actor); err != nil {
		return nil, fmt.Errorf("orchestrator: Akteur: %w", err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.LeaseGrace == 0 {
		opts.LeaseGrace = DefaultLeaseGrace
	}
	return &Tasks{store: store, policy: policy, goal: goal, actor: actor, opts: opts,
		offers: SnapshotOffers{Roles: append([]supervisor.RoleOffer(nil), offers.Roles...), Skills: append([]supervisor.SkillOffer(nil), offers.Skills...)}}, nil
}

// ---- Fehler -----------------------------------------------------------------------------------------------------

// TaskClass ist die stabile, maschinenlesbare Klasse eines Fehlers von Tasks. Die Klassen sind Schnittstelle (CLI, Dienst,
// Protokollierung) und ändern sich nicht ohne Versionswechsel. Fehler der Schrittfunktion des Ausführungszustands behalten
// ihre Klasse aus domain (stale, sequence, id, message_type, value, ...).
type TaskClass string

const (
	// TaskStale: Ein Ereignis gehört zu einem älteren Versuch oder zu einer beendeten, widerrufenen oder abgelaufenen Lease. Es
	// hat keine Wirkung. (Klasse stale der Schrittfunktion.)
	TaskStale TaskClass = TaskClass(domain.CatStale)
	// TaskSequence: Das Ereignis passt nicht zur Folge (nach dem terminalen Ergebnis, nicht begonnener Versuch, unerlaubter
	// Übergang). Ein doppeltes terminales Ergebnis hat diese Klasse.
	TaskSequence TaskClass = TaskClass(domain.CatSequence)
	// TaskRevision: Die Revision des Plans oder des Auftrags ist veraltet; auf dem aktuellen Stand neu planen.
	TaskRevision TaskClass = TaskClass(domain.CatRevision)
	// TaskConflict: Gleichzeitiges oder doppeltes Handeln: die Entscheidung hat schon reserviert, der Auftrag ist schon
	// reserviert oder hat einen laufenden Versuch, das Profil hat sich seit der Planung geändert.
	TaskConflict TaskClass = "conflict"
	// TaskState: Der Zustand von Auftrag oder Agent erlaubt die Operation nicht.
	TaskState TaskClass = TaskClass(domain.CatState)
	// TaskDependency: Eine Abhängigkeit des Auftrags ist nicht abgeschlossen. blocked, cancelled und ein gescheiterter oder
	// abgestürzter Versuch geben nichts frei.
	TaskDependency TaskClass = "dependency"
	// TaskScope: Der Auftrag gehört nicht zum beauftragten Umfang des Ziels.
	TaskScope TaskClass = "scope"
	// TaskCapacity: Alle Worker-Plätze (max_workers) sind belegt.
	TaskCapacity TaskClass = "capacity"
	// TaskUnavailable: Es gibt keinen verfügbaren Agenten der Rolle (oder der genannte ist nicht verfügbar).
	TaskUnavailable TaskClass = "unavailable"
	// TaskRejected: Der Plan ist abgelehnt oder die erneute Prüfung des Validators lehnt ab; TaskError.Rejections nennt die
	// Ablehnungen strukturiert.
	TaskRejected TaskClass = "rejected"
	// TaskNotFound: Die Entität (Plan, Auftrag, Ausführung, Agent) gibt es nicht.
	TaskNotFound TaskClass = "not_found"
	// TaskInvalid: Die Eingabe ist ungültig (ID, Grund, Anfrage).
	TaskInvalid TaskClass = "invalid"
	// TaskBusy: Die Datenbank blieb gesperrt; Wiederholen ist zulässig.
	TaskBusy TaskClass = "busy"
	// TaskInternal: Ein unerwarteter Fehler der Datenbank.
	TaskInternal TaskClass = "internal"
)

// TaskError ist der Fehlertyp von Tasks: eine stabile Klasse, die Operation, eine Meldung und die Ursache. Bei TaskRejected
// stehen die Ablehnungen des Validators (oder des gespeicherten Plans) in Rejections.
type TaskError struct {
	Class      TaskClass
	Op         string
	Message    string
	Rejections []supervisor.Rejection
	Err        error
}

func (e *TaskError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "orchestrator %s [%s]", e.Op, e.Class)
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

// Unwrap liefert die Ursache.
func (e *TaskError) Unwrap() error { return e.Err }

// Is erlaubt errors.Is(err, &TaskError{Class: TaskStale}): gleiche Klasse.
func (e *TaskError) Is(target error) bool {
	t, ok := target.(*TaskError)
	return ok && t.Class == e.Class && t.Message == "" && t.Err == nil && t.Op == ""
}

// TaskClassOf liefert die Klasse des ersten *TaskError in der Kette (leer, wenn es keinen gibt).
func TaskClassOf(err error) TaskClass {
	var te *TaskError
	if errors.As(err, &te) {
		return te.Class
	}
	return ""
}

func taskErr(class TaskClass, op, format string, args ...any) *TaskError {
	return &TaskError{Class: class, Op: op, Message: fmt.Sprintf(format, args...)}
}

// taskWrap ordnet einen Fehler einer Klasse zu. Ein TaskError und ein Abbruch des Kontexts bleiben unverändert; ein Fehler der
// Persistenzschicht wird nach seinem Code, ein Vertragsverstoß nach seiner Fehlerklasse abgebildet.
func taskWrap(op string, err error) error {
	if err == nil {
		return nil
	}
	var te *TaskError
	if errors.As(err, &te) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	class := TaskInternal
	switch persistence.CodeOf(err) {
	case persistence.CodeNotFound:
		class = TaskNotFound
	case persistence.CodeConflict:
		class = TaskConflict
	case persistence.CodeState:
		class = TaskState
	case persistence.CodeInvalid:
		class = TaskInvalid
	case persistence.CodeBusy:
		class = TaskBusy
	case "":
		var ce *domain.ContractError
		if errors.As(err, &ce) {
			class = TaskClass(ce.Category())
		}
	}
	return &TaskError{Class: class, Op: op, Err: err}
}

func taskCheckIDs(op string, ids ...string) error {
	for _, id := range ids {
		if err := domain.CheckID(id); err != nil {
			return &TaskError{Class: TaskInvalid, Op: op, Err: err}
		}
	}
	return nil
}

// update führt fn in einer Schreibtransaktion aus und bildet Fehler auf TaskClass ab.
func (t *Tasks) update(ctx context.Context, actor, op string, fn func(tx *persistence.Tx) error) (persistence.UpdateResult, error) {
	res, err := t.store.Update(ctx, actor, fn)
	return res, taskWrap(op, err)
}

// ---- Reservierung -----------------------------------------------------------------------------------------------

// ReserveTaskRequest ist die Anfrage von ReserveTask. Der Ticketvertrag nennt nur (Auftrag, Agent, erwartete Revision); die
// Übergabe aus CC-14 verlangt, dass nur ein angenommener Batch reserviert. Deshalb gehören Batch und Entscheidung dazu.
type ReserveTaskRequest struct {
	ProjectID   string
	WorkOrderID string // der Auftrag ("taskID" des Ticketvertrags)
	// AgentID nennt den Agenten; leer: Wahl nach Agentenwahl (ReserveBatch).
	AgentID string
	// ExpectedRevision ist die Zeilenrevision des Auftrags (ab 1), auf der die Entscheidung beruht.
	ExpectedRevision int64
	BatchID          string
	DecisionID       string
}

// ReserveBatch reserviert alle Delegationen des gespeicherten, angenommenen Batches in einer Transaktion: Entweder gelingen
// alle, oder nichts ändert sich. Reihenfolge der Prüfung und der Leases: wie im Batch. Ablauf und Fehlerklassen:
// docs/architecture/orchestrator.md. Aktionen ohne Delegation (wait, ask_question, plan_extend, request_integration,
// request_completion) führt ReserveBatch nicht aus.
func (t *Tasks) ReserveBatch(ctx context.Context, projectID, batchID string) ([]domain.ExecutionLease, error) {
	return t.reserve(ctx, "ReserveBatch", projectID, batchID, nil)
}

// ReserveTask reserviert die eine Delegation eines Batches, der genau diese eine Delegation enthält, mit denselben Schritten
// wie ReserveBatch und zusätzlich der Revisionsprüfung des Auftrags. Hat der Batch mehrere Delegationen, verweist der Fehler
// auf ReserveBatch.
func (t *Tasks) ReserveTask(ctx context.Context, req ReserveTaskRequest) (domain.ExecutionLease, error) {
	const op = "ReserveTask"
	if err := taskCheckIDs(op, req.ProjectID, req.WorkOrderID, req.BatchID, req.DecisionID); err != nil {
		return domain.ExecutionLease{}, err
	}
	if req.AgentID != "" {
		if err := taskCheckIDs(op, req.AgentID); err != nil {
			return domain.ExecutionLease{}, err
		}
	}
	if req.ExpectedRevision < 1 {
		return domain.ExecutionLease{}, taskErr(TaskInvalid, op, "die erwartete Revision des Auftrags beginnt bei 1 (angegeben: %d)", req.ExpectedRevision)
	}
	ls, err := t.reserve(ctx, op, req.ProjectID, req.BatchID, &req)
	if err != nil {
		return domain.ExecutionLease{}, err
	}
	return ls[0], nil
}

func (t *Tasks) reserve(ctx context.Context, op, projectID, batchID string, only *ReserveTaskRequest) ([]domain.ExecutionLease, error) {
	if err := taskCheckIDs(op, projectID, batchID); err != nil {
		return nil, err
	}
	goalID, err := t.goal(projectID)
	if err != nil {
		return nil, &TaskError{Class: TaskInvalid, Op: op, Message: "das beauftragte Ziel ist nicht bestimmbar", Err: err}
	}
	var leases []domain.ExecutionLease
	_, err = t.update(ctx, t.actor, op, func(tx *persistence.Tx) error {
		var err error
		leases, err = t.reserveTx(tx, op, projectID, goalID, batchID, only)
		return err
	})
	if err != nil {
		return nil, err
	}
	return leases, nil
}

// taskScope bündelt, was alle Reservierungen einer Transaktion gemeinsam haben.
type taskScope struct {
	projectID, goalID, batchID string
	at, expires                time.Time
}

// taskDelegation ist eine Delegation des Batches: die Entscheidung, der Auftrag und das wirksame Profil.
type taskDelegation struct {
	decisionID string
	orderID    string
	profile    domain.AgentProfile
}

// taskPick sind die Vorgaben einer Einzelreservierung: ein genannter Agent und die erwartete Zeilenrevision des Auftrags.
type taskPick struct {
	agentID          string
	expectedRevision *int64
}

// reserveTx ist der Ablauf von ReserveBatch und ReserveTask in der Transaktion (Reihenfolge der Prüfungen ist die
// Festlegung): Plan angenommen; keine Entscheidung hat schon reserviert; Revision des Plans aktuell; Validator erneut auf
// dem Snapshot der Transaktion; wirksame Profile gleich den gespeicherten; je Delegation reservieren.
func (t *Tasks) reserveTx(tx *persistence.Tx, op, projectID, goalID, batchID string, only *ReserveTaskRequest) ([]domain.ExecutionLease, error) {
	plan, err := tx.Plan(projectID, batchID)
	if err != nil {
		return nil, err
	}
	if plan.Verdict != supervisor.VerdictAccepted {
		return nil, &TaskError{Class: TaskRejected, Op: op, Message: fmt.Sprintf("Plan %q ist nicht angenommen (%s); nur ein angenommener Batch reserviert", batchID, plan.Verdict),
			Rejections: append([]supervisor.Rejection(nil), plan.Rejections...)}
	}
	records, err := tx.Decisions(projectID, batchID)
	if err != nil {
		return nil, err
	}
	var stored []persistence.DecisionRecord
	for _, d := range records {
		if d.Decision.Action.RequiresSelection() {
			stored = append(stored, d)
		}
	}
	if len(stored) == 0 {
		return nil, taskErr(TaskInvalid, op, "Plan %q enthält keine Delegation, die reserviert werden könnte", batchID)
	}

	// Doppelte Entscheidung: vor der Revisionsprüfung, damit die erneute Zustellung desselben Batches ein Konflikt ist und
	// kein „veraltet“ (die erste Reservierung hat die Revision erhöht).
	for _, d := range stored {
		prev, err := tx.DecisionLease(projectID, batchID, d.Decision.DecisionID)
		if err != nil {
			return nil, err
		}
		if prev != nil {
			return nil, taskErr(TaskConflict, op, "die Entscheidung %s/%s hat schon reserviert (Lease %s)", batchID, d.Decision.DecisionID, prev.LeaseID)
		}
	}
	if only != nil {
		switch {
		case len(stored) != 1:
			return nil, taskErr(TaskInvalid, op, "Plan %q enthält %d Delegationen; ReserveTask reserviert genau eine, mehrere reserviert ReserveBatch", batchID, len(stored))
		case stored[0].Decision.DecisionID != only.DecisionID:
			return nil, taskErr(TaskInvalid, op, "die Entscheidung %q gehört nicht zur Delegation des Plans %q", only.DecisionID, batchID)
		case stored[0].Decision.Target.ID != only.WorkOrderID:
			return nil, taskErr(TaskInvalid, op, "die Entscheidung %q gilt dem Auftrag %q, nicht %q", only.DecisionID, stored[0].Decision.Target.ID, only.WorkOrderID)
		}
	}

	rev, err := tx.Revision(projectID)
	if err != nil {
		return nil, err
	}
	if plan.Batch.StateRevision != rev {
		return nil, taskErr(TaskRevision, op, "Plan %q beruht auf Zustandsrevision %d, aktuell ist %d; auf dem aktuellen Stand neu planen", batchID, plan.Batch.StateRevision, rev)
	}

	// Der Validator läuft erneut, in dieser Transaktion, auf dem Snapshot derselben Revision.
	snap, err := BuildSnapshot(tx, projectID, goalID, t.offers)
	if err != nil {
		return nil, err
	}
	validated, err := supervisor.Validator{}.Accept(plan.Batch, snap, t.policy)
	if err != nil {
		if re, ok := supervisor.AsRejection(err); ok {
			return nil, &TaskError{Class: TaskRejected, Op: op, Message: "die erneute Prüfung des Batches in der Transaktion lehnt ab", Rejections: re.Rejections, Err: re}
		}
		return nil, err
	}
	var dels []taskDelegation
	for _, vd := range validated.Decisions {
		if vd.Delegation == nil {
			continue
		}
		id := vd.Decision.DecisionID
		want, ok := plan.Delegations[id]
		if !ok {
			return nil, taskErr(TaskConflict, op, "für die Entscheidung %s ist kein wirksames Profil gespeichert; neu planen", id)
		}
		if !taskSameProfile(want, vd.Delegation.Profile) {
			return nil, taskErr(TaskConflict, op, "das wirksame Profil der Entscheidung %s weicht vom geplanten ab (%s v%d gespeichert, %s v%d jetzt): Policy oder Konfiguration haben sich geändert, neu planen",
				id, want.ProfileID, want.Version, vd.Delegation.Profile.ProfileID, vd.Delegation.Profile.Version)
		}
		dels = append(dels, taskDelegation{decisionID: id, orderID: vd.Delegation.OrderID, profile: vd.Delegation.Profile})
	}
	if len(dels) != len(stored) {
		return nil, taskErr(TaskConflict, op, "der Batch hat %d gespeicherte, aber %d erneut geprüfte Delegationen; neu planen", len(stored), len(dels))
	}

	proj, err := tx.Project(projectID)
	if err != nil {
		return nil, err
	}
	at := t.opts.Now().UTC()
	sc := taskScope{projectID: projectID, goalID: goalID, batchID: batchID, at: at,
		expires: at.Add(time.Duration(proj.Config.Resources.AttemptRuntimeMinutes)*time.Minute + t.opts.LeaseGrace)}

	leases := make([]domain.ExecutionLease, 0, len(dels))
	for _, d := range dels {
		var pick taskPick
		if only != nil {
			expected := only.ExpectedRevision
			pick = taskPick{agentID: only.AgentID, expectedRevision: &expected}
		}
		l, err := t.reserveOne(tx, sc, d, pick)
		if err != nil {
			return nil, err
		}
		leases = append(leases, l)
	}
	return leases, nil
}

// taskSameProfile vergleicht zwei Profile nach ihrer kanonischen JSON-Form.
func taskSameProfile(a, b domain.AgentProfile) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// taskID leitet eine ID deterministisch aus der Entscheidung ab: dieselbe Entscheidung kann nie zwei Ausführungen oder Leases
// anlegen (die Primärschlüssel kollidieren), auch wenn alle anderen Prüfungen versagten.
func taskID(prefix string, sc taskScope, decisionID string) string {
	sum := sha256.Sum256([]byte(sc.projectID + "\x00" + sc.batchID + "\x00" + decisionID))
	return prefix + "-" + hex.EncodeToString(sum[:])[:24]
}

// reserveOne reserviert eine Delegation: prüft (als Schutz in der Transaktion, auch was der Validator schon prüft) und legt
// Ausführung, Versuch 1, Lease, Auftragsübergang und Agentenzustand an. Die Revision steigt in der Transaktion genau einmal.
func (t *Tasks) reserveOne(tx *persistence.Tx, sc taskScope, d taskDelegation, pick taskPick) (domain.ExecutionLease, error) {
	const op = "reserve"
	role := d.profile.Role
	if role == domain.RoleSupervisor {
		return domain.ExecutionLease{}, taskErr(TaskInvalid, op, "die Rolle supervisor hat keinen Auftrag und reserviert nicht")
	}
	order, err := tx.WorkOrder(sc.projectID, d.orderID)
	if err != nil {
		return domain.ExecutionLease{}, err
	}
	if pick.expectedRevision != nil {
		if err := tx.RequireWorkOrderRevision(sc.projectID, d.orderID, *pick.expectedRevision); err != nil {
			if persistence.CodeOf(err) == persistence.CodeConflict {
				return domain.ExecutionLease{}, &TaskError{Class: TaskRevision, Op: op, Message: "die Revision des Auftrags ist veraltet; auf dem aktuellen Stand neu planen", Err: err}
			}
			return domain.ExecutionLease{}, err
		}
	}
	if order.GoalID != sc.goalID {
		return domain.ExecutionLease{}, taskErr(TaskScope, op, "der Auftrag %s gehört zum Ziel %q, beauftragt ist %q", d.orderID, order.GoalID, sc.goalID)
	}
	to, ok := orderStartTarget(role, order.State)
	if !ok {
		return domain.ExecutionLease{}, taskErr(TaskState, op, "der Auftrag %s ist %s; die Rolle %s startet aus diesem Zustand nicht", d.orderID, order.State, role)
	}
	for _, dep := range order.DependsOn {
		do, err := tx.WorkOrder(sc.projectID, dep)
		if err != nil {
			return domain.ExecutionLease{}, err
		}
		if do.State != domain.WOCompleted {
			return domain.ExecutionLease{}, taskErr(TaskDependency, op, "der Auftrag %s hängt von %s ab, und der ist %s, nicht abgeschlossen", d.orderID, dep, do.State)
		}
	}
	if l, err := tx.ActiveLease(sc.projectID, d.orderID); err != nil {
		return domain.ExecutionLease{}, err
	} else if l != nil {
		return domain.ExecutionLease{}, taskErr(TaskConflict, op, "der Auftrag %s ist schon durch die Lease %s reserviert", d.orderID, l.LeaseID)
	}
	execs, err := tx.Executions(sc.projectID, d.orderID)
	if err != nil {
		return domain.ExecutionLease{}, err
	}
	for _, e := range execs {
		cur, err := taskLatestAttempt(tx, sc.projectID, e.ExecutionID)
		if err != nil {
			return domain.ExecutionLease{}, err
		}
		if !cur.State.IsTerminal() {
			return domain.ExecutionLease{}, taskErr(TaskConflict, op, "der Auftrag %s hat mit %s#%d einen Versuch, der nicht beendet ist (%s)", d.orderID, e.ExecutionID, cur.Attempt, cur.State)
		}
	}
	if err := taskCapacity(tx, sc.projectID, op); err != nil {
		return domain.ExecutionLease{}, err
	}
	agent, err := taskPickAgent(tx, sc.projectID, op, role, d.profile.ProfileID, pick.agentID)
	if err != nil {
		return domain.ExecutionLease{}, err
	}

	exec := domain.Execution{ExecutionID: taskID("exec", sc, d.decisionID), ProjectID: sc.projectID, WorkOrderID: d.orderID, AgentID: agent.AgentID, Role: role, CreatedAt: sc.at}
	if err := tx.CreateExecution(exec); err != nil {
		return domain.ExecutionLease{}, err
	}
	if err := tx.RecordProfile(sc.projectID, d.profile); err != nil {
		return domain.ExecutionLease{}, err
	}
	if err := tx.StartAttempt(sc.projectID, domain.ExecutionAttempt{ExecutionID: exec.ExecutionID, Attempt: 1, ProfileID: d.profile.ProfileID,
		ProfileVersion: d.profile.Version, State: domain.ExecInitializing, StartedAt: sc.at}); err != nil {
		return domain.ExecutionLease{}, err
	}
	lease, err := tx.CreateLease(domain.ExecutionLease{LeaseID: taskID("lease", sc, d.decisionID), ProjectID: sc.projectID, WorkOrderID: d.orderID,
		ExecutionID: exec.ExecutionID, Attempt: 1, AgentID: agent.AgentID, State: domain.LeaseActive, AcquiredAt: sc.at, ExpiresAt: sc.expires},
		&persistence.LeaseOrigin{BatchID: sc.batchID, DecisionID: d.decisionID})
	if err != nil {
		return domain.ExecutionLease{}, err
	}
	if to != order.State {
		if err := tx.TransitionWorkOrder(sc.projectID, d.orderID, order.State, to); err != nil {
			return domain.ExecutionLease{}, err
		}
	}
	if err := tx.SetAgentAvailability(sc.projectID, agent.AgentID, domain.AgentAvailable, domain.AgentReserved); err != nil {
		return domain.ExecutionLease{}, err
	}
	return lease, nil
}

// taskLatestAttempt liefert den jüngsten Versuch einer Ausführung.
func taskLatestAttempt(tx *persistence.Tx, projectID, execID string) (domain.ExecutionAttempt, error) {
	as, err := tx.Attempts(projectID, execID)
	if err != nil {
		return domain.ExecutionAttempt{}, err
	}
	if len(as) == 0 {
		return domain.ExecutionAttempt{}, &TaskError{Class: TaskNotFound, Op: "attempt", Message: fmt.Sprintf("die Ausführung %q hat keinen Versuch", execID)}
	}
	return as[len(as)-1], nil
}

// taskCapacity prüft, dass ein weiterer Worker-Platz frei ist: aktive Ausführungen ohne Supervisor plus die neue höchstens
// max_workers. Der Supervisor belegt keinen Platz, Reviewer schon.
func taskCapacity(tx *persistence.Tx, projectID, op string) error {
	proj, err := tx.Project(projectID)
	if err != nil {
		return err
	}
	active, err := tx.ActiveExecutions(projectID)
	if err != nil {
		return err
	}
	n := 0
	for _, a := range active {
		if a.Role != domain.RoleSupervisor {
			n++
		}
	}
	if n+1 > proj.Config.MaxWorkers {
		return taskErr(TaskCapacity, op, "max_workers=%d, aktive Ausführungen=%d (der Supervisor zählt nicht mit): kein Platz für eine weitere", proj.Config.MaxWorkers, n)
	}
	return nil
}

// taskProfileAgent liefert den Agenten, den profile_id ("<rolle>:<agent>") nennt, sonst "".
func taskProfileAgent(profileID string, role domain.Role) string {
	return strings.TrimPrefix(profileID, string(role)+":") // ohne Präfix bleibt die ID unverändert
}

// taskPickAgent wählt den Agenten: der genannte (Anfrage oder profile_id), sonst der erste verfügbare der Rolle in
// aufsteigender ID-Reihenfolge. Ein genannter Agent, der nicht passt oder nicht verfügbar ist, wird nie durch einen anderen
// ersetzt.
func taskPickAgent(tx *persistence.Tx, projectID, op string, role domain.Role, profileID, explicit string) (domain.Agent, error) {
	named := ""
	if p := taskProfileAgent(profileID, role); p != profileID {
		named = p
	}
	if named != "" && explicit != "" && named != explicit {
		return domain.Agent{}, taskErr(TaskConflict, op, "das Profil %s nennt den Agenten %q, die Anfrage %q", profileID, named, explicit)
	}
	if named == "" {
		named = explicit
	}
	if named != "" {
		a, err := tx.Agent(projectID, named)
		if err != nil {
			return domain.Agent{}, err
		}
		switch {
		case a.Role != role:
			return domain.Agent{}, taskErr(TaskState, op, "der Agent %s gehört zur Rolle %s, nicht zu %s", a.AgentID, a.Role, role)
		case a.Availability != domain.AgentAvailable:
			return domain.Agent{}, taskErr(TaskUnavailable, op, "der Agent %s ist nicht verfügbar (%s)", a.AgentID, a.Availability)
		}
		return a, nil
	}
	agents, err := tx.Agents(projectID)
	if err != nil {
		return domain.Agent{}, err
	}
	for _, a := range agents { // aufsteigend nach ID
		if a.Role == role && a.Availability == domain.AgentAvailable {
			return a, nil
		}
	}
	return domain.Agent{}, taskErr(TaskUnavailable, op, "es gibt keinen verfügbaren Agenten der Rolle %s", role)
}
