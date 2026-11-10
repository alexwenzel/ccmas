package scenario_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
	"github.com/alexwenzel/ccmas/internal/supervisor/scenario"
	"github.com/alexwenzel/ccmas/prompts"
)

// Belege der Live-Szenarien (CC-14, W14.3). Je Szenario eine Datei unter docs/architecture/evidence/supervisor/. Dieselbe
// Datei schreibt der Live-Test (live_test.go, nur mit CCMAS_LIVE_TESTS=1) und liest der Offline-Test hier, der in jedem
// make check läuft, kein Konto braucht und prüft, dass der Beleg zum heutigen Stand von Validator, Policy und Prädikaten passt.

const (
	repoRoot    = "../../.."
	evidenceDir = "../../../docs/architecture/evidence/supervisor"

	// SessionLimit begrenzt, wie viele echte Sitzungen die Belege dieses Verzeichnisses und ein Live-Testlauf zusammen enthalten
	// dürfen (einschließlich Neuplanungen und Fehlversuchen): je Live-Testlauf, nicht für das ganze Ticket. Die Belege S1 bis S7
	// enthalten 9 Sitzungen (8 für S1 bis S6, 1 für S7). Das Ticketbudget von 12 bis 16 echten Sitzungen in CC-14 umfasst zusätzlich
	// den Rauchlauf haiku/low aus W14.2, der hier nicht gezählt wird (1 + 8 + 1 = 10 Sitzungen, das Budget ist eingehalten).
	SessionLimit = 10

	supervisorModel = "claude-opus-5-5" // laut Auftrag das Modell des Supervisorprofils der Beispielkonfiguration
)

// tb ist, was die Prüfungen der Belege brauchen. *testing.T erfüllt es; der Test der Prüfung selbst setzt einen Aufzeichner ein, um
// zu zeigen, dass ein veralteter oder verfälschter Beleg gefunden wird.
type tb interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

type fatal struct{}

// recorder zeichnet Meldungen auf; Fatalf beendet die laufende Prüfung (panic, in check abgefangen).
type recorder struct{ problems []string }

func (r *recorder) Helper()             {}
func (r *recorder) Logf(string, ...any) {}
func (r *recorder) Errorf(format string, a ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, a...))
}
func (r *recorder) Fatalf(format string, a ...any) { r.Errorf(format, a...); panic(fatal{}) }

// prefixed setzt jeder Meldung das Szenario voran.
type prefixed struct {
	tb
	id scenario.ID
}

func (p prefixed) Errorf(format string, a ...any) {
	p.tb.Errorf("%s: %s", p.id, fmt.Sprintf(format, a...))
}
func (p prefixed) Fatalf(format string, a ...any) {
	p.tb.Fatalf("%s: %s", p.id, fmt.Sprintf(format, a...))
}

// guard fängt das Fatalf des Aufzeichners ab.
func guard(f func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fatal); !ok {
				panic(r)
			}
		}
	}()
	f()
}

// ---- Belegformat -----------------------------------------------------------------------------------------------

type scenarioEvidence struct {
	Evidence     string            `json:"evidence"`
	Scenario     string            `json:"scenario"`
	Description  string            `json:"description"`
	Invocation   string            `json:"invocation"`
	Note         string            `json:"note"`
	Redaction    string            `json:"redaction"`
	PolicyInputs map[string]string `json:"policy_inputs"`
	Runs         []runEvidence     `json:"runs"`
	Final        finalEvidence     `json:"final"`
}

// runEvidence ist ein Planungslauf (Plan mit bis zu zwei Neuplanungen). Mehr als einen Lauf gibt es nur nach einem
// dokumentierten Infrastrukturfehler (RepeatReason).
type runEvidence struct {
	Run           int                   `json:"run"`
	RepeatReason  string                `json:"repeat_reason,omitempty"`
	Outcome       string                `json:"outcome"` // accepted | suspended
	SuspendReason string                `json:"suspend_reason,omitempty"`
	SuspendDetail string                `json:"suspend_detail,omitempty"`
	Sessions      int                   `json:"sessions"`
	Attempts      []attemptEvidence     `json:"attempts"`
	AcceptedBatch *domain.DecisionBatch `json:"accepted_batch,omitempty"`
	// Predicates: Ergebnis jedes Prädikats auf dem angenommenen Batch; nur bei Outcome accepted.
	Predicates    []scenario.Result `json:"predicates,omitempty"`
	PredicatesMet *bool             `json:"predicates_met,omitempty"`
}

