package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
	"github.com/alexwenzel/ccmas/prompts"
)

// ---- Erfolg -------------------------------------------------------------------------------------------------

func TestPlannerAcceptsAValidPlanAndPassesEverythingToTheRuntime(t *testing.T) {
	e := newPlannerEnv(t)
	var cwdExisted bool
	var cwdEmpty bool
	e.rt.steps = []runStep{func(req runner.Request) (runner.Execution, error) {
		fi, err := os.Stat(req.Cwd)
		cwdExisted = err == nil && fi.IsDir()
		if entries, err := os.ReadDir(req.Cwd); err == nil {
			cwdEmpty = len(entries) == 0
		}
		return answer(goodPlan(t))(req)
	}}
	snap := e.snapshot()
	res, err := e.planner().PlanWithRecord(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if e.rt.calls() != 1 || len(res.Attempts) != 1 || res.Attempts[0].Verdict != VerdictAccepted {
		t.Fatalf("Aufrufe %d, Versuche %+v", e.rt.calls(), res.Attempts)
	}
	b := res.Batch
	if b.ProjectID != snap.ProjectID || b.StateRevision != snap.StateRevision || b.SchemaVersion != 1 || b.BatchID == "" || len(b.Decisions) != 2 {
		t.Fatalf("Batch = %+v", b)
	}
	for _, d := range b.Decisions {
		if d.ProjectID != snap.ProjectID || d.StateRevision != snap.StateRevision || d.SchemaVersion != 1 {
			t.Errorf("Entscheidung %s ohne Bezug aus dem Snapshot: %+v", d.DecisionID, d)
		}
	}
	if len(res.Validated.Delegations()) != 2 {
		t.Errorf("validierter Batch = %+v", res.Validated)
	}
	// Plan (Ticketvertrag) liefert denselben Batch.
	e.rt.steps, e.rt.reqs = []runStep{answer(goodPlan(t))}, nil
	e.src.calls = 0
	again, err := e.planner().Plan(context.Background(), snap)
	if err != nil || again.StateRevision != snap.StateRevision || len(again.Decisions) != 2 {
		t.Fatalf("Plan = %+v, %v", again, err)
	}

	req := e.rt.reqs[0]
	if req.ProjectID != snap.ProjectID || req.WorkOrderID != snap.Goal.ID || req.Profile.Role != domain.RoleSupervisor {
		t.Errorf("Anfrage = %+v", req)
	}
	// Readonly, neutrales temporäres Verzeichnis (existierte während des Laufs, leer, danach entfernt), kleine Grenzen.
	if req.Profile.PolicyProfile != "readonly" || !cwdExisted || !cwdEmpty {
		t.Errorf("Policy %q, cwd vorhanden %v, leer %v", req.Profile.PolicyProfile, cwdExisted, cwdEmpty)
	}
	if _, err := os.Stat(req.Cwd); !os.IsNotExist(err) {
		t.Errorf("das temporäre Verzeichnis %s wurde nicht entfernt", req.Cwd)
	}
	if strings.HasPrefix(req.Cwd, repoRoot) || req.MaxTurns != DefaultMaxTurns || req.MaxRuntimeSeconds != int(DefaultMaxRuntime/time.Second) {
		t.Errorf("cwd %q, MaxTurns %d, Laufzeit %d s", req.Cwd, req.MaxTurns, req.MaxRuntimeSeconds)
	}
	// Systemprompt aus prompts/ und das Modellschema.
	if req.SystemPrompt != prompts.Supervisor() || string(req.OutputSchema) != string(ModelOutputSchema()) {
		t.Error("Systemprompt oder Ausgabeschema weichen ab")
	}
	// Skill-Snapshot: gesetzt, geprüft, mit den Skills der Rolle supervisor; der Prompt trägt ihren Inhalt aus dem Snapshot.
	if req.SkillSnapshot == nil {
		t.Fatal("RunSpec.skill_snapshot fehlt")
	}
	manifest, err := skills.VerifySnapshotDir(req.SkillSnapshot.Path)
	if err != nil {
		t.Fatalf("Snapshot nicht gültig: %v", err)
	}
	if manifest.SnapshotID != req.SkillSnapshot.SnapshotID || manifest.Role != domain.RoleSupervisor {
		t.Errorf("snapshot.json = %s/%s", manifest.SnapshotID, manifest.Role)
	}
	var ids []string
	for _, s := range manifest.Skills {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"ccmas-supervisor", "ccmas-work-assessment"}) || !slices.Equal(req.Profile.SkillIDs, ids) {
		t.Errorf("Skills im Snapshot %v, im Profil %v", ids, req.Profile.SkillIDs)
	}
	body := skillBody(string(mustFile(t, req.SkillSnapshot.Path, "ccmas-supervisor")))
	if !strings.Contains(req.Prompt, body) || !strings.Contains(req.Prompt, "## Skill ccmas-supervisor (Version 1, tree_sha256 ") {
		t.Error("der Prompt trägt den Inhalt des führenden Skills aus dem Snapshot nicht")
	}
	// Der Zustand steht als JSON im Prompt, kanonisch und vollständig.
	pc, _ := BuildPlanningContext(snap, 0)
	if !strings.Contains(req.Prompt, "```json\n"+pc.JSON+"\n```") || !strings.Contains(req.Prompt, "Zustandsrevision: 7") {
		t.Error("der Prompt trägt den Planungszustand nicht")
	}
}

