package persistence

import "github.com/alexwenzel/ccmas/internal/domain"

// Aktionen auf einem Arbeitsauftrag, die ein Akteur mit Grund auslöst (CC-42). Der Zustandsübergang selbst
// (TransitionWorkOrder) kennt keinen Grund; die Aktion hält ihn im Auditverlauf fest, damit nachvollziehbar bleibt, WER
// einen Auftrag WARUM abgebrochen oder wieder aufgenommen hat.

// WorkOrderAction nennt die Art der Aktion.
type WorkOrderAction string

const (
	// WorkOrderCancelled: Der Auftrag wurde abgebrochen.
	WorkOrderCancelled WorkOrderAction = "cancelled"
	// WorkOrderResumed: Der blockierte Auftrag wurde zur Wiederaufnahme freigegeben.
	WorkOrderResumed WorkOrderAction = "resumed"
)

var workOrderActionKinds = map[WorkOrderAction]string{
	WorkOrderCancelled: domain.AuditWorkOrderCancelled,
	WorkOrderResumed:   domain.AuditWorkOrderResumed,
}

// maxActionReasonBytes begrenzt den Grund einer Aktion (ein Detailwert des Auditverlaufs).
const maxActionReasonBytes = domain.MaxAuditValueBytes

// RecordWorkOrderAction hält eine Aktion mit Grund im Auditverlauf fest (Journal: ändert für sich die Zustandsrevision
// nicht; der zugehörige Zustandsübergang tut es). Akteur ist der Akteur der Transaktion. Der Auftrag muss existieren, der
// Grund ist Pflicht und kurz, ohne Steuerzeichen.
func (t *Tx) RecordWorkOrderAction(projectID, workOrderID string, action WorkOrderAction, reason string) error {
	return t.write(func() error {
		kind, ok := workOrderActionKinds[action]
		if !ok {
			return invalidf("unbekannte Aktion %q", action)
		}
		if err := checkIDs(projectID, workOrderID); err != nil {
			return err
		}
		if err := checkText("Grund", reason, maxActionReasonBytes, false, true); err != nil {
			return err
		}
		if _, err := t.workOrderRevision(projectID, workOrderID); err != nil {
			return err
		}
		return t.journal(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: kind, entityType: "work_order",
			entityID: workOrderID, to: string(action), details: map[string]string{"reason": reason}})
	})
}
