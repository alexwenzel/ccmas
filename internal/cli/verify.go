package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// Stufen der Prüfung im Sinne von „Prüfbefehl status“ (Spezifikation): vorhanden, konfiguriert,
// funktional nachgewiesen. „angemeldet“ gehört zu status; hier belegt erst der Live-Lauf, dass
// die Anmeldung trägt.
const (
	levelPresent    = "vorhanden"
	levelConfigured = "konfiguriert"
	levelFunctional = "funktional nachgewiesen"

	statusOK         = "ok"
	statusFail       = "fehler"
	statusNotChecked = "nicht geprüft"
	statusFinding    = "befund"
)

const (
	verifyPrompt         = "Antworte mit genau einem Wort: ok"
	verifyMaxTurns       = 3
	verifyMaxRuntimeSecs = 120
	verifyTimeout        = 4 * time.Minute
)

// liveExecutor führt eine Ausführung über den Runner-Adapter aus. In Tests ersetzbar, damit der
// Offline-Pfad und die Auswertung ohne Prozess und ohne Kontingent prüfbar sind.
type liveExecutor interface {
	Execute(ctx context.Context, req runner.Request) (runner.Execution, error)
}

var newLiveExecutor = func(cfg runner.Config) liveExecutor { return runner.New(cfg) }