func mustFile(t *testing.T, snapPath, skill string) []byte {
	t.Helper()
	raw, err := os.ReadFile(snapPath + "/skills/" + skill + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ---- Go bestimmt Projekt und Revision ---------------------------------------------------------------------------

func TestGoOverwritesProjectAndRevisionFromTheModelOutput(t *testing.T) {
	e := newPlannerEnv(t)
	// Das Modell nennt ein fremdes Projekt und eine erfundene Revision (Batch und Entscheidungen).
	var m map[string]any
	_ = json.Unmarshal([]byte(goodPlan(t)), &m)
	m["project_id"], m["state_revision"], m["batch_id"] = "fremd", 99, "mein-batch"
	for _, d := range m["decisions"].([]any) {
		d.(map[string]any)["project_id"], d.(map[string]any)["state_revision"] = "fremd", 99
	}
	raw, _ := json.Marshal(m)
	e.rt.steps = []runStep{answer(string(raw))}
	res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
	if err != nil {
		t.Fatalf("das Modell darf den Bezug nicht bestimmen, Go überschreibt ihn: %v", err)
	}
	if res.Batch.ProjectID != "ccmas" || res.Batch.StateRevision != 7 || res.Batch.BatchID == "mein-batch" {
		t.Errorf("Batch = %+v", res.Batch)
	}
	for _, d := range res.Batch.Decisions {
		if d.ProjectID != "ccmas" || d.StateRevision != 7 {
			t.Errorf("Entscheidung = %+v", d)
		}
	}
}

// ---- Neuplanung ---------------------------------------------------------------------------------------------------

func TestPlannerReplansAfterARejectionAndReturnsTheRejectionTextInTheNextPrompt(t *testing.T) {
	e := newPlannerEnv(t)
	e.rt.steps = []runStep{answer(badPlan(t)), answer(goodPlan(t))}
	res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if e.rt.calls() != 2 || len(res.Attempts) != 2 {
		t.Fatalf("Aufrufe %d, Versuche %d", e.rt.calls(), len(res.Attempts))
	}
	first, second := res.Attempts[0], res.Attempts[1]
	if first.Verdict != VerdictRejected || !slices.Contains(first.RejectionCodes, CodePreconditionUnmet) || first.RejectionText == "" {
		t.Fatalf("erster Versuch = %+v", first)
	}
	if second.Verdict != VerdictAccepted || !second.Replan || first.Replan {
		t.Errorf("zweiter Versuch = %+v", second)
	}
	p1, p2 := e.rt.reqs[0].Prompt, e.rt.reqs[1].Prompt
	if strings.Contains(p1, "Rückmeldung zum letzten Versuch") {
		t.Error("der erste Prompt trägt schon eine Rückmeldung")
	}
	if !strings.Contains(p2, "# Rückmeldung zum letzten Versuch") || !strings.Contains(p2, first.RejectionText) {
		t.Errorf("der zweite Prompt trägt den Ablehnungstext nicht:\n%s", p2[max(0, len(p2)-1500):])
	}
	if !strings.Contains(p2, "[precondition_unmet] dec-1") || !strings.Contains(p2, `"decision_id":"dec-1"`) {
		t.Error("der zweite Prompt nennt Code, Entscheidung und den abgelehnten Vorschlag nicht")
	}
	if res.Batch.BatchID == "" || first.RawOutput == nil || string(first.RawOutput) != badPlan(t) {
		t.Errorf("rohe Ausgabe nicht festgehalten: %s", first.RawOutput)
	}
}

func TestPlannerReplansOnAStaleRevisionWithAFreshSnapshot(t *testing.T) {
	e := newPlannerEnv(t)
	fresh := e.snapshot()
	fresh.StateRevision = 8
	a := fresh.order(t, "wo-a")
	a.State, a.ActiveExecutionID = domain.WOInProgress, "exec-a"
	fresh.activate(domain.RoleImplementer, "exec-a", "wo-a")
	e.src.snaps = []ProjectSnapshot{fresh} // der Zustand hat sich seit dem Planungs-Snapshot (Revision 7) geändert

	second := modelOutput(t, realImpl("dec-1", "wo-b"))
	e.rt.steps = []runStep{answer(goodPlan(t)), answer(second)}
	res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	first := res.Attempts[0]
	if !first.Stale || first.CurrentRevision != 8 || first.SnapshotRevision != 7 || first.Verdict != VerdictRejected || !slices.Contains(first.RejectionCodes, CodeStaleRevision) {
		t.Fatalf("erster Versuch = %+v", first)
	}
	// Das zählt als Neuplanung und läuft auf dem frischen Snapshot (Revision 8).
	if len(res.Attempts) != 2 || !res.Attempts[1].Replan || res.Attempts[1].SnapshotRevision != 8 {
		t.Fatalf("Versuche = %+v", res.Attempts)
	}
	if !strings.Contains(e.rt.reqs[1].Prompt, "Zustandsrevision: 8") || !strings.Contains(e.rt.reqs[1].Prompt, "[stale_revision]") {
		t.Error("der Neuplanungs-Prompt trägt weder den frischen Zustand noch die Rückmeldung")
	}
	if res.Batch.StateRevision != 8 {
		t.Errorf("angenommener Plan beruht auf Revision %d", res.Batch.StateRevision)
	}
}

func TestStaleRevisionCountsAgainstTheReplanBudget(t *testing.T) {
	e := newPlannerEnv(t)
	fresh := e.snapshot()
	fresh.StateRevision = 8
	e.src.snaps = []ProjectSnapshot{fresh}
	e.rt.steps = []runStep{answer(goodPlan(t)), answer(goodPlan(t)), answer(goodPlan(t))}
	// Die Ausgaben beziehen sich immer auf den Planungs-Snapshot; ab Versuch 2 ist das der frische (Revision 8) und gilt.
	res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
	if err != nil || len(res.Attempts) != 2 {
		t.Fatalf("err %v, Versuche %d", err, len(res.Attempts))
	}
	// Ohne Neuplanung (MaxReplans < 0) bleibt der veraltete Stand ein Wartezustand.
	e2 := newPlannerEnv(t)
	e2.src.snaps = []ProjectSnapshot{fresh}
	e2.rt.steps = []runStep{answer(goodPlan(t))}
	_, err = e2.planner(func(c *PlannerConfig) { c.MaxReplans = -1 }).PlanWithRecord(context.Background(), e2.snapshot())
	mustSuspend(t, err, SuspendBudgetExhausted, 1)
}

func TestPlannerSuspendsWhenTheReplanBudgetIsExhausted(t *testing.T) {
	e := newPlannerEnv(t)
	e.rt.steps = []runStep{answer(badPlan(t)), answer(badPlan(t)), answer(badPlan(t)), answer(goodPlan(t))}
	got, err := e.planner().Plan(context.Background(), e.snapshot())
	se := mustSuspend(t, err, SuspendBudgetExhausted, 3)
	if e.rt.calls() != 3 {
		t.Errorf("%d Aufrufe: höchstens zwei Neuplanungen, also drei Versuche", e.rt.calls())
	}
	if got.Decisions != nil || got.BatchID != "" {
		t.Errorf("kein Ersatzplan: %+v", got)
	}
	for i, a := range se.Attempts {
		if a.Number != i+1 || a.Verdict != VerdictRejected || a.RejectionText == "" || len(a.RejectionCodes) == 0 || a.RawOutput == nil || a.Replan != (i > 0) {
			t.Errorf("Versuch %d = %+v", i+1, a)
		}
	}
}

func TestMissingStructuredOutputIsHandledLikeARejectionAndThenWaits(t *testing.T) {
	t.Run("ein Versuch ohne Ausgabe, dann zulässig", func(t *testing.T) {
		e := newPlannerEnv(t)
		e.rt.steps = []runStep{answerWithoutOutput(), answer(goodPlan(t))}
		res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
		if err != nil || len(res.Attempts) != 2 || res.Attempts[0].Verdict != VerdictNoOutput || res.Attempts[0].FailureDetail == "" {
			t.Fatalf("err %v, Versuche %+v", err, res.Attempts)
		}
		if !strings.Contains(e.rt.reqs[1].Prompt, "keine gültige strukturierte Ausgabe") {
			t.Error("die Rückmeldung nennt die fehlende Ausgabe nicht")
		}
	})
	t.Run("Fehlercode des Runners, jedes Mal", func(t *testing.T) {
		e := newPlannerEnv(t)
		for range 3 {
			e.rt.steps = append(e.rt.steps, failWith("structured_output_failed", "keine gültige Ausgabe nach mehreren Versuchen"))
		}
		_, err := e.planner().Plan(context.Background(), e.snapshot())
		se := mustSuspend(t, err, SuspendBudgetExhausted, 3)
		if se.Attempts[0].TerminalCode != "structured_output_failed" || se.Attempts[2].Verdict != VerdictNoOutput {
			t.Errorf("Versuche = %+v", se.Attempts)
		}
	})
	t.Run("eine Ausgabe, die kein Planungsergebnis ist, ist eine Ablehnung (schema)", func(t *testing.T) {
		e := newPlannerEnv(t)
		e.rt.steps = []runStep{answer(`{"irgendwas": 1}`), answer(goodPlan(t))}
		res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
		if err != nil || res.Attempts[0].Verdict != VerdictRejected || !slices.Contains(res.Attempts[0].RejectionCodes, CodeSchema) {
			t.Fatalf("err %v, Versuche %+v", err, res.Attempts)
		}
	})
}

// ---- Der Supervisor fehlt: sichtbar warten, kein Ersatzplan -----------------------------------------------

func TestPlannerWaitsVisiblyWhenTheSupervisorIsMissing(t *testing.T) {
	cases := map[string]struct {
		step   runStep
		reason SuspendReason
	}{
		"Runtime startet nicht":   {startFails("uv nicht gefunden"), SuspendSupervisorUnavailable},
		"nicht angemeldet":        {failWith("not_logged_in", "Kein Login"), SuspendSupervisorUnavailable},
		"Absturz ohne Ergebnis":   {crashed(), SuspendSupervisorUnavailable},
		"Abo-Limit":               {limitReached(), SuspendSubscriptionLimit},
		"Laufzeitgrenze":          {failWith("max_runtime_exceeded", "zu lange"), SuspendSupervisorUnavailable},
		"Skill-Snapshot defekt":   {failWith("skills_not_loaded", "nicht geladen"), SuspendSupervisorUnavailable},
		"Ausgabeschema abgelehnt": {failWith("output_schema_invalid", "kein Draft-07"), SuspendSupervisorUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newPlannerEnv(t)
			e.rt.steps = []runStep{c.step, answer(goodPlan(t))}
			got, err := e.planner().Plan(context.Background(), e.snapshot())
			se := mustSuspend(t, err, c.reason, 1)
			if e.rt.calls() != 1 {
				t.Errorf("%d Aufrufe: ein fehlender Supervisor wird nicht wiederholt", e.rt.calls())
			}
			if got.Decisions != nil || got.BatchID != "" {
				t.Errorf("Go darf keinen Ersatzplan liefern: %+v", got)
			}
			if a := se.Attempts[0]; a.Verdict != VerdictSupervisorFailed || a.FailureDetail == "" {
				t.Errorf("Versuch = %+v", a)
			}
		})
	}
}

func TestPlannerWaitsWhenTheCurrentStateIsUnavailable(t *testing.T) {
	e := newPlannerEnv(t)
	e.src.err = errors.New("Zustand gerade nicht lesbar")
	e.rt.steps = []runStep{answer(goodPlan(t))}
	_, err := e.planner().Plan(context.Background(), e.snapshot())
	se := mustSuspend(t, err, SuspendStateUnavailable, 1)
	if se.Attempts[0].Verdict != VerdictStateUnavailable || !errors.Is(err, e.src.err) {
		t.Errorf("Versuch %+v, Ursache %v", se.Attempts[0], se.Cause)
	}

	e = newPlannerEnv(t)
	other := e.snapshot()
	other.ProjectID = "anderes"
	e.src.snaps = []ProjectSnapshot{other}
	e.rt.steps = []runStep{answer(goodPlan(t))}
	_, err = e.planner().Plan(context.Background(), e.snapshot())
	mustSuspend(t, err, SuspendStateUnavailable, 1)

	e = newPlannerEnv(t)
	bad := e.snapshot()
	bad.Capacity.MaxWorkers = 0 // verletzt die Invarianten
	_, err = e.planner().Plan(context.Background(), bad)
	mustSuspend(t, err, SuspendStateUnavailable, 0)
	if e.rt.calls() != 0 {
		t.Error("auf einem ungültigen Planungs-Snapshot wird kein Modell aufgerufen")
	}
}

func TestPlannerReturnsTheContextErrorWhenCancelled(t *testing.T) {
	e := newPlannerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.rt.steps = []runStep{func(req runner.Request) (runner.Execution, error) {
		cancel()
		return runner.Execution{}, ctx.Err()
	}}
	_, err := e.planner().Plan(ctx, e.snapshot())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrPlanningSuspended) {
		t.Errorf("err = %v: Abbruch des Aufrufers ist kein Wartezustand", err)
	}
}

