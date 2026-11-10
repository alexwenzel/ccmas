package domain

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// TestLeaseTransitions: aus „aktiv“ führt jeder Weg in genau einen Endzustand; Endzustände haben keinen Ausgang, und
// ein Zustand geht nie in sich selbst über. Die Tabelle steht hier ausgeschrieben, nicht aus der Implementierung abgeleitet.
func TestLeaseTransitions(t *testing.T) {
	allowed := map[[2]LeaseState]bool{
		{LeaseActive, LeaseReleased}: true,
		{LeaseActive, LeaseExpired}:  true,
		{LeaseActive, LeaseRevoked}:  true,
	}
	for _, from := range LeaseStates() {
		for _, to := range LeaseStates() {
			want := allowed[[2]LeaseState{from, to}]
			if got := from.CanTransition(to); got != want {
				t.Errorf("%s -> %s: CanTransition = %v, erwartet %v", from, to, got, want)
			}
		}
	}
	if LeaseState("bogus").CanTransition(LeaseReleased) || LeaseActive.CanTransition("bogus") {
		t.Error("ein unbekannter Zustand hat keinen Übergang")
	}
}

func TestLeaseIsTerminal(t *testing.T) {
	want := map[LeaseState]bool{LeaseActive: false, LeaseReleased: true, LeaseExpired: true, LeaseRevoked: true}
	for _, s := range LeaseStates() {
		if got := s.IsTerminal(); got != want[s] {
			t.Errorf("%s.IsTerminal() = %v, erwartet %v", s, got, want[s])
		}
	}
	if LeaseState("bogus").IsTerminal() {
		t.Error("ein unbekannter Zustand ist kein Endzustand")
	}
	// Konsistenz: Endzustand heißt, es gibt keinen Übergang hinaus.
	for _, from := range LeaseStates() {
		out := false
		for _, to := range LeaseStates() {
			out = out || from.CanTransition(to)
		}
		if out == from.IsTerminal() {
			t.Errorf("%s: Übergänge hinaus = %v, Endzustand = %v", from, out, from.IsTerminal())
		}
	}
}

func validLease() ExecutionLease {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	return ExecutionLease{
		LeaseID: "lease-1", ProjectID: "proj-1", WorkOrderID: "wo-1", AgentID: "agent-1", Generation: 1, State: LeaseActive,
		AcquiredAt: at, ExpiresAt: at.Add(time.Hour),
	}
}