type finalEvidence struct {
	Run           int    `json:"run"`
	Outcome       string `json:"outcome"`
	AttemptCount  int    `json:"attempt_count"`
	Sessions      int    `json:"sessions"`
	PredicatesMet *bool  `json:"predicates_met,omitempty"`
}

type attemptEvidence struct {
	Number    int    `json:"number"`
	Date      string `json:"date"`
	StartedAt string `json:"started_at"`
	Scenario  string `json:"scenario"`

	Snapshot          json.RawMessage `json:"snapshot"` // vollständiger kanonischer Planungs-Snapshot
	SnapshotSHA256    string          `json:"snapshot_sha256"`
	SnapshotRevision  int64           `json:"snapshot_revision"`
	ContextBytes      int             `json:"context_bytes"`
	ContextReductions []string        `json:"context_reductions"`
	// ValidationSnapshot: der Zustand, gegen den der Validator geprüft hat, falls er vom Planungs-Snapshot abweicht.
	ValidationSnapshot       json.RawMessage `json:"validation_snapshot,omitempty"`
	ValidationSnapshotSHA256 string          `json:"validation_snapshot_sha256"`
	ValidationRevision       int64           `json:"validation_revision"`
	Stale                    bool            `json:"stale"`

	PromptSHA256       string `json:"prompt_sha256"`
	PromptBytes        int    `json:"prompt_bytes"`
	SystemPromptSHA256 string `json:"system_prompt_sha256"`
	SystemPromptBytes  int    `json:"system_prompt_bytes"`
	OutputSchemaSHA256 string `json:"output_schema_sha256"`
	FeedbackSHA256     string `json:"feedback_sha256,omitempty"`
	Replan             bool   `json:"replan"`

	SkillSnapshotID       string                   `json:"skill_snapshot_id"`
	Skills                []supervisor.SkillRecord `json:"skills"`
	LoadedSkillSnapshotID string                   `json:"loaded_skill_snapshot_id"`
	LoadedSkills          []string                 `json:"loaded_skills"`

	Profile         domain.AgentProfile `json:"profile"` // angefordert (requested_*), wirksam (model, effort), Herkunft (value_sources), profile_id/version
	EffectiveModel  string              `json:"effective_model"`
	EffortEffective string              `json:"effort_effective"`

	SDKVersion    string `json:"sdk_version"`
	CLIVersion    string `json:"cli_version"`
	SessionID     string `json:"session_id"`
	ExecutionID   string `json:"execution_id"`
	DurationMS    int64  `json:"duration_ms"`
	TerminalCode  string `json:"terminal_code,omitempty"`
	FailureDetail string `json:"failure_detail,omitempty"`

	RawOutput    json.RawMessage       `json:"raw_output,omitempty"`
	StampedBatch *domain.DecisionBatch `json:"stamped_batch,omitempty"`

	Verdict    string     `json:"verdict"`
	Validation validation `json:"validation"`
}

type validation struct {
	Accepted   bool                   `json:"accepted"`
	Codes      []supervisor.Code      `json:"codes"`
	Rejections []supervisor.Rejection `json:"rejections"`
	Text       string                 `json:"text,omitempty"`
}

// ---- Umgebung: Policy aus der Beispielkonfiguration, Katalog des Worktrees -------------------------------------