type verifyCheck struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// liveResult ist das Ergebnis des Minimallaufs eines Modells.
type liveResult struct {
	Model          string   `json:"model"`
	Role           string   `json:"role"`
	Roles          []string `json:"roles"`
	Status         string   `json:"status"` // ok, befund, fehler
	EffortAsked    string   `json:"effort_requested"`
	EffortNote     string   `json:"effort_note"`
	EffectiveModel string   `json:"effective_model,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	ClaudeVersion  string   `json:"claude_version,omitempty"`
	AuthSource     string   `json:"auth_source,omitempty"`
	ExecutionID    string   `json:"execution_id,omitempty"`
	State          string   `json:"state,omitempty"`
	ExitCode       int      `json:"exit_code"`
	Code           string   `json:"code,omitempty"`
	Diagnosis      string   `json:"diagnosis,omitempty"`
}

type verifyReport struct {
	CheckedAt time.Time     `json:"checked_at"`
	ConfigDir string        `json:"config_dir"`
	Live      bool          `json:"live"`
	OK        bool          `json:"ok"`
	Checks    []verifyCheck `json:"checks"`
	LiveRuns  []liveResult  `json:"live_runs,omitempty"`
	Notes     []string      `json:"notes"`
}

func (r *verifyReport) add(name, level string, err error, okDetail string) {
	c := verifyCheck{Name: name, Level: level, Status: statusOK, Detail: okDetail}
	if err != nil {
		c.Status, c.Detail = statusFail, strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	r.Checks = append(r.Checks, c)
}

func (r *verifyReport) failures() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == statusFail {
			n++
		}
	}
	for _, l := range r.LiveRuns {
		if l.Status != statusOK {
			n++
		}
	}
	return n
}

func newProfileVerifyCommand(opts *profileOpts) *cobra.Command {
	var (
		live         bool
		asJSON       bool
		roleNames    []string
		claudeBinary string
	)
	cmd := &cobra.Command{
		Use:   "verify [--live] [--role <rolle> …] [--claude-binary <pfad>] [--json]",
		Short: "Profile, Runner und Claude-Binary prüfen; mit --live je Modell ein Minimallauf",
		Long: "Ohne --live: keine Agentenausführung, kein Kontingent. Geprüft werden die Konfiguration, die Auflösbarkeit " +
			"jeder Rolle und jedes Agenten, das Runner-Projekt (runners/claude_sdk), uv und das Claude-Binary. Das " +
			"Ergebnis nennt je Prüfpunkt die Stufe: vorhanden, konfiguriert oder funktional nachgewiesen (Letzteres " +
			"nur mit --live; ohne --live ist es nicht geprüft). Es wird kein Prozess gestartet.\n\n" +
			"Mit --live: je eindeutigem Modell der aufgelösten Rollenprofile genau ein Minimallauf über den Runner-Adapter " +
			"(Policy readonly, ohne Skills, kurzer Prompt, wenige Runden, niedrigster Effort). Das verbraucht Abo-Kontingent " +
			"und läuft nur, wenn --live ausdrücklich angegeben ist. Der Lauf belegt Verfügbarkeit, Anmeldung und das wirksame " +
			"Modell (Vergleich von konfiguriertem Modell und session_started.model), nicht den Effort: das SDK meldet den " +
			"wirksamen Effort nicht. Ein Abo-Limit oder eine fehlende Anmeldung wird als Diagnose gemeldet; es gibt keinen " +
			"Fallback und keine API-Abrechnung. Der Nachweis gilt nur für Zeitpunkt (checked_at) und Modell der Ausgabe.\n\n" +
			"Das Claude-Binary ergibt sich aus --claude-binary, sonst " + runner.EnvClaudeBinary + ", sonst \"claude\" im PATH " +
			"(aufgelöst zum echten Pfad). --role schränkt die Rollen ein (wiederholbar); Agenten werden mit ihrer Rolle geprüft.\n\n" +
			"Exit-Code 0, wenn alles in Ordnung ist, 1 bei einem Befund, 2 bei einem Bedienfehler.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			roles, err := verifyRoles(roleNames)
			if err != nil {
				return err
			}
			rep := &verifyReport{CheckedAt: time.Now().UTC().Truncate(time.Second), Live: live, Notes: []string{}}
			state := runOfflineChecks(opts, rep, roles, claudeBinary)
			if live {
				if rep.failures() > 0 {
					rep.Notes = append(rep.Notes, "Live-Läufe nicht gestartet: die Voraussetzungen oben sind nicht erfüllt.")
				} else {
					ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
					runLiveChecks(ctx, rep, state)
					stop()
				}
			} else {
				rep.Checks = append(rep.Checks, verifyCheck{Name: "Live-Lauf je Modell", Level: levelFunctional, Status: statusNotChecked,
					Detail: "nur mit --live (verbraucht Abo-Kontingent)"})
			}
			rep.OK = rep.failures() == 0
			if err := writeVerifyReport(cmd.OutOrStdout(), rep, asJSON); err != nil {
				return err
			}
			if !rep.OK {
				return fmt.Errorf("profile verify: %d Befund(e)", rep.failures())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "je Modell einen Minimallauf ausführen (verbraucht Abo-Kontingent)")
	cmd.Flags().StringArrayVar(&roleNames, "role", nil, "nur diese Rolle prüfen (wiederholbar); Standard: alle Rollen")
	cmd.Flags().StringVar(&claudeBinary, "claude-binary", "", "Pfad des Claude-Binarys (sonst "+runner.EnvClaudeBinary+", sonst PATH)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func verifyRoles(names []string) ([]domain.Role, error) {
	if len(names) == 0 {
		return domain.Roles(), nil
	}
	want := map[domain.Role]bool{}
	for _, n := range names {
		r, err := parseRole(n)
		if err != nil {
			return nil, err
		}
		want[r] = true
	}
	var out []domain.Role
	for _, r := range domain.Roles() {
		if want[r] {
			out = append(out, r)
		}
	}
	return out, nil
}

// verifyState hält, was der Live-Teil aus den Offline-Prüfungen braucht.
type verifyState struct {
	root     string
	claude   string
	caps     profiles.Capabilities
	resolved map[domain.Role]domain.AgentProfile
	roles    []domain.Role
}

func runOfflineChecks(opts *profileOpts, rep *verifyReport, roles []domain.Role, claudeFlag string) *verifyState {
	st := &verifyState{resolved: map[domain.Role]domain.AgentProfile{}, roles: roles}
	if !rep.Live {
		rep.Notes = append(rep.Notes, "Ohne --live wird kein Prozess gestartet und kein Kontingent verbraucht.")
	}
	rep.Notes = append(rep.Notes,
		"verify prüft Verfügbarkeit, Anmeldung und das wirksame Modell, nicht den Effort: das SDK meldet den wirksamen Effort nicht.")

	store, cfg, cfgErr := opts.loadConfigOnly()
	if store != nil {
		rep.ConfigDir = store.Dir()
	}
	rep.add("agents.yaml", levelConfigured, cfgErr, fmt.Sprintf("gültig (Revision %d)", cfg.Revision))
	var caps profiles.Capabilities
	var capsErr error
	if store != nil {
		caps, capsErr = store.LoadCapabilities()
		capsErr = codedProfileError(capsErr)
	} else {
		capsErr = errors.New("nicht geprüft (Konfigurationsverzeichnis fehlt)")
	}
	rep.add("models.yaml", levelConfigured, capsErr, fmt.Sprintf("gültig (Revision %d, %d Modelle)", caps.Revision, len(caps.ModelIDs())))
	st.caps = caps
	cat, catErr := loadCatalog(opts.root)
	rep.add("Skill-Katalog", levelPresent, catErr, "geladen")

	if cfgErr == nil && capsErr == nil && catErr == nil {
		res, err := profiles.NewResolver(cfg, cat)
		if err != nil {
			rep.add("Auflösung", levelConfigured, codedProfileError(err), "")
		} else {
			want := map[domain.Role]bool{}
			for _, r := range roles {
				want[r] = true
				p, err := res.ResolveProfile(r, "", profiles.Overrides{}, caps)
				rep.add("Rolle "+string(r), levelConfigured, codedProfileError(err),
					fmt.Sprintf("%s, Effort %s, Policy %s, Skills %s", p.Model, p.Effort, p.PolicyProfile, joinStrings(p.SkillIDs)))
				if err == nil {
					st.resolved[r] = p
				}
			}
			for _, id := range cfg.AgentIDs() {
				a := cfg.Agents[id]
				if !want[a.Role] {
					continue
				}
				p, err := res.ResolveProfile(a.Role, id, profiles.Overrides{}, caps)
				rep.add("Agent "+id, levelConfigured, codedProfileError(err),
					fmt.Sprintf("Rolle %s: %s, Effort %s, Policy %s", a.Role, p.Model, p.Effort, p.PolicyProfile))
			}
		}
	}

	root, rootErr := resolveSoftwareRoot(opts.root)
	st.root = root
	if rootErr == nil {
		dir := runner.RunnerProjectDir(root)
		for _, f := range []string{"pyproject.toml", "uv.lock"} {
			if fi, err := os.Stat(filepath.Join(dir, f)); err != nil || !fi.Mode().IsRegular() {
				rootErr = fmt.Errorf("Runner-Projekt unvollständig: %s fehlt in %s", f, dir)
				break
			}
		}
		if rootErr == nil {
			rep.add("Runner-Projekt", levelPresent, nil, dir)
		}
	}
	if rootErr != nil {
		rep.add("Runner-Projekt", levelPresent, rootErr, "")
	}
	uv, uvErr := exec.LookPath("uv")
	if uvErr != nil {
		uvErr = errors.New("uv nicht im PATH gefunden")
	}
	rep.add("uv", levelPresent, uvErr, uv)
	bin, binErr := runner.ResolveClaudeBinary(claudeFlag, lookupEnv)
	rep.add("Claude-Binary", levelPresent, binErr, bin)
	st.claude = bin
	return st
}

// runLiveChecks führt je eindeutigem Modell einen Minimallauf aus.
func runLiveChecks(ctx context.Context, rep *verifyReport, st *verifyState) {
	type group struct {
		model string
		roles []domain.Role
	}
	var groups []*group
	for _, r := range st.roles {
		p, ok := st.resolved[r]
		if !ok {
			continue
		}
		var g *group
		for _, c := range groups {
			if c.model == p.Model {
				g = c
			}
		}
		if g == nil {
			g = &group{model: p.Model}
			groups = append(groups, g)
		}
		g.roles = append(g.roles, r)
	}
	exe := newLiveExecutor(runner.Config{SoftwareRoot: st.root, MaxTurns: verifyMaxTurns})
	for _, g := range groups {
		if ctx.Err() != nil {
			rep.LiveRuns = append(rep.LiveRuns, liveResult{Model: g.model, Status: statusFail, Diagnosis: "abgebrochen"})
			continue
		}
		rep.LiveRuns = append(rep.LiveRuns, liveRun(ctx, exe, st, g.model, g.roles))
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf("Live: %d Minimallauf/-läufe über den Runner-Adapter (readonly, ohne Skills, Prompt von %d Zeichen, höchstens %d Runden).",
		len(rep.LiveRuns), len(verifyPrompt), verifyMaxTurns))
}

// verifyProfile ist das Profil des Minimallaufs: Modell und Rolle der Vorlage, aber readonly, ohne
// Skills und mit dem niedrigsten vom Modell unterstützten Effort (schont das Kontingent).
func verifyProfile(tmpl domain.AgentProfile, caps profiles.Capabilities) (domain.AgentProfile, string) {
	p := tmpl
	p.ProfileID = "verify-" + string(tmpl.Role)
	p.PolicyProfile = runner.PolicyReadonly
	p.SkillIDs = []string{}
	note := "angefordert; nicht gemessen (das SDK meldet den wirksamen Effort nicht)"
	p.Effort = domain.EffortLow
	for _, e := range domain.Efforts() {
		if caps.Supports(p.Model, e) {
			p.Effort = e
			break
		}
	}
	p.RequestedEffort, p.Fallback = p.Effort, nil
	if p.ValueSources != nil {
		vs := *p.ValueSources
		vs.Effort, vs.SkillIDs = domain.SourceOrder, domain.SourceOrder
		p.ValueSources = &vs
	}
	return p, note
}

func liveRun(ctx context.Context, exe liveExecutor, st *verifyState, model string, roles []domain.Role) liveResult {
	tmpl := st.resolved[roles[0]]
	vp, note := verifyProfile(tmpl, st.caps)
	res := liveResult{Model: model, Role: string(roles[0]), EffortAsked: string(vp.Effort), EffortNote: note}
	for _, r := range roles {
		res.Roles = append(res.Roles, string(r))
	}
	cwd, err := os.MkdirTemp("", "ccmas-verify-")
	if err != nil {
		res.Status, res.Diagnosis = statusFail, "Arbeitsverzeichnis nicht anlegbar: "+err.Error()
		return res
	}
	defer os.RemoveAll(cwd)
	runCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	execID := runner.NewID("verify")
	exec, err := exe.Execute(runCtx, runner.Request{
		ProjectID: "verify", ExecutionID: execID, Attempt: 1, WorkOrderID: "verify",
		Profile: vp, Prompt: verifyPrompt, Cwd: cwd, ClaudeBinary: st.claude, MaxRuntimeSeconds: verifyMaxRuntimeSecs,
	})
	res.ExecutionID = execID
	if err != nil {
		res.Status, res.Diagnosis = statusFail, "Lauf nicht gestartet: "+err.Error()
		return res
	}
	diagnoseLive(&res, exec, model)
	return res
}

// diagnoseLive wertet den Ausführungsdatensatz aus: nachgewiesen ist ein Lauf nur mit
// ordentlichem Ergebnis, unverändertem Modell und claude.ai-Anmeldung.
func diagnoseLive(res *liveResult, exec runner.Execution, wantModel string) {
	res.State, res.ExitCode = string(exec.State), exec.ExitCode
	if s := exec.SessionStarted; s != nil {
		res.EffectiveModel, res.SessionID, res.ClaudeVersion, res.AuthSource = s.Model, s.SessionID, s.ClaudeVersion, string(s.AuthSource)
	}
	for _, w := range exec.Warnings() {
		if w.Code == "subscription_limit" {
			res.Status, res.Code = statusFail, w.Code
			res.Diagnosis = "Abo-Limit erreicht: " + fit(w.Message, 200) + " Kein Fallback; später wiederholen."
			return
		}
	}
	if e, ok := exec.TerminalError(); ok {
		res.Status, res.Code = statusFail, e.Code
		res.Diagnosis = terminalErrorHint(e)
		return
	}
	if exec.Crashed {
		res.Status = statusFail
		res.Diagnosis = fmt.Sprintf("Runner ohne Ergebnis beendet (Exit-Code %d%s). Letzte Logzeilen: %s", exec.ExitCode, exitNote(exec), stderrTail(exec.Stderr, 3))
		return
	}
	r, ok := exec.Result()
	switch {
	case !ok:
		res.Status, res.Diagnosis = statusFail, "kein Ergebnis"
	case r.Outcome != domain.OutcomeSucceeded:
		res.Status, res.Code = statusFail, string(r.Outcome)
		res.Diagnosis = fmt.Sprintf("Lauf endete mit %s: %s", r.Outcome, fit(r.Summary, 200))
	case exec.SessionStarted == nil:
		res.Status, res.Diagnosis = statusFail, "kein session_started: das wirksame Modell ist nicht belegt"
	case exec.SessionStarted.Model != wantModel:
		res.Status, res.Code = statusFinding, "model_mismatch"
		res.Diagnosis = fmt.Sprintf("wirksames Modell %q weicht vom konfigurierten %q ab", exec.SessionStarted.Model, wantModel)
	case exec.SessionStarted.AuthSource != domain.AuthClaudeAI:
		res.Status, res.Code = statusFinding, "auth_source"
		res.Diagnosis = fmt.Sprintf("Anmeldeart %q statt claude_ai", exec.SessionStarted.AuthSource)
	case len(exec.SessionStarted.Skills) != 0 || exec.SessionStarted.SkillSnapshotID != "":
		res.Status, res.Code = statusFinding, "skills_loaded"
		res.Diagnosis = "die Sitzung meldet Skills, obwohl keine angefordert wurden"
	default:
		res.Status = statusOK
	}
}

func exitNote(e runner.Execution) string {
	if e.ExitNote == "" {
		return ""
	}
	return ", " + e.ExitNote
}

// terminalErrorHint übersetzt die terminalen Fehlercodes des Runners in eine Diagnose.
func terminalErrorHint(e domain.ErrorPayload) string {
	hint := map[string]string{
		"not_logged_in":          "Keine claude.ai-Anmeldung aktiv. Im lokalen Claude-Code-Binary mit /login anmelden; es gibt keinen API-Fallback.",
		"not_subscription_login": "Die aktive Anmeldung ist keine claude.ai-Abo-Anmeldung; CCMAS wechselt nicht zu API-Abrechnung.",
		"api_billing_detected":   "Die Sitzung würde über einen API-Schlüssel abrechnen und wurde abgebrochen; es gibt keinen API-Fallback.",
		"billing_error":          "Abrechnungsfehler der Sitzung; es gibt keinen Fallback.",
		"cli_unavailable":        "Das Claude-Binary ist nicht verwendbar.",
		"sdk_cli_incompatible":   "SDK und Claude-Binary sind nicht kompatibel.",
		"cli_version_untested":   "Die Version des Claude-Binarys ist nicht getestet.",
	}[e.Code]
	msg := fit(e.Message, 240)
	if hint != "" {
		return hint + " (" + e.Code + ": " + msg + ")"
	}
	return e.Code + ": " + msg
}

func fit(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// stderrTail gibt die letzten n nicht leeren Zeilen gekürzt zurück (Logs des Runners sind redigiert).
func stderrTail(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, fit(l, 160))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return "(keine)"
	}
	return strings.Join(lines, " | ")
}

func writeVerifyReport(out io.Writer, rep *verifyReport, asJSON bool) error {
	if asJSON {
		return writeJSON(out, rep)
	}
	fmt.Fprintf(out, "Profilprüfung %s (Konfiguration: %s)\n", rep.CheckedAt.Format(time.RFC3339), orDash(rep.ConfigDir))
	fmt.Fprintf(out, "Stufen: %s, %s, %s\n\n", levelPresent, levelConfigured, levelFunctional)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, c := range rep.Checks {
		fmt.Fprintf(tw, "[%s]\t%s\t%s\t%s\n", c.Status, c.Level, c.Name, c.Detail)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(rep.LiveRuns) > 0 {
		fmt.Fprintln(out, "\nLive-Läufe (je Modell einer):")
		tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "STATUS\tMODELL\tWIRKSAM\tEFFORT ANGEFORDERT\tSESSION\tROLLEN\tERGEBNIS")
		for _, l := range rep.LiveRuns {
			detail := l.State
			if l.Diagnosis != "" {
				detail = l.Diagnosis
			}
			fmt.Fprintf(tw, "[%s]\t%s\t%s\t%s\t%s\t%s\t%s\n", l.Status, l.Model, orDash(l.EffectiveModel), l.EffortAsked,
				orDash(l.SessionID), strings.Join(l.Roles, ","), detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)
	for _, n := range rep.Notes {
		fmt.Fprintln(out, "Hinweis:", n)
	}
	if rep.OK {
		fmt.Fprintln(out, "Ergebnis: in Ordnung")
	} else {
		fmt.Fprintf(out, "Ergebnis: %d Befund(e)\n", rep.failures())
	}
	return nil
}