func TestExecutionLeaseCheck(t *testing.T) {
	ok := validLease()
	if err := ok.Check(); err != nil {
		t.Fatalf("gültige Lease: %v", err)
	}
	bound := validLease()
	bound.ExecutionID, bound.Attempt, bound.WorktreePath = "exec-1", 2, "/work/wt/wo-1"
	if err := bound.Check(); err != nil {
		t.Fatalf("an einen Versuch und Worktree gebundene Lease: %v", err)
	}
	ended := validLease()
	ended.State, ended.ReleasedAt = LeaseReleased, ended.AcquiredAt.Add(time.Minute)
	if err := ended.Check(); err != nil {
		t.Fatalf("freigegebene Lease: %v", err)
	}

	cases := map[string]struct {
		mut  func(*ExecutionLease)
		want Category
	}{
		"Lease-ID leer":                      {func(l *ExecutionLease) { l.LeaseID = "" }, CatID},
		"Projekt-ID ungültig":                {func(l *ExecutionLease) { l.ProjectID = "a b" }, CatID},
		"Auftrags-ID leer":                   {func(l *ExecutionLease) { l.WorkOrderID = "" }, CatID},
		"Agenten-ID leer":                    {func(l *ExecutionLease) { l.AgentID = "" }, CatID},
		"Ausführungs-ID ungültig":            {func(l *ExecutionLease) { l.ExecutionID, l.Attempt = "x y", 1 }, CatID},
		"Versuch ohne Ausführung":            {func(l *ExecutionLease) { l.Attempt = 1 }, CatValue},
		"Ausführung ohne Versuch":            {func(l *ExecutionLease) { l.ExecutionID = "exec-1" }, CatValue},
		"Versuch 0 mit Ausführung":           {func(l *ExecutionLease) { l.ExecutionID, l.Attempt = "exec-1", 0 }, CatValue},
		"Generation 0":                       {func(l *ExecutionLease) { l.Generation = 0 }, CatValue},
		"Generation negativ":                 {func(l *ExecutionLease) { l.Generation = -1 }, CatValue},
		"unbekannter Zustand":                {func(l *ExecutionLease) { l.State = "bogus" }, CatState},
		"Beginn fehlt":                       {func(l *ExecutionLease) { l.AcquiredAt = time.Time{} }, CatTimestamp},
		"Ablauf fehlt":                       {func(l *ExecutionLease) { l.ExpiresAt = time.Time{} }, CatTimestamp},
		"Ablauf nicht nach Beginn":           {func(l *ExecutionLease) { l.ExpiresAt = l.AcquiredAt }, CatTimestamp},
		"Ablauf vor Beginn":                  {func(l *ExecutionLease) { l.ExpiresAt = l.AcquiredAt.Add(-time.Second) }, CatTimestamp},
		"aktive Lease mit Endzeit":           {func(l *ExecutionLease) { l.ReleasedAt = l.AcquiredAt.Add(time.Second) }, CatTimestamp},
		"beendete Lease ohne Endzeit":        {func(l *ExecutionLease) { l.State = LeaseRevoked }, CatTimestamp},
		"Endzeit vor Beginn":                 {func(l *ExecutionLease) { l.State, l.ReleasedAt = LeaseExpired, l.AcquiredAt.Add(-time.Second) }, CatTimestamp},
		"relativer Worktree":                 {func(l *ExecutionLease) { l.WorktreePath = "wt/wo-1" }, CatPath},
		"Worktree nicht kanonisch":           {func(l *ExecutionLease) { l.WorktreePath = "/work/../wt" }, CatPath},
		"Worktree mit abschließendem Strich": {func(l *ExecutionLease) { l.WorktreePath = "/work/wt/" }, CatPath},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			l := validLease()
			c.mut(&l)
			wantCategory(t, l.Check(), c.want)
		})
	}
}

// TestExecutionLeaseJSON: stabile JSON-Namen; die optionalen Felder fehlen, wenn sie leer sind.
func TestExecutionLeaseJSON(t *testing.T) {
	raw, err := json.Marshal(validLease())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"lease_id", "project_id", "work_order_id", "agent_id", "generation", "state", "acquired_at", "expires_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Feld %q fehlt: %s", k, raw)
		}
	}
	for _, k := range []string{"execution_id", "attempt", "worktree_path", "released_at"} {
		if _, ok := m[k]; ok {
			t.Errorf("leeres Feld %q wird ausgegeben: %s", k, raw)
		}
	}
}

// TestLeaseAuditKindsAreWellFormed: die Ereignisarten der Reservierung haben die Form gegenstand.ereignis, sind
// eindeutig, kommen in AuditKinds genau einmal vor und kollidieren nicht mit den Arten der übrigen Ebenen.
func TestLeaseAuditKindsAreWellFormed(t *testing.T) {
	kinds := LeaseAuditKinds()
	if len(kinds) == 0 {
		t.Fatal("keine Ereignisarten")
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if !auditKindPattern.MatchString(k) || seen[k] {
			t.Errorf("Ereignisart %q ungültig oder doppelt", k)
		}
		seen[k] = true
		if n := slices.Index(AuditKinds(), k); n < 0 || slices.Index(AuditKinds()[n+1:], k) >= 0 {
			t.Errorf("Ereignisart %q fehlt in AuditKinds oder steht dort mehrfach", k)
		}
	}
}