type evEnv struct {
	root   string
	cat    *skills.Catalog
	rules  *skills.Rules
	caps   profiles.Capabilities
	cfg    profiles.Config
	pol    supervisor.Policy
	offers scenario.Env
	hashes map[string]string
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// newEvEnv baut die Policy auf dem Weg, den auch der Betrieb geht: `profile init` (Store.Init) legt die Startprofile aus den
// Beispielkonfigurationen in einem temporären Konfigurationsverzeichnis an, dort werden sie gelesen; der Skill-Katalog des
// Worktrees kommt über skills.LoadChecked. Es gibt keine Overrides.
func newEvEnv(t *testing.T) *evEnv {
	t.Helper()
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "config")
	store := profiles.NewStore(dir)
	if _, err := store.Init(config.ModelsExample(), config.AgentsExample()); err != nil {
		t.Fatal(err)
	}
	caps, err := store.LoadCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cat, rules, err := skills.LoadChecked(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := profiles.NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := supervisor.NewPolicy(res, caps, cat, rules)
	if err != nil {
		t.Fatal(err)
	}
	read := func(parts ...string) []byte {
		raw, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	e := &evEnv{root: root, cat: cat, rules: rules, caps: caps, cfg: cfg, pol: pol,
		offers: scenario.Env{Roles: supervisor.BuildRoleOffers(cfg, caps, cat, nil), Skills: supervisor.BuildSkillOffers(cat)}}
	e.hashes = map[string]string{
		"models.example.yaml":          sha(config.ModelsExample()),
		"agents.example.yaml":          sha(config.AgentsExample()),
		"models.yaml (profile init)":   sha(read2(t, filepath.Join(dir, profiles.ModelsFile))),
		"agents.yaml (profile init)":   sha(read2(t, filepath.Join(dir, profiles.AgentsFile))),
		"rules/roles.yaml":             sha(read("rules", "roles.yaml")),
		".claude/skills/manifest.yaml": sha(read(".claude", "skills", "manifest.yaml")),
		"prompts/supervisor.md":        sha([]byte(prompts.Supervisor())),
		"output_schema":                sha(supervisor.ModelOutputSchema()),
	}
	return e
}

func read2(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return raw
}

// supervisorProfile ist das Profil, das der Planer ohne Overrides auflöst.
func (e *evEnv) supervisorProfile(t tb) domain.AgentProfile {
	t.Helper()
	p, err := e.pol.Resolver.ResolveProfile(domain.RoleSupervisor, "", profiles.Overrides{}, e.pol.Caps)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return p
}

func snapshotHash(t tb, s supervisor.ProjectSnapshot) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return sha(raw)
}

// ---- Dateien -------------------------------------------------------------------------------------------------------

func evidenceFile(dir string, id scenario.ID) string { return filepath.Join(dir, string(id)+".json") }

func loadEvidence(path string) (scenarioEvidence, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return scenarioEvidence{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ev scenarioEvidence
	if err := dec.Decode(&ev); err != nil {
		return scenarioEvidence{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return ev, nil
}

func marshalEvidence(ev scenarioEvidence) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(ev); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeEvidenceFile schreibt den Beleg, wenn er redigiert ist (keine Pfade, E-Mail- oder Kontoangaben).
func writeEvidenceFile(path string, ev scenarioEvidence) error {
	raw, err := marshalEvidence(ev)
	if err != nil {
		return err
	}
	if err := checkRedaction(raw); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// Redigierung: keine Pfade (Nutzer-, Temp- und Systempfade), keine E-Mail-Adressen und keine Konto- oder Organisationsangaben.
var forbidden = regexp.MustCompile(`(/Users/|/home/|/private/|/var/|/tmp/|/opt/|\\Users\\|[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}|w3entwickler|alexwenzel|organization_id|account_uuid)`)

func checkRedaction(raw []byte) error {
	if m := forbidden.Find(raw); m != nil {
		return fmt.Errorf("Beleg nicht redigiert: %q", m)
	}
	return nil
}

// countSessions zählt die echten Sitzungen aller Belege im Verzeichnis (Versuche mit Session-ID).
func countSessions(dir string) (int, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	n := 0
	for _, f := range files {
		ev, err := loadEvidence(f)
		if err != nil {
			return 0, err
		}
		for _, r := range ev.Runs {
			for _, a := range r.Attempts {
				if a.SessionID != "" {
					n++
				}
			}
		}
	}
	return n, nil
}

// ---- Offline-Test --------------------------------------------------------------------------------------------------

// planSignature ist die Menge aus Aktion, Zielauftrag und Rolle eines angenommenen Plans.
func planSignature(b domain.DecisionBatch) []string {
	set := map[string]bool{}
	for _, d := range b.Decisions {
		role := ""
		if d.Selection != nil {
			role = string(d.Selection.Role)
		}
		set[fmt.Sprintf("%s|%s|%s", d.Action, d.Target.ID, role)] = true
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSupervisorScenarioEvidence prüft die eingecheckten Belege der Live-Szenarien gegen den heutigen Stand, ohne Konto und
// ohne Modellaufruf: Snapshot-Hashes, Policy-Eingaben, das Urteil des Validators je Versuch, die Prädikate auf den
// angenommenen Plänen und die Unterschiede zwischen den Szenarien.
func TestSupervisorScenarioEvidence(t *testing.T) {
	checkEvidenceDir(t, newEvEnv(t), evidenceDir)
}

// checkEvidenceDir prüft alle Belege im Verzeichnis; jedes Szenario braucht einen.
func checkEvidenceDir(t tb, e *evEnv, dir string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	have := map[scenario.ID]scenarioEvidence{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if err := checkRedaction(raw); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
		ev, err := loadEvidence(f)
		if err != nil {
			t.Errorf("Beleg nicht lesbar: %v", err)
			continue
		}
		id := scenario.ID(strings.TrimSuffix(filepath.Base(f), ".json"))
		if string(id) != ev.Scenario {
			t.Errorf("%s: Datei und Szenario %q passen nicht zusammen", filepath.Base(f), ev.Scenario)
		}
		have[id] = ev
	}
	for _, id := range scenario.IDs() {
		if _, ok := have[id]; !ok {
			t.Errorf("Beleg für Szenario %s fehlt in %s", id, dir)
		}
	}
	total := 0
	signatures := map[scenario.ID][]string{}
	wantProfile := e.supervisorProfile(t)

	for _, id := range scenario.IDs() {
		ev, ok := have[id]
		if !ok {
			continue
		}
		sc := prefixed{t, id}
		guard(func() { total += e.checkScenario(sc, id, ev, wantProfile, signatures) })
	}
	if total > SessionLimit {
		t.Errorf("%d echte Sitzungen in den Belegen, das Budget sind %d", total, SessionLimit)
	}

	// 3. Die angenommenen Pläne unterscheiden sich zwischen den Szenarien: die Mengen aus Aktion, Zielauftrag und Rolle sind
	// paarweise verschieden (jedes Szenario hat andere Zustände, also andere Aufträge); darüber hinaus müssen die Pläne der
	// Szenarien mit gleicher Form des Zustands in der Art der Arbeit voneinander abweichen.
	ids := scenario.IDs()
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a, b := signatures[ids[i]], signatures[ids[j]]
			if a != nil && b != nil && slices.Equal(a, b) {
				t.Errorf("die angenommenen Pläne von %s und %s sind gleich: %v", ids[i], ids[j], a)
			}
		}
	}
	kinds := map[scenario.ID][]string{}
	for id, sig := range signatures {
		set := map[string]bool{}
		for _, s := range sig {
			parts := strings.SplitN(s, "|", 3)
			set[parts[0]+"/"+parts[2]] = true
		}
		var ks []string
		for k := range set {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		kinds[id] = ks
	}
	distinctKinds := map[string]bool{}
	for _, ks := range kinds {
		distinctKinds[strings.Join(ks, ",")] = true
	}
	if len(signatures) > 1 && len(distinctKinds) < 3 {
		t.Errorf("die Pläne sind in ihrer Art kaum verschieden (Aktion/Rolle je Szenario: %v)", kinds)
	}
}

// checkScenario prüft den Beleg eines Szenarios und liefert die Zahl seiner echten Sitzungen.
func (e *evEnv) checkScenario(t tb, id scenario.ID, ev scenarioEvidence, wantProfile domain.AgentProfile, signatures map[scenario.ID][]string) (total int) {
	t.Helper()
	// 1. Policy-Eingaben: weicht einer ab, erklärt das jede spätere Abweichung; der Beleg ist dann veraltet.
	for name, now := range e.hashes {
		if was := ev.PolicyInputs[name]; was != now {
			t.Errorf("Beleg veraltet: Policy-Eingabe %s hat sich geändert (Beleg %.12s…, aktuell %.12s…). Die Live-Läufe sind mit dem neuen Stand zu wiederholen (CCMAS_LIVE_TESTS=1, TestLiveSupervisorScenarios)", name, was, now)
		}
	}
	for name := range ev.PolicyInputs {
		if _, ok := e.hashes[name]; !ok {
			t.Errorf("Beleg nennt die unbekannte Policy-Eingabe %q", name)
		}
	}
	base, err := scenario.Snapshot(id, e.offers)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(ev.Runs) == 0 || len(ev.Runs) > 2 {
		t.Fatalf("%d Läufe im Beleg (einer, höchstens eine dokumentierte Wiederholung)", len(ev.Runs))
	}

	var decidingBatch *domain.DecisionBatch
	for ri, run := range ev.Runs {
		if run.Run != ri+1 || (ri > 0) != (run.RepeatReason != "") {
			t.Errorf("Lauf %d: Nummer %d, Wiederholungsgrund %q", ri+1, run.Run, run.RepeatReason)
		}
		if ri == 1 {
			if err := repeatAllowed(scenarioEvidence{Runs: ev.Runs[:1]}); err != nil {
				t.Errorf("Wiederholung ohne Infrastrukturfehler im ersten Lauf: %v", err)
			}
		}
		sessions := 0
		for ai, a := range run.Attempts {
			if a.SessionID != "" {
				sessions++
			}
			e.checkAttempt(t, id, base, wantProfile, ri, ai, a)
		}
		if sessions != run.Sessions {
			t.Errorf("Lauf %d: %d Sitzungen gemeldet, %d Versuche mit Session-ID", run.Run, run.Sessions, sessions)
		}
		total += sessions
		e.checkRun(t, id, run, &decidingBatch)
	}

	// 2. Endergebnis stimmt mit dem letzten Lauf überein.
	last := ev.Runs[len(ev.Runs)-1]
	if ev.Final.Run != last.Run || ev.Final.Outcome != last.Outcome || ev.Final.AttemptCount != len(last.Attempts) ||
		ev.Final.Sessions != last.Sessions || (ev.Final.PredicatesMet == nil) != (last.PredicatesMet == nil) ||
		(last.PredicatesMet != nil && *ev.Final.PredicatesMet != *last.PredicatesMet) {
		t.Errorf("Endergebnis %+v passt nicht zum letzten Lauf (%s, %d Versuche)", ev.Final, last.Outcome, len(last.Attempts))
	}
	// 2b. Der Nachweis M02 gilt nur, wenn das Szenario mit einem angenommenen Plan endet und alle Prädikate erfüllt sind.
	if last.Outcome != "accepted" || last.PredicatesMet == nil || !*last.PredicatesMet {
		t.Errorf("Der Nachweis M02 gilt nicht mehr: Szenario %s endet nicht mit einem angenommenen Plan und erfüllten Prädikaten (Ergebnis %q, Prädikate erfüllt: %v)", id, last.Outcome, last.PredicatesMet != nil && *last.PredicatesMet)
	}
	if id == scenario.S6 {
		first := last.Attempts[0]
		if first.Verdict != string(supervisor.VerdictRejected) || !slices.Contains(first.Validation.Codes, supervisor.CodeStaleRevision) {
			t.Errorf("Der Nachweis M02 gilt nicht mehr: S6 braucht als ersten Versuch eine Ablehnung mit stale_revision (Urteil %q, Codes %v)", first.Verdict, first.Validation.Codes)
		}
	}
	if decidingBatch != nil {
		signatures[id] = planSignature(*decidingBatch)
	}
	t.Logf("%s: %s nach %d Versuch(en), %d Sitzung(en), Prädikate erfüllt: %v", id, last.Outcome, len(last.Attempts), last.Sessions, last.PredicatesMet != nil && *last.PredicatesMet)

	return total
}

// checkAttempt prüft einen Versuch: Snapshots, Hashes, Profil, Sitzung und das Urteil des Validators.
func (e *evEnv) checkAttempt(t tb, id scenario.ID, base supervisor.ProjectSnapshot, wantProfile domain.AgentProfile, run, idx int, a attemptEvidence) {
	t.Helper()
	where := fmt.Sprintf("Lauf %d, Versuch %d", run+1, a.Number)
	if a.Number != idx+1 || a.Scenario != string(id) || a.Replan != (idx > 0) {
		t.Errorf("%s: Nummer %d, Szenario %q, Neuplanung %v", where, a.Number, a.Scenario, a.Replan)
	}

	// Planungs-Snapshot: vollständig, kanonisch, Hash stimmt; der erste Versuch plant auf dem Snapshot des Szenarios.
	planning, err := supervisor.DecodeSnapshot(a.Snapshot)
	if err != nil {
		t.Errorf("%s: Snapshot nicht lesbar: %v", where, err)
		return
	}
	if err := planning.Check(); err != nil {
		t.Errorf("%s: Snapshot ungültig: %v", where, err)
	}
	if h := snapshotHash(t, planning); h != a.SnapshotSHA256 {
		t.Errorf("%s: Hash des Snapshots %s ≠ gespeichertem %s", where, h, a.SnapshotSHA256)
	}
	if planning.StateRevision != a.SnapshotRevision {
		t.Errorf("%s: Revision %d ≠ %d", where, planning.StateRevision, a.SnapshotRevision)
	}
	if idx == 0 && a.SnapshotSHA256 != snapshotHash(t, base) {
		t.Errorf("Beleg veraltet: der Snapshot des Szenarios %s hat sich geändert (Versuch 1 des Belegs ≠ scenario.Snapshot)", id)
	}

	// Prüfzustand.
	current := planning
	if len(a.ValidationSnapshot) > 0 {
		current, err = supervisor.DecodeSnapshot(a.ValidationSnapshot)
		if err != nil {
			t.Errorf("%s: Prüfzustand nicht lesbar: %v", where, err)
			return
		}
	}
	if h := snapshotHash(t, current); h != a.ValidationSnapshotSHA256 {
		t.Errorf("%s: Hash des Prüfzustands %s ≠ gespeichertem %s", where, h, a.ValidationSnapshotSHA256)
	}
	if (len(a.ValidationSnapshot) > 0) != (a.ValidationSnapshotSHA256 != a.SnapshotSHA256) || a.Stale != (current.StateRevision != planning.StateRevision) || a.ValidationRevision != current.StateRevision {
		t.Errorf("%s: Prüfzustand und Veraltung passen nicht zusammen (stale %v, Revision %d/%d)", where, a.Stale, planning.StateRevision, current.StateRevision)
	}

	// Sitzung und Profil: Supervisorprofil der Beispielkonfiguration, ohne Overrides; wirksames Modell laut Sitzung.
	if a.SessionID == "" || a.SDKVersion == "" || a.CLIVersion == "" {
		if a.Verdict == string(supervisor.VerdictAccepted) || a.Verdict == string(supervisor.VerdictRejected) {
			t.Errorf("%s: Urteil %s ohne Sitzung", where, a.Verdict)
		}
	}
	p := a.Profile
	if p.Role != domain.RoleSupervisor || p.Model != wantProfile.Model || p.Effort != wantProfile.Effort || p.ProfileID != wantProfile.ProfileID || p.Version != wantProfile.Version {
		t.Errorf("Beleg veraltet: %s: Profil %s v%d %s/%s ≠ aktuellem Supervisorprofil %s v%d %s/%s", where, p.ProfileID, p.Version, p.Model, p.Effort, wantProfile.ProfileID, wantProfile.Version, wantProfile.Model, wantProfile.Effort)
	}
	if p.ValueSources == nil || p.ValueSources.Model != domain.SourceRole || p.ValueSources.Effort != domain.SourceRole {
		t.Errorf("%s: Modell oder Effort stammen nicht aus dem Rollenstandard (keine Overrides erwartet): %+v", where, p.ValueSources)
	}
	if a.SessionID != "" && a.EffectiveModel != p.Model {
		t.Errorf("%s: wirksames Modell %q ≠ angefordertem %q", where, a.EffectiveModel, p.Model)
	}
	if !strings.Contains(a.EffortEffective, "nicht gemessen") {
		t.Errorf("%s: der wirksame Effort ist nicht gemessen und darf nicht als gemessen stehen: %q", where, a.EffortEffective)
	}
	if a.SystemPromptSHA256 != e.hashes["prompts/supervisor.md"] || a.OutputSchemaSHA256 != e.hashes["output_schema"] {
		t.Errorf("Beleg veraltet: %s: Systemprompt oder Ausgabeschema weichen vom heutigen Stand ab", where)
	}
	wantSkills := []string{"ccmas-supervisor", "ccmas-work-assessment"}
	var gotSkills []string
	for _, s := range a.Skills {
		gotSkills = append(gotSkills, s.ID)
		def, ok := e.cat.Skill(s.ID)
		if !ok || def.Version != s.Version || def.TreeSHA256 != s.TreeSHA256 {
			t.Errorf("Beleg veraltet: %s: Skill %s v%d (%.12s…) weicht vom Katalog ab", where, s.ID, s.Version, s.TreeSHA256)
		}
	}
	if !slices.Equal(gotSkills, wantSkills) {
		t.Errorf("%s: Skills %v, erwartet %v", where, gotSkills, wantSkills)
	}
	if a.SessionID != "" && (a.LoadedSkillSnapshotID != a.SkillSnapshotID || !slices.Equal(slices.Sorted(slices.Values(a.LoadedSkills)), wantSkills)) {
		t.Errorf("%s: in der Sitzung geladene Skills %v (Snapshot %s) ≠ Skill-Snapshot %s", where, a.LoadedSkills, a.LoadedSkillSnapshotID, a.SkillSnapshotID)
	}

	// Urteil des Validators erneut: derselbe Batch, derselbe Zustand, dieselbe Policy, der heutige Validator.
	switch a.Verdict {
	case string(supervisor.VerdictAccepted), string(supervisor.VerdictRejected):
		if a.StampedBatch == nil {
			t.Errorf("%s: Urteil %s ohne gestempelten Batch", where, a.Verdict)
			return
		}
		if a.StampedBatch.StateRevision != planning.StateRevision || a.StampedBatch.ProjectID != planning.ProjectID {
			t.Errorf("%s: der Batch trägt nicht Projekt und Revision des Planungs-Snapshots", where)
		}
		_, verr := supervisor.Validator{}.Accept(*a.StampedBatch, current, e.pol)
		rej, isRej := supervisor.AsRejection(verr)
		if verr != nil && !isRej {
			t.Errorf("%s: Validator liefert einen Fehler, der keine Ablehnung ist: %v", where, verr)
			return
		}
		if (verr == nil) != a.Validation.Accepted || (verr == nil) != (a.Verdict == string(supervisor.VerdictAccepted)) {
			t.Errorf("Beleg veraltet oder falsch: %s: der heutige Validator urteilt %q, der Beleg %q (angenommen: %v)", where, map[bool]string{true: "angenommen", false: "abgelehnt"}[verr == nil], a.Verdict, a.Validation.Accepted)
			return
		}
		if isRej {
			var got []string
			for _, r := range rej.Rejections {
				got = append(got, string(r.Code)+"|"+r.DecisionID)
			}
			var recorded []string
			for _, r := range a.Validation.Rejections {
				recorded = append(recorded, string(r.Code)+"|"+r.DecisionID)
			}
			sort.Strings(got)
			sort.Strings(recorded)
			if !slices.Equal(got, recorded) {
				t.Errorf("Beleg veraltet: %s: Ablehnungen heute %v, im Beleg %v", where, got, recorded)
			}
			if !slices.Equal(rej.Codes(), a.Validation.Codes) {
				t.Errorf("%s: Codes heute %v, im Beleg %v", where, rej.Codes(), a.Validation.Codes)
			}
			for _, r := range a.Validation.Rejections {
				if strings.TrimSpace(r.Reason) == "" {
					t.Errorf("%s: Ablehnung %s ohne Text", where, r.Code)
				}
			}
		} else if len(a.Validation.Rejections) != 0 || len(a.Validation.Codes) != 0 {
			t.Errorf("%s: angenommen, aber mit Ablehnungen im Beleg", where)
		}
		// Die rohe Ausgabe und der gestempelte Batch gehören zusammen (Go setzt nur Schemaversion, Batch-ID, Projekt, Revision).
		checkRawMatchesStamped(t, where, a, planning)
	}
}

// decodeRawBatch liest die rohe Ausgabe wie der Planer (decodeModelBatch in internal/supervisor): unbekannte Felder und
// Folgedaten sind Fehler.
func decodeRawBatch(raw []byte) (domain.DecisionBatch, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b domain.DecisionBatch
	if err := dec.Decode(&b); err != nil {
		return domain.DecisionBatch{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.DecisionBatch{}, errors.New("Daten nach dem Planungsergebnis")
	}
	return b, nil
}

// checkRawMatchesStamped liest die rohe Ausgabe zu einem Batch und vergleicht ihn mit dem gestempelten Batch des Versuchs. Gleich
// sein müssen alle Felder bis auf die von Go gesetzten (Schemaversion, Batch-ID, Projekt, Revision); diese werden aus dem
// Planungs-Snapshot und der Nummer des Versuchs erwartet. Eine von Hand geänderte Entscheidung im gestempelten Batch fällt so auf.
func checkRawMatchesStamped(t tb, where string, a attemptEvidence, planning supervisor.ProjectSnapshot) {
	t.Helper()
	if len(a.RawOutput) == 0 {
		t.Errorf("%s: keine rohe strukturierte Ausgabe", where)
		return
	}
	raw, err := decodeRawBatch(a.RawOutput)
	if err != nil {
		t.Errorf("%s: die rohe Ausgabe ist kein Planungsergebnis, es gibt aber einen gestempelten Batch: %v", where, err)
		return
	}
	// Was Go setzt, ersetzen wir auf der rohen Ausgabe durch den erwarteten Wert (Planer: stamp).
	raw.SchemaVersion = 1
	raw.BatchID = fmt.Sprintf("plan-r%d-v%d", planning.StateRevision, a.Number)
	raw.ProjectID, raw.StateRevision = planning.ProjectID, planning.StateRevision
	ds := make([]domain.Decision, len(raw.Decisions))
	for i, d := range raw.Decisions {
		d.SchemaVersion = 1
		d.ProjectID, d.StateRevision = planning.ProjectID, planning.StateRevision
		ds[i] = d
	}
	raw.Decisions = ds

	got, _ := json.Marshal(a.StampedBatch)
	want, _ := json.Marshal(raw)
	if !bytes.Equal(got, want) {
		t.Errorf("%s: der gestempelte Batch ist nicht die rohe Ausgabe des Modells mit den von Go gesetzten Feldern (Schemaversion, Batch-ID, Projekt, Revision); der Beleg wurde verändert oder ist inkonsistent. Entscheidungen im Beleg: %s, aus der rohen Ausgabe: %s", where, decisionSummary(*a.StampedBatch), decisionSummary(raw))
	}
}

// decisionSummary nennt Aktion, Ziel, Rolle, Modell und Effort je Entscheidung für die Fehlermeldung.
func decisionSummary(b domain.DecisionBatch) string {
	var parts []string
	for _, d := range b.Decisions {
		s := fmt.Sprintf("%s %s %s", d.DecisionID, d.Action, d.Target.ID)
		if d.Selection != nil {
			s += fmt.Sprintf(" (%s, %s/%s, Skills %v)", d.Selection.Role, d.Selection.Model, d.Selection.Effort, d.Selection.SkillIDs)
		}
		parts = append(parts, s)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// checkRun prüft den Lauf als Ganzes: Ergebnis, angenommener Batch und Prädikate.
func (e *evEnv) checkRun(t tb, id scenario.ID, run runEvidence, deciding **domain.DecisionBatch) {
	t.Helper()
	n := len(run.Attempts)
	if n == 0 || n > 1+supervisor.DefaultMaxReplans {
		t.Fatalf("Lauf %d: %d Versuche", run.Run, n)
	}
	last := run.Attempts[n-1]
	switch run.Outcome {
	case "accepted":
		if last.Verdict != string(supervisor.VerdictAccepted) || run.AcceptedBatch == nil || last.StampedBatch == nil {
			t.Fatalf("Lauf %d: angenommen, aber letzter Versuch %s", run.Run, last.Verdict)
		}
		for _, a := range run.Attempts[:n-1] {
			if a.Verdict == string(supervisor.VerdictAccepted) {
				t.Errorf("Lauf %d: Versuch %d war schon angenommen", run.Run, a.Number)
			}
		}
		got, _ := json.Marshal(run.AcceptedBatch)
		want, _ := json.Marshal(last.StampedBatch)
		if !bytes.Equal(got, want) {
			t.Errorf("Lauf %d: der angenommene Batch ist nicht der gestempelte des letzten Versuchs", run.Run)
		}
		*deciding = run.AcceptedBatch
		snap, err := supervisor.DecodeSnapshot(last.Snapshot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		results := scenario.EvaluateDetailed(id, *run.AcceptedBatch, snap)
		if len(results) != len(run.Predicates) || run.PredicatesMet == nil {
			t.Fatalf("Lauf %d: %d Prädikate im Beleg, heute %d", run.Run, len(run.Predicates), len(results))
		}
		allMet := true
		for i, r := range results {
			rec := run.Predicates[i]
			if rec.Name != r.Name || rec.Met != r.Met {
				t.Errorf("Beleg veraltet: Prädikat %s heute %v, im Beleg %s: %v (%v)", r.Name, r.Met, rec.Name, rec.Met, rec.Violations)
			}
			allMet = allMet && r.Met
		}
		if *run.PredicatesMet != allMet {
			t.Errorf("Lauf %d: predicates_met %v, aus den Prädikaten %v", run.Run, *run.PredicatesMet, allMet)
		}
		if id == scenario.S6 {
			first := run.Attempts[0]
			if n != 2 || first.Verdict != string(supervisor.VerdictRejected) || !slices.Contains(first.Validation.Codes, supervisor.CodeStaleRevision) || !first.Stale || first.ValidationRevision != first.SnapshotRevision+1 {
				t.Errorf("S6: erster Versuch muss stale_revision (veraltet, Revision %d→%d) sein und genau eine Neuplanung folgen (Versuche: %d)", first.SnapshotRevision, first.ValidationRevision, n)
			}
			if n == 2 {
				second := run.Attempts[1]
				if second.SnapshotRevision != first.ValidationRevision || second.Stale || second.SnapshotSHA256 == first.SnapshotSHA256 {
					t.Errorf("S6: die Neuplanung muss auf dem frischen Snapshot (Revision %d) laufen, nicht auf dem alten", first.ValidationRevision)
				}
			}
		}
	case "suspended":
		if last.Verdict == string(supervisor.VerdictAccepted) || run.SuspendReason == "" || run.AcceptedBatch != nil || run.PredicatesMet != nil {
			t.Errorf("Lauf %d: ausgesetzt, aber widersprüchlich (Urteil %s, Grund %q)", run.Run, last.Verdict, run.SuspendReason)
		}
	default:
		t.Errorf("Lauf %d: unbekanntes Ergebnis %q", run.Run, run.Outcome)
	}
}
