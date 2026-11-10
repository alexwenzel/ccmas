package persistence

import (
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// TestRecordWorkOrderActionIsJournalWithReason: Die Aktion hält Akteur und Grund im Auditverlauf fest, ändert aber für
// sich weder die Zustandsrevision noch den Auftrag.
func TestRecordWorkOrderActionIsJournalWithReason(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	revBefore := revisionOf(t, s, projectID)

	var tx1 UpdateResult
	tx1 = mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordWorkOrderAction(projectID, "wo-1", WorkOrderCancelled, "Umfang entfällt")
	})
	if len(tx1.Revisions) != 0 || revisionOf(t, s, projectID) != revBefore {
		t.Errorf("die Aktion allein ändert die Revision: %v", tx1.Revisions)
	}
	ev := lastEvent(t, s)
	if ev.Kind != domain.AuditWorkOrderCancelled || ev.EntityID != "wo-1" || ev.Level != domain.AuditLevelWorkOrder ||
		ev.Details["reason"] != "Umfang entfällt" || ev.ToState != "cancelled" || ev.Actor != "tester" {
		t.Errorf("Ereignis = %+v", ev)
	}
	mustView(t, s, func(r *ReadTx) error {
		o, err := r.WorkOrder(projectID, "wo-1")
		if err != nil || o.State != domain.WOWaiting {
			t.Errorf("Auftrag verändert: %v %v", o.State, err)
		}
		return nil
	})
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordWorkOrderAction(projectID, "wo-1", WorkOrderResumed, "Blocker behoben")
	})
	if ev := lastEvent(t, s); ev.Kind != domain.AuditWorkOrderResumed || ev.Details["reason"] != "Blocker behoben" {
		t.Errorf("Ereignis = %+v", ev)
	}
}

func TestRecordWorkOrderActionRejectsBadInput(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	before, rev := countEvents(t, s), revisionOf(t, s, projectID)
	for name, tc := range map[string]struct {
		order  string
		action WorkOrderAction
		reason string
		want   Code
	}{
		"Grund fehlt":         {"wo-1", WorkOrderCancelled, "", CodeInvalid},
		"Grund zu lang":       {"wo-1", WorkOrderCancelled, strings.Repeat("x", 513), CodeInvalid},
		"Steuerzeichen":       {"wo-1", WorkOrderResumed, "a\x00b", CodeInvalid},
		"Zeilenumbruch":       {"wo-1", WorkOrderResumed, "a\nb", CodeInvalid},
		"unbekannte Aktion":   {"wo-1", WorkOrderAction("deleted"), "x", CodeInvalid},
		"unbekannter Auftrag": {"wo-404", WorkOrderCancelled, "x", CodeNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				return tx.RecordWorkOrderAction(projectID, tc.order, tc.action, tc.reason)
			})
			wantCode(t, err, tc.want)
		})
	}
	if countEvents(t, s) != before || revisionOf(t, s, projectID) != rev {
		t.Error("ein abgewiesener Aufruf hat Ereignisse oder Revision verändert")
	}
}
