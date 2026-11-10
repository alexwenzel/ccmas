package persistence

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

const maxQuestionText = 16 << 10

// asState macht aus einem Vertragsverstoß der Klasse state einen Fehler mit dem Code state; andere Verstöße
// bleiben invalid.
func asState(err error) error {
	var ce *domain.ContractError
	if errors.As(err, &ce) && ce.Category() == domain.CatState {
		return &Error{Code: CodeState, Err: err}
	}
	return invalid(err)
}

// CreateQuestion legt eine offene Frage eines Versuchs an. Frage, Ausführung und Versuch gehören zum selben
// Auftrag. Das Anlegen der Frage ändert den Ausführungszustand nicht (eigene Ebene).
func (t *Tx) CreateQuestion(projectID string, q domain.Question) error {
	return t.write(func() error {
		if err := checkIDs(projectID, q.QuestionID, q.WorkOrderID, q.ExecutionID); err != nil {
			return err
		}
		switch {
		case q.Attempt < 1:
			return invalidf("Versuch %d ungültig", q.Attempt)
		case q.State != domain.QuestionOpen:
			return invalidf("eine neue Frage ist offen, nicht %q", q.State)
		case q.AnswerID != "" || !q.DeliveryConfirmedAt.IsZero():
			return invalidf("eine neue Frage hat weder Antwort noch Zustellbestätigung")
		}
		for _, f := range []struct {
			name, v  string
			required bool
		}{{"Frage", q.Text, true}, {"Kontext", q.Context, true}, {"Empfehlung", q.Recommendation, false}} {
			if err := checkText(f.name, f.v, maxQuestionText, true, f.required); err != nil {
				return err
			}
		}
		exec, err := t.Execution(projectID, q.ExecutionID)
		if err != nil {
			return err
		}
		if _, err := t.Attempt(projectID, q.ExecutionID, q.Attempt); err != nil {
			return err
		}
		if exec.WorkOrderID != q.WorkOrderID {
			return invalidf("die Ausführung %q gehört zu Auftrag %q, die Frage zu %q", q.ExecutionID, exec.WorkOrderID, q.WorkOrderID)
		}
		created := q.CreatedAt
		if created.IsZero() {
			created = t.at
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO questions (question_id, project_id, work_order_id, execution_id, attempt, text, context,
			recommendation, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)`, q.QuestionID, projectID, q.WorkOrderID, q.ExecutionID,
			q.Attempt, q.Text, q.Context, q.Recommendation, fmtTime(created)); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelQuestion, kind: domain.AuditQuestionCreated, entityType: "question",
			entityID: q.QuestionID, to: string(domain.QuestionOpen),
			details: map[string]string{"work_order_id": q.WorkOrderID, "execution": attemptEntity(q.ExecutionID, q.Attempt)}})
	})
}

// AnswerQuestion ordnet der offenen Frage ihre Antwort zu (offen -> beantwortet). Eine zweite Antwort oder eine
// Antwort auf eine nicht offene Frage ist ein Fehler der Klasse state; die Zustellbestätigung entsteht erst
// danach (ConfirmQuestionDelivery). Der Akteur der Transaktion wird an der Antwort festgehalten.
func (t *Tx) AnswerQuestion(projectID string, a domain.Answer) error {
	return t.write(func() error {
		if err := checkIDs(projectID, a.AnswerID, a.QuestionID); err != nil {
			return err
		}
		if a.DeliveryConfirmedAt != nil {
			return invalidf("die Zustellbestätigung entsteht erst nach dem Senden")
		}
		if err := checkText("Antwort", a.Text, maxQuestionText, true, true); err != nil {
			return err
		}
		q, err := t.Question(projectID, a.QuestionID)
		if err != nil {
			return err
		}
		if err := q.Accept(a); err != nil {
			return asState(err)
		}
		created := a.CreatedAt
		if created.IsZero() {
			created = t.at
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "INSERT INTO answers (answer_id, project_id, question_id, text, created_at, actor) VALUES (?, ?, ?, ?, ?, ?)",
			a.AnswerID, projectID, a.QuestionID, a.Text, fmtTime(created), t.actor); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE questions SET state = ?, answer_id = ? WHERE project_id = ? AND question_id = ?",
			string(domain.QuestionAnswered), a.AnswerID, projectID, a.QuestionID); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelQuestion, kind: domain.AuditQuestionAnswered, entityType: "question",
			entityID: a.QuestionID, from: string(domain.QuestionOpen), to: string(domain.QuestionAnswered),
			details: map[string]string{"answer_id": a.AnswerID}})
	})
}

// ConfirmQuestionDelivery hält die bestätigte Zustellung der Antwort fest (beantwortet -> zugestellt). Aus „offen“
// gibt es keinen Weg dorthin: keine Antwort ist keine Zustellung. Leere Zeit: Zeit der Transaktion.
func (t *Tx) ConfirmQuestionDelivery(projectID, questionID string, at time.Time) error {
	return t.write(func() error {
		if err := checkIDs(projectID, questionID); err != nil {
			return err
		}
		q, err := t.Question(projectID, questionID)
		if err != nil {
			return err
		}
		if at.IsZero() {
			at = t.at
		}
		if err := q.ConfirmDelivery(at); err != nil {
			return asState(err)
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE questions SET state = ?, delivery_confirmed_at = ? WHERE project_id = ? AND question_id = ?",
			string(domain.QuestionDelivered), fmtTime(at), projectID, questionID); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelQuestion, kind: domain.AuditQuestionDelivered, entityType: "question",
			entityID: questionID, from: string(domain.QuestionAnswered), to: string(domain.QuestionDelivered)})
	})
}

// CloseQuestion beendet eine offene oder beantwortete Frage ohne Zustellung (superseded oder cancelled).
func (t *Tx) CloseQuestion(projectID, questionID string, from, to domain.QuestionState) error {
	return t.write(func() error {
		if err := checkIDs(projectID, questionID); err != nil {
			return err
		}
		if to != domain.QuestionSuperseded && to != domain.QuestionCancelled {
			return invalidf("eine Frage wird mit superseded oder cancelled geschlossen, nicht mit %q", to)
		}
		if !from.CanTransition(to) {
			return stateErr("Fragezustand %q -> %q ist nicht erlaubt", from, to)
		}
		q, err := t.Question(projectID, questionID)
		if err != nil {
			return err
		}
		if q.State != from {
			return errf(CodeConflict, "Frage %q ist %q, erwartet war %q", questionID, q.State, from)
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE questions SET state = ? WHERE project_id = ? AND question_id = ?", string(to), projectID, questionID); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelQuestion, kind: domain.AuditQuestionStateChanged, entityType: "question",
			entityID: questionID, from: string(from), to: string(to)})
	})
}

const questionColumns = "question_id, work_order_id, execution_id, attempt, text, context, recommendation, state, created_at, answer_id, delivery_confirmed_at"

func scanQuestion(sc interface{ Scan(...any) error }) (domain.Question, error) {
	var (
		q                    domain.Question
		state, created       string
		answer, confirmedStr sql.NullString
	)
	if err := sc.Scan(&q.QuestionID, &q.WorkOrderID, &q.ExecutionID, &q.Attempt, &q.Text, &q.Context, &q.Recommendation, &state, &created, &answer, &confirmedStr); err != nil {
		return q, err
	}
	q.State, q.AnswerID = domain.QuestionState(state), str(answer)
	var err error
	if q.CreatedAt, err = scanTime(created); err != nil {
		return q, err
	}
	q.DeliveryConfirmedAt, err = scanNullTime(confirmedStr)
	return q, err
}

// Question liefert eine Frage.
func (r *reader) Question(projectID, questionID string) (domain.Question, error) {
	q, err := scanQuestion(r.q.QueryRowContext(r.ctx, "SELECT "+questionColumns+" FROM questions WHERE project_id = ? AND question_id = ?", projectID, questionID))
	if errors.Is(err, sql.ErrNoRows) {
		return q, errf(CodeNotFound, "Frage %q existiert nicht", questionID)
	}
	return q, mapErr(err)
}

// Questions liefert die Fragen des Projekts nach Erstellung, optional nur in den genannten Zuständen.
func (r *reader) Questions(projectID string, states ...domain.QuestionState) ([]domain.Question, error) {
	query := "SELECT " + questionColumns + " FROM questions WHERE project_id = ?"
	args := []any{projectID}
	if len(states) > 0 {
		marks := make([]string, len(states))
		for i, s := range states {
			marks[i] = "?"
			args = append(args, string(s))
		}
		query += " AND state IN (" + strings.Join(marks, ",") + ")"
	}
	rows, err := r.q.QueryContext(r.ctx, query+" ORDER BY created_at, question_id", args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Question
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, q)
	}
	return out, mapErr(rows.Err())
}

// OpenQuestions liefert die noch nicht zugestellten Fragen (offen oder beantwortet) in der Form des Snapshots.
func (r *reader) OpenQuestions(projectID string) ([]supervisor.OpenQuestion, error) {
	qs, err := r.Questions(projectID, domain.QuestionOpen, domain.QuestionAnswered)
	if err != nil {
		return nil, err
	}
	var out []supervisor.OpenQuestion
	for _, q := range qs {
		out = append(out, supervisor.OpenQuestion{QuestionID: q.QuestionID, WorkOrderID: q.WorkOrderID, State: q.State, Text: q.Text})
	}
	return out, nil
}

// Answer liefert die Antwort einer Frage; DeliveryConfirmedAt kommt von der Frage.
func (r *reader) Answer(projectID, questionID string) (domain.Answer, error) {
	var (
		a       domain.Answer
		created string
		conf    sql.NullString
	)
	err := r.q.QueryRowContext(r.ctx, `SELECT a.answer_id, a.question_id, a.text, a.created_at, q.delivery_confirmed_at FROM answers a
		JOIN questions q ON q.question_id = a.question_id WHERE a.project_id = ? AND a.question_id = ?`, projectID, questionID).
		Scan(&a.AnswerID, &a.QuestionID, &a.Text, &created, &conf)
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(CodeNotFound, "Frage %q hat keine Antwort", questionID)
	}
	if err != nil {
		return a, mapErr(err)
	}
	if a.CreatedAt, err = scanTime(created); err != nil {
		return a, err
	}
	if conf.Valid {
		at, err := scanTime(conf.String)
		if err != nil {
			return a, err
		}
		a.DeliveryConfirmedAt = &at
	}
	return a, nil
}