// ---- Profil des Supervisors -------------------------------------------------------------------------------------

func TestSupervisorProfileComesFromTheResolver(t *testing.T) {
	t.Run("Konfiguration: Rollenstandard", func(t *testing.T) {
		e := newPlannerEnv(t)
		e.rt.steps = []runStep{answer(goodPlan(t))}
		res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
		if err != nil {
			t.Fatal(err)
		}
		p := e.rt.reqs[0].Profile
		if p.Role != domain.RoleSupervisor || p.Model != mBig || p.Effort != domain.EffortHigh || p.ValueSources.Model != domain.SourceRole {
			t.Errorf("Profil = %+v", p)
		}
		if a := res.Attempts[0]; a.Profile.Model != mBig || a.EffectiveModel != mBig || a.Profile.RequestedEffort != domain.EffortHigh || a.Profile.ValueSources == nil {
			t.Errorf("Protokoll hält das Profil nicht fest: %+v", a)
		}
	})
	t.Run("ein individueller Agentenwert hat Vorrang", func(t *testing.T) {
		e := newPlannerEnv(t)
		cfg := testConfig()
		cfg.Agents["sup-lean"] = profiles.Agent{Role: domain.RoleSupervisor, Model: mMid, Effort: domain.EffortMedium}
		e.cfg, e.pol = cfg, e.policyFor(cfg)
		snap := e.snapshot()
		snap.Roles = BuildRoleOffers(cfg, e.caps, e.cat, map[domain.Role]string{domain.RoleSupervisor: "sup-lean"})
		e.src.snaps = []ProjectSnapshot{snap}
		e.rt.steps = []runStep{answer(goodPlan(t))}
		res, err := e.planner().PlanWithRecord(context.Background(), snap)
		if err != nil {
			t.Fatal(err)
		}
		p := e.rt.reqs[0].Profile
		if p.ProfileID != "supervisor:sup-lean" || p.Model != mMid || p.Effort != domain.EffortMedium || p.ValueSources.Model != domain.SourceAgent {
			t.Errorf("Profil = %+v", p)
		}
		if res.Attempts[0].AgentID != "sup-lean" {
			t.Errorf("Agent im Protokoll: %q", res.Attempts[0].AgentID)
		}
	})
	t.Run("der Resolver ist austauschbar und bekommt Rolle, Agent und Auftragswerte", func(t *testing.T) {
		e := newPlannerEnv(t)
		fake := &fakeResolver{inner: e.pol.Resolver}
		over := profiles.Overrides{Order: profiles.Selection{Model: mMid, Effort: domain.EffortLow}}
		e.rt.steps = []runStep{answer(goodPlan(t))}
		if _, err := e.planner(func(c *PlannerConfig) { c.Profiles, c.Overrides = fake, over }).Plan(context.Background(), e.snapshot()); err != nil {
			t.Fatal(err)
		}
		if fake.role != domain.RoleSupervisor || fake.agent != "" || fake.overrides.Order.Model != mMid {
			t.Errorf("Resolver aufgerufen mit %s/%q/%+v", fake.role, fake.agent, fake.overrides)
		}
		if p := e.rt.reqs[0].Profile; p.Model != mMid || p.Effort != domain.EffortLow || p.ValueSources.Model != domain.SourceOrder {
			t.Errorf("Profil mit Auftragswerten = %+v", p)
		}
	})
	t.Run("nicht readonly: keine Planung", func(t *testing.T) {
		e := newPlannerEnv(t)
		cfg := testConfig()
		rp := cfg.Roles[domain.RoleSupervisor]
		rp.PolicyProfile = "scoped_write"
		cfg.Roles[domain.RoleSupervisor] = rp
		e.cfg, e.pol = cfg, e.policyFor(cfg)
		e.rt.steps = []runStep{answer(goodPlan(t))}
		_, err := e.planner().Plan(context.Background(), e.snapshot())
		mustSuspend(t, err, SuspendSupervisorUnavailable, 1)
		if e.rt.calls() != 0 {
			t.Error("die Runtime wurde mit einem schreibenden Profil aufgerufen")
		}
	})
	t.Run("der führende Skill fehlt: keine Planung", func(t *testing.T) {
		e := newPlannerEnv(t)
		e.rt.steps = []runStep{answer(goodPlan(t))}
		over := profiles.Overrides{Order: profiles.Selection{SkillIDs: []string{"ccmas-asana-ticket"}}}
		_, err := e.planner(func(c *PlannerConfig) { c.Overrides = over }).Plan(context.Background(), e.snapshot())
		se := mustSuspend(t, err, SuspendSupervisorUnavailable, 1)
		if !strings.Contains(se.Detail, LeadingSkill) || e.rt.calls() != 0 {
			t.Errorf("Detail %q, Aufrufe %d", se.Detail, e.rt.calls())
		}
	})
}

