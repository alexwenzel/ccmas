package supervisor

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// TestOpenWorkIsNeverShortenedUnderBudgetPressure (Befundrunde 2): Befundtexte offener und blockierter Aufträge und die Texte
// offener Fragen werden nie gekürzt. Reicht das Budget danach nicht, ist das ein Kontextfehler, kein stilles Kürzen.
func TestOpenWorkIsNeverShortenedUnderBudgetPressure(t *testing.T) {
	long := func(tag string) string { return tag + " " + strings.Repeat("ausführlich ", 250) } // gut 3000 Zeichen
	findingOf := func(o *Order, summary string) {
		rep := report(o.ID, commitA, "1.0", domain.VerdictFindings, "exec-rev")
		rep.Findings[0].Summary = summary
		o.LastReview = &ReviewRecord{Report: rep, ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl"}
	}
	snap := baseSnapshot()
	openFinding, blockedFinding := long("offener Befund"), long("blockierter Befund")
	findingOf(snap.order(t, "wo-a"), openFinding)
	findingOf(snap.order(t, "wo-block"), blockedFinding)
	closedFinding := long("abgeschlossener Befund")
	findingOf(snap.order(t, "wo-done"), closedFinding)
	openQuestion, answeredQuestion, deliveredQuestion := long("offene Frage"), long("beantwortete Frage"), long("zugestellte Frage")
	snap.OpenQuestions = []OpenQuestion{
		{QuestionID: "q-open", WorkOrderID: "wo-a", State: domain.QuestionOpen, Text: openQuestion},
		{QuestionID: "q-answered", WorkOrderID: "wo-b", State: domain.QuestionAnswered, Text: answeredQuestion},
		{QuestionID: "q-done", WorkOrderID: "wo-b", State: domain.QuestionDelivered, Text: deliveredQuestion},
	}
	decode := func(t *testing.T, pc PlanningContext) ProjectSnapshot {
		t.Helper()
		var got ProjectSnapshot
		if err := json.Unmarshal([]byte(pc.JSON), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	full, err := BuildPlanningContext(snap, 1<<20)
	if err != nil || len(full.Reductions) != 0 {
		t.Fatalf("unverkürzt: %+v, %v", full.Reductions, err)
	}

	// Das Budget zwingt zur Reduktion; abgeschlossene Aufträge werden verkürzt, die Texte offener Arbeit bleiben vollständig.
	pc, err := BuildPlanningContext(snap, full.Bytes-1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Reductions) == 0 {
		t.Fatal("keine Reduktion trotz knappem Budget")
	}
	got := decode(t, pc)
	for id, want := range map[string]string{"wo-a": openFinding, "wo-block": blockedFinding} {
		o := got.order(t, id)
		if o.LastReview == nil || o.LastReview.Report.Findings[0].Summary != want {
			t.Errorf("Befund von %s wurde verändert", id)
		}
	}
	for _, q := range got.OpenQuestions {
		switch q.QuestionID {
		case "q-open":
			if q.Text != openQuestion {
				t.Error("Text der offenen Frage wurde gekürzt")
			}
		case "q-answered":
			if q.Text != answeredQuestion {
				t.Error("Text der beantworteten, noch nicht zugestellten Frage wurde gekürzt")
			}
		}
	}
	if o := got.order(t, "wo-done"); o.LastReview != nil {
		t.Errorf("abgeschlossener Auftrag nicht verkürzt: %+v", o)
	}

	// Wird es enger, darf nur gekürzt werden, was nicht offen ist: der Text einer zugestellten Frage. Die offene Arbeit bleibt
	// auch dann vollständig (die Regeln für Befund- und Fragetexte offener Arbeit gibt es nicht mehr).
	tighter, err := BuildPlanningContext(snap, pc.Bytes-500)
	if err != nil {
		t.Fatalf("mit gekürzter zugestellter Frage müsste es passen: %v", err)
	}
	if slices.Contains(tighter.Reductions, "finding_texts_shortened") {
		t.Errorf("Reduktionen = %v", tighter.Reductions)
	}
	for _, q := range decode(t, tighter).OpenQuestions {
		switch q.QuestionID {
		case "q-done":
			if len(q.Text) >= len(deliveredQuestion) || !strings.HasSuffix(q.Text, "…") {
				t.Errorf("zugestellte Frage nicht gekürzt: %d Zeichen", len(q.Text))
			}
		case "q-open":
			if q.Text != openQuestion {
				t.Error("Text der offenen Frage wurde gekürzt")
			}
		}
	}
	for id, want := range map[string]string{"wo-a": openFinding, "wo-block": blockedFinding} {
		gotTighter := decode(t, tighter)
		if o := gotTighter.order(t, id); o.LastReview.Report.Findings[0].Summary != want {
			t.Errorf("Befund von %s wurde gekürzt", id)
		}
	}

	// Passen die offenen Texte allein nicht ins Budget, ist das ein Kontextfehler, kein stilles Kürzen.
	openTexts := len(openFinding) + len(blockedFinding) + len(openQuestion) + len(answeredQuestion)
	_, err = BuildPlanningContext(snap, openTexts-1000)
	var ce *ContextError
	if !errors.Is(err, ErrContextTooLarge) || !errors.As(err, &ce) || ce.OpenOrders == 0 {
		t.Fatalf("err = %v: statt offene Texte zu kürzen muss ein Kontextfehler kommen", err)
	}
}
