package domain

import (
	"strings"
	"testing"
	"time"
)

func validEvent() AuditEvent {
	return AuditEvent{
		ProjectID: "demo", OccurredAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), Actor: "orchestrator",
		Kind: AuditWorkOrderStateChanged, EntityType: "work_order", EntityID: "wo-1", Level: AuditLevelWorkOrder,
		FromState: "waiting", ToState: "in_progress", StateRevision: 3,
		Details: map[string]string{"plan_version": "2"},
	}
}

func TestAuditEventCheck(t *testing.T) {
	if err := validEvent().Check(); err != nil {
		t.Fatalf("gültiges Ereignis abgewiesen: %v", err)
	}
	cases := map[string]struct {
		mutate func(e *AuditEvent)
		want   Category
	}{
		"Projekt":            {func(e *AuditEvent) { e.ProjectID = "" }, CatID},
		"Zeit":               {func(e *AuditEvent) { e.OccurredAt = time.Time{} }, CatTimestamp},
		"Akteur":             {func(e *AuditEvent) { e.Actor = "a b" }, CatID},
		"Art leer":           {func(e *AuditEvent) { e.Kind = "" }, CatValue},
		"Art in Großschrift": {func(e *AuditEvent) { e.Kind = "WorkOrder.Changed" }, CatValue},
		"Art ohne Punkt":     {func(e *AuditEvent) { e.Kind = "changed" }, CatValue},
		"Entität leer":       {func(e *AuditEvent) { e.EntityType = "" }, CatValue},
		"Entitäts-ID leer":   {func(e *AuditEvent) { e.EntityID = "" }, CatID},
		"Entitäts-ID Steuer": {func(e *AuditEvent) { e.EntityID = "a\nb" }, CatID},
		"Ebene unbekannt":    {func(e *AuditEvent) { e.Level = "misc" }, CatValue},
		"Zustand ungültig":   {func(e *AuditEvent) { e.FromState = "Wartet Auf" }, CatState},
		"Revision negativ":   {func(e *AuditEvent) { e.StateRevision = -1 }, CatRevision},
		"Detailschlüssel":    {func(e *AuditEvent) { e.Details = map[string]string{"Bad Key": "x"} }, CatValue},
		"Detailwert Steuer":  {func(e *AuditEvent) { e.Details = map[string]string{"k": "a\x00"} }, CatValue},
		"Detailwert zu lang": {func(e *AuditEvent) { e.Details = map[string]string{"k": strings.Repeat("x", MaxAuditValueBytes+1)} }, CatSize},
		"Details zu groß": {func(e *AuditEvent) {
			e.Details = map[string]string{}
			for i := 0; i < 20; i++ {
				e.Details["k"+string(rune('a'+i))] = strings.Repeat("x", 400)
			}
		}, CatSize},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			tc.mutate(&e)
			wantCategory(t, e.Check(), tc.want)
		})
	}
}

func TestAuditLevelsAndKindsAreWellFormed(t *testing.T) {
	seen := map[AuditLevel]bool{}
	for _, l := range AuditLevels() {
		if !l.Valid() || seen[l] {
			t.Errorf("Ebene %q ungültig oder doppelt", l)
		}
		seen[l] = true
	}
	if AuditLevel("misc").Valid() {
		t.Error("unbekannte Ebene gilt als gültig")
	}
	kinds := map[string]bool{}
	for _, k := range AuditKinds() {
		if !auditKindPattern.MatchString(k) || kinds[k] {
			t.Errorf("Ereignisart %q ungültig oder doppelt", k)
		}
		kinds[k] = true
	}
}

// TestAuditDetailsEncodeDeterministically: gleiche Details ergeben dieselben Bytes (Prüfsumme, Vergleich).
func TestAuditDetailsEncodeDeterministically(t *testing.T) {
	a := AuditEvent{Details: map[string]string{"b": "2", "a": "1"}}
	b := AuditEvent{Details: map[string]string{"a": "1", "b": "2"}}
	ja, err := a.DetailsJSON()
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := b.DetailsJSON()
	if string(ja) != string(jb) || string(ja) != `{"a":"1","b":"2"}` {
		t.Fatalf("Details = %s / %s", ja, jb)
	}
	empty, _ := AuditEvent{}.DetailsJSON()
	if string(empty) != `{}` {
		t.Fatalf("leere Details = %s", empty)
	}
}