type fakeResolver struct {
	inner     ProfileResolver
	role      domain.Role
	agent     string
	overrides profiles.Overrides
}

func (f *fakeResolver) ResolveProfile(role domain.Role, agentID string, o profiles.Overrides, caps profiles.Capabilities) (domain.AgentProfile, error) {
	f.role, f.agent, f.overrides = role, agentID, o
	return f.inner.ResolveProfile(role, agentID, o, caps)
}

// ---- Kontextbudget -------------------------------------------------------------------------------------------

// tightContext ist ein Kontextbudget, das bigSnapshot(e, 120) nur nach den Reduktionsregeln einhält.
const tightContext = 30 << 10

// bigSnapshot hängt viele abgeschlossene Aufträge mit ausführlichem Zustand an.
func bigSnapshot(e *plannerEnv, closed int) ProjectSnapshot {
	s := e.snapshot()
	for i := 0; i < closed; i++ {
		o := Order{ID: "wo-done-" + strings.Repeat("x", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Kind: OrderImplementation, State: domain.WOCompleted, Mode: domain.ModePR,
			Title: "Abgeschlossen", DependsOn: []string{}, Concept: approvedConcept("1.0"), HeadCommit: commitA, Branch: "feature/CC-1-" + strings.Repeat("lang", 10)}
		o.LastReview = &ReviewRecord{Report: report(o.ID, commitA, "1.0", domain.VerdictApproved, "exec-r"), ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-i"}
		s.Orders = append(s.Orders, o)
	}
	return s
}

func TestPlanningContextIsDeterministicBoundedAndNeverDropsOpenOrders(t *testing.T) {
	e := newPlannerEnv(t)
	snap := bigSnapshot(e, 120)
	full, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	before := string(full)

	t.Run("unter dem Budget unverändert und deterministisch", func(t *testing.T) {
		a, err := BuildPlanningContext(e.snapshot(), 0)
		if err != nil || len(a.Reductions) != 0 || a.Bytes != len(a.JSON) {
			t.Fatalf("Kontext = %+v, %v", a, err)
		}
		// Mischung der Reihenfolge ändert die Bytes nicht.
		shuffled := e.snapshot()
		slices.Reverse(shuffled.Orders)
		b, _ := BuildPlanningContext(shuffled, 0)
		if a.JSON != b.JSON {
			t.Error("der Kontext hängt von der Reihenfolge der Eingabe ab")
		}
	})
	t.Run("Reduktion in dokumentierter Reihenfolge, nur soweit nötig", func(t *testing.T) {
		limit := len(before) - 1
		pc, err := BuildPlanningContext(snap, limit)
		if err != nil {
			t.Fatal(err)
		}
		if pc.Bytes > limit || len(pc.Reductions) == 0 || pc.Reductions[0] != ReduceSkillDescriptions {
			t.Fatalf("Kontext %d Bytes, Reduktionen %v", pc.Bytes, pc.Reductions)
		}
		tight, err := BuildPlanningContext(snap, tightContext)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(tight.Reductions, ReduceClosedOrders) || tight.Bytes > tightContext {
			t.Fatalf("Reduktionen %v, %d Bytes", tight.Reductions, tight.Bytes)
		}
		order := []string{ReduceSkillDescriptions, ReduceClosedOrders, ReduceFindingTexts, ReduceSkillList, ReduceQuestionTexts}
		last := -1
		for _, r := range tight.Reductions {
			i := slices.Index(order, r)
			if i <= last {
				t.Errorf("Reihenfolge verletzt: %v", tight.Reductions)
			}
			last = i
		}
	})
	t.Run("offene und blockierte Aufträge bleiben vollständig, alle Aufträge bleiben", func(t *testing.T) {
		pc, err := BuildPlanningContext(snap, tightContext)
		if err != nil {
			t.Fatal(err)
		}
		var got ProjectSnapshot
		if err := json.Unmarshal([]byte(pc.JSON), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Orders) != len(snap.Orders) {
			t.Fatalf("%d von %d Aufträgen im Kontext: nichts wird still weggelassen", len(got.Orders), len(snap.Orders))
		}
		for _, o := range got.Orders {
			orig := snap.order(t, o.ID)
			if closedOrder(*orig) {
				if o.Concept != nil || o.LastReview != nil || o.HeadCommit != "" {
					t.Errorf("abgeschlossener Auftrag %s nicht verkürzt: %+v", o.ID, o)
				}
				continue
			}
			wantOpen, _ := json.Marshal(snap.Canonical().Orders[indexOfOrder(snap.Canonical(), o.ID)])
			gotOpen, _ := json.Marshal(o)
			if string(wantOpen) != string(gotOpen) {
				t.Errorf("offener Auftrag %s wurde verändert:\n%s\n%s", o.ID, wantOpen, gotOpen)
			}
		}
	})
	t.Run("zu groß trotz aller Regeln: Fehler statt stillem Weglassen", func(t *testing.T) {
		_, err := BuildPlanningContext(snap, 2<<10)
		var ce *ContextError
		if !errors.Is(err, ErrContextTooLarge) || !errors.As(err, &ce) || ce.OpenOrders == 0 || len(ce.Reductions) == 0 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("der Snapshot des Aufrufers bleibt unverändert", func(t *testing.T) {
		_, _ = BuildPlanningContext(snap, tightContext)
		after, _ := json.Marshal(snap)
		if string(after) != before {
			t.Error("BuildPlanningContext hat den Snapshot verändert")
		}
	})
}

func indexOfOrder(s ProjectSnapshot, id string) int {
	for i, o := range s.Orders {
		if o.ID == id {
			return i
		}
	}
	return -1
}

func TestPlannerUsesTheReducedContextAndSaysSo(t *testing.T) {
	e := newPlannerEnv(t)
	snap := bigSnapshot(e, 120)
	e.src.snaps = []ProjectSnapshot{snap}
	e.rt.steps = []runStep{answer(goodPlan(t))}
	res, err := e.planner(func(c *PlannerConfig) { c.ContextLimit = tightContext }).PlanWithRecord(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Attempts[0]
	if len(a.ContextReduced) == 0 || a.ContextBytes > tightContext || !strings.Contains(e.rt.reqs[0].Prompt, "wurde zur Größenbegrenzung verkürzt") {
		t.Errorf("Versuch = %+v", a)
	}
	// Der Snapshot-Hash im Protokoll bezeichnet den vollständigen Planungs-Snapshot, nicht den verkürzten Kontext.
	full, _ := json.Marshal(snap)
	if a.SnapshotSHA256 != sha256Hex(full) {
		t.Error("Snapshot-Hash gehört nicht zum vollständigen Snapshot")
	}
}

func TestPlannerWaitsWhenTheStateDoesNotFitTheContextBudget(t *testing.T) {
	e := newPlannerEnv(t)
	snap := bigSnapshot(e, 120)
	e.src.snaps = []ProjectSnapshot{snap}
	e.rt.steps = []runStep{answer(goodPlan(t))}
	_, err := e.planner(func(c *PlannerConfig) { c.ContextLimit = 4 << 10 }).Plan(context.Background(), snap)
	se := mustSuspend(t, err, SuspendContextTooLarge, 1)
	if !errors.Is(err, ErrContextTooLarge) || e.rt.calls() != 0 {
		t.Errorf("err %v, Aufrufe %d: ohne Kontext wird kein Modell aufgerufen", err, e.rt.calls())
	}
	if se.Attempts[0].Verdict != VerdictStateUnavailable {
		t.Errorf("Versuch = %+v", se.Attempts[0])
	}
}

// ---- Prompt und Protokoll ---------------------------------------------------------------------------------------------

func TestPromptIsDeterministic(t *testing.T) {
	var prompts [3]string
	var hashes [3]string
	for i := range prompts {
		e := newPlannerEnv(t)
		snap := e.snapshot()
		if i == 1 { // gleiche Inhalte, andere Reihenfolge der Eingabe
			slices.Reverse(snap.Orders)
			slices.Reverse(snap.Skills)
		}
		e.src.snaps = []ProjectSnapshot{snap}
		e.rt.steps = []runStep{answer(goodPlan(t))}
		res, err := e.planner().PlanWithRecord(context.Background(), snap)
		if err != nil {
			t.Fatal(err)
		}
		prompts[i], hashes[i] = e.rt.reqs[0].Prompt, res.Attempts[0].PromptSHA256
	}
	if prompts[0] != prompts[1] || prompts[0] != prompts[2] || hashes[0] != hashes[1] || hashes[0] != hashes[2] {
		t.Error("gleiche Eingaben ergeben nicht denselben Prompt")
	}
}

func TestAttemptRecordHoldsTheEvidenceOfAnAttempt(t *testing.T) {
	e := newPlannerEnv(t)
	e.rt.steps = []runStep{answer(badPlan(t)), answer(goodPlan(t))}
	snap := e.snapshot()
	res, err := e.planner().PlanWithRecord(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(snap)
	for i, a := range res.Attempts {
		req := e.rt.reqs[i]
		check := func(name string, got, want any) {
			if got != want {
				t.Errorf("Versuch %d: %s = %v, erwartet %v", a.Number, name, got, want)
			}
		}
		check("Nummer", a.Number, i+1)
		check("Ausführung", a.ExecutionID, req.ExecutionID)
		check("Snapshot-Hash", a.SnapshotSHA256, sha256Hex(full))
		check("Prompt-Hash", a.PromptSHA256, sha256Hex([]byte(req.Prompt)))
		check("Prompt-Länge", a.PromptBytes, len(req.Prompt))
		check("Systemprompt-Hash", a.SystemPromptSHA, prompts.SHA256(prompts.Supervisor()))
		check("Systemprompt-Länge", a.SystemPromptSize, len(prompts.Supervisor()))
		check("Schema-Hash", a.SchemaSHA256, sha256Hex(ModelOutputSchema()))
		check("Skill-Snapshot", a.SkillSnapshotID, req.SkillSnapshot.SnapshotID)
		check("Sitzung", a.SessionID, "sess-"+req.ExecutionID)
		check("geladener Skill-Snapshot", a.LoadedSnapshotID, req.SkillSnapshot.SnapshotID)
		check("geladene Skills", strings.Join(a.LoadedSkills, ","), "ccmas-supervisor,ccmas-work-assessment")
		check("CLI-Version", a.ClaudeVersion, "2.1.311")
		check("SDK-Version", a.SDKVersion, "0.2.180")
		check("wirksames Modell", a.EffectiveModel, mBig)
		check("Profil-Modell", a.Profile.Model, mBig)
		check("Dauer", a.DurationMS, int64(1500))
		if a.StartedAt.IsZero() || len(a.Skills) != 2 {
			t.Errorf("Versuch %d: Start %v, Skills %v", a.Number, a.StartedAt, a.Skills)
		}
		for _, s := range a.Skills {
			sk, _ := e.cat.Skill(s.ID)
			if s.TreeSHA256 != sk.TreeSHA256 || s.Version != sk.Version || !s.InPrompt {
				t.Errorf("Skill im Protokoll: %+v, Katalog %+v", s, sk)
			}
		}
	}
	// Das Protokoll ist als JSON ausgebbar und enthält nie den Text des Prompts.
	raw, err := json.Marshal(res.Attempts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Planungsauftrag") || strings.Contains(string(raw), "# Supervisor von CCMAS") {
		t.Error("das Protokoll trägt Text des Prompts")
	}
	if !strings.Contains(string(raw), `"verdict":"rejected"`) || !strings.Contains(string(raw), `"verdict":"accepted"`) {
		t.Errorf("Urteile fehlen im JSON: %s", raw)
	}
}

// ---- Konfiguration -------------------------------------------------------------------------------------------------

func TestNewPlannerChecksItsConfiguration(t *testing.T) {
	e := newPlannerEnv(t)
	ok := PlannerConfig{Runtime: e.rt, Source: e.src, Policy: e.pol, Catalog: e.cat, ClaudeBinary: "/opt/test/claude", SnapshotDir: e.snapDir}
	if _, err := NewPlanner(ok); err != nil {
		t.Fatal(err)
	}
	unchecked, err := skills.Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*PlannerConfig){
		"ohne Runtime":                  func(c *PlannerConfig) { c.Runtime = nil },
		"ohne Quelle":                   func(c *PlannerConfig) { c.Source = nil },
		"ohne Katalog":                  func(c *PlannerConfig) { c.Catalog = nil },
		"Katalog nicht mit LoadChecked": func(c *PlannerConfig) { c.Catalog = unchecked },
		"ohne Binary":                   func(c *PlannerConfig) { c.ClaudeBinary = "" },
		"ohne Snapshot-Verzeichnis":     func(c *PlannerConfig) { c.SnapshotDir = "" },
		"unvollständige Policy":         func(c *PlannerConfig) { c.Policy = Policy{} },
	} {
		cfg := ok
		mod(&cfg)
		if _, err := NewPlanner(cfg); err == nil {
			t.Errorf("%s: nicht abgewiesen", name)
		}
	}
	p, _ := NewPlanner(ok)
	if p.cfg.MaxReplans != DefaultMaxReplans || p.cfg.MaxTurns != DefaultMaxTurns || p.cfg.MaxRuntime != DefaultMaxRuntime ||
		p.cfg.ContextLimit != DefaultContextLimit || p.cfg.SystemPrompt != prompts.Supervisor() || p.cfg.Profiles == nil {
		t.Errorf("Standardwerte = %+v", p.cfg)
	}
}

// ---- Keine szenariospezifische Logik --------------------------------------------------------------------------------

// TestPlannerAddsNothingOfItsOwn: Was das Modell nicht plant, kommt nicht in den Plan. Ein Plan mit einer einzigen Entscheidung
// bleibt ein Plan mit einer Entscheidung, auch wenn der Snapshot weitere startbare Arbeit zeigt.
func TestPlannerAddsNothingOfItsOwn(t *testing.T) {
	e := newPlannerEnv(t)
	e.rt.steps = []runStep{answer(modelOutput(t, realImpl("dec-1", "wo-a")))}
	res, err := e.planner().PlanWithRecord(context.Background(), e.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Batch.Decisions) != 1 || res.Batch.Decisions[0].Target.ID != "wo-a" {
		t.Errorf("Plan = %+v", res.Batch.Decisions)
	}
}
