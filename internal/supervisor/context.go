package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// DefaultContextLimit ist das Kontextbudget des Planungszustands in Bytes (etwa 64 KiB).
const DefaultContextLimit = 64 << 10

// Reduktionsregeln des Planungszustands (in dieser Reihenfolge, jede nur, solange der Zustand das Budget überschreitet).
// Offene und blockierte Aufträge werden nie weggelassen und nie verkürzt (auch ihre Befundtexte nicht), ebenso nicht die Texte
// offener Fragen; die Aufträge selbst werden nie entfernt: Abhängigkeiten verweisen auf sie. Verkürzt werden darf nur, was keine
// laufende Entscheidung trägt: abgeschlossene und abgebrochene Aufträge, die Skill-Liste und Texte bereits erledigter Fragen.
const (
	// ReduceSkillDescriptions: die Beschreibungen der Skill-Liste entfallen.
	ReduceSkillDescriptions = "skill_descriptions_dropped"
	// ReduceClosedOrders: abgeschlossene und abgebrochene Aufträge bleiben als Kurzform (ID, Art, Titel, Zustand, Modus,
	// Abhängigkeiten); Konzeptstand, Review, Ergebnis, PR und Commit entfallen.
	ReduceClosedOrders = "closed_orders_compacted"
	// ReduceSkillList: die Skill-Liste entfällt (die Rollenangebote nennen ihre erlaubten Skills weiterhin).
	ReduceSkillList = "skill_list_dropped"
	// ReduceQuestionTexts: Texte erledigter Fragen (zugestellt, ersetzt, abgebrochen) werden auf 200 Zeichen gekürzt. Die
	// Texte offener und beantworteter, noch nicht zugestellter Fragen bleiben vollständig: keine Antwort ist keine Freigabe, und
	// die Frage hält den betroffenen Auftrag an.
	ReduceQuestionTexts = "question_texts_shortened"
)

// ErrContextTooLarge: Der Zustand passt auch nach allen Reduktionsregeln nicht in das Budget. Es wird nichts still
// weggelassen; die Planung setzt aus, bis das Budget angepasst oder der Zustand kleiner ist.
var ErrContextTooLarge = errors.New("Planungszustand überschreitet das Kontextbudget")

// ContextError beschreibt, warum der Zustand nicht passt.
type ContextError struct {
	Bytes, Limit int
	OpenOrders   int // offene und blockierte Aufträge, die unverkürzt bleiben müssen
	Reductions   []string
}

func (e *ContextError) Error() string {
	return fmt.Sprintf("%v: %d Bytes nach den Reduktionsregeln %v, Budget %d Bytes; %d offene oder blockierte Aufträge dürfen nicht weggelassen werden",
		ErrContextTooLarge, e.Bytes, e.Reductions, e.Limit, e.OpenOrders)
}

func (e *ContextError) Is(target error) bool { return target == ErrContextTooLarge }

// PlanningContext ist der dem Modell gezeigte Zustand.
type PlanningContext struct {
	JSON       string   // kompakt, deterministisch
	Bytes      int      // len(JSON)
	Reductions []string // angewandte Reduktionsregeln in Anwendungsreihenfolge; leer: unverkürzt
}

// questionPending: Die Frage ist offen oder beantwortet, aber noch nicht zugestellt, und hält ihren Auftrag an.
func questionPending(q OpenQuestion) bool {
	return q.State == domain.QuestionOpen || q.State == domain.QuestionAnswered
}

func closedOrder(o Order) bool { return o.State == domain.WOCompleted || o.State == domain.WOCancelled }

// BuildPlanningContext stellt den Zustand für das Modell zusammen: deterministisch (gleicher Snapshot, gleiche Bytes) und
// begrenzt auf limit Bytes (0: DefaultContextLimit). Passt der kanonisch serialisierte Snapshot nicht, greifen die
// Reduktionsregeln (ReduceSkillDescriptions, ReduceClosedOrders, ReduceSkillList, ReduceQuestionTexts) der Reihe nach, jede
// nur, solange das Budget überschritten ist. Offene und blockierte Aufträge (mit ihren Befundtexten) und offene Fragen bleiben
// stets unverkürzt; reicht es danach nicht, ist das ein *ContextError (errors.Is(err, ErrContextTooLarge)), nie ein stilles
// Weglassen oder Kürzen. Befundtexte abgeschlossener Aufträge entfallen mit deren Kurzform.
//
// Der Snapshot des Aufrufers bleibt unverändert; die Reduktion wirkt auf einer tiefen Kopie. Der Validator prüft immer gegen
// den vollständigen Snapshot, nicht gegen den reduzierten Zustand.
func BuildPlanningContext(snap ProjectSnapshot, limit int) (PlanningContext, error) {
	if limit <= 0 {
		limit = DefaultContextLimit
	}
	work := snap.Canonical()
	render := func() (string, error) {
		raw, err := json.Marshal(work)
		return string(raw), err
	}
	text, err := render()
	if err != nil {
		return PlanningContext{}, err
	}
	var applied []string
	steps := []struct {
		name string
		do   func()
	}{
		{ReduceSkillDescriptions, func() {
			for i := range work.Skills {
				work.Skills[i].Description = ""
			}
		}},
		{ReduceClosedOrders, func() {
			for i, o := range work.Orders {
				if closedOrder(o) {
					work.Orders[i] = Order{ID: o.ID, Kind: o.Kind, Title: o.Title, State: o.State, Mode: o.Mode, DependsOn: o.DependsOn}
				}
			}
		}},
		{ReduceSkillList, func() { work.Skills = []SkillOffer{} }},
		{ReduceQuestionTexts, func() {
			for i, q := range work.OpenQuestions {
				if !questionPending(q) {
					work.OpenQuestions[i].Text = shorten(q.Text, 200)
				}
			}
		}},
	}
	for _, st := range steps {
		if len(text) <= limit {
			break
		}
		st.do()
		applied = append(applied, st.name)
		if text, err = render(); err != nil {
			return PlanningContext{}, err
		}
	}
	if len(text) > limit {
		open := 0
		for _, o := range work.Orders {
			if !closedOrder(o) {
				open++
			}
		}
		return PlanningContext{}, &ContextError{Bytes: len(text), Limit: limit, OpenOrders: open, Reductions: applied}
	}
	return PlanningContext{JSON: text, Bytes: len(text), Reductions: nonNil(applied)}, nil
}

// shorten kürzt auf n Zeichen (Runen) und kennzeichnet die Kürzung.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
