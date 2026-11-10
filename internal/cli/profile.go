package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// EnvConfigDir ist die Umgebungsvariable, die das Konfigurationsverzeichnis festlegt.
const EnvConfigDir = "CCMAS_CONFIG_DIR"

const groupProfileCommands = "profile-commands"

// configDirRel ist der Standardort unter dem CCMAS-Softwareordner (".ccmas/" ist ignoriert).
var configDirRel = filepath.Join(".ccmas", "config")

// resolveConfigDir ermittelt das Konfigurationsverzeichnis: --config-dir, dann CCMAS_CONFIG_DIR,
// dann <CCMAS-Softwareordner>/.ccmas/config (Softwareordner wie bei "ccmas skills").
func resolveConfigDir(flagDir, flagRoot string) (string, error) {
	dir := flagDir
	if dir == "" {
		dir = lookupEnv(EnvConfigDir)
	}
	if dir == "" {
		root, err := resolveSoftwareRoot(flagRoot)
		if err != nil {
			return "", fmt.Errorf("Konfigurationsverzeichnis nicht bestimmbar: %w; alternativ --config-dir oder %s angeben", err, EnvConfigDir)
		}
		dir = filepath.Join(root, configDirRel)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("Konfigurationsverzeichnis %q nicht auflösbar: %w", dir, err)
	}
	return abs, nil
}

// profileOpts sind die gemeinsamen Optionen von "profile" und "agent".
type profileOpts struct {
	configDir string
	root      string
}

func (o *profileOpts) addFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&o.configDir, "config-dir", "", "Konfigurationsverzeichnis (sonst "+EnvConfigDir+", sonst <Softwareordner>/.ccmas/config)")
	cmd.PersistentFlags().StringVar(&o.root, "root", "", "CCMAS-Softwareordner (sonst "+EnvHome+", sonst Verzeichnis über dem Binary)")
}

func (o *profileOpts) store() (*profiles.Store, error) {
	dir, err := resolveConfigDir(o.configDir, o.root)
	if err != nil {
		return nil, err
	}
	return profiles.NewStore(dir), nil
}

// profileSession hält Konfiguration, Fähigkeiten und Skill-Katalog eines prüfenden Befehls.
type profileSession struct {
	store *profiles.Store
	cfg   profiles.Config
	caps  profiles.Capabilities
	cat   *skills.Catalog
}

// loadConfigOnly liest nur agents.yaml (lesende Befehle brauchen weder Modelle noch Skills).
func (o *profileOpts) loadConfigOnly() (*profiles.Store, profiles.Config, error) {
	st, err := o.store()
	if err != nil {
		return nil, profiles.Config{}, err
	}
	cfg, err := st.LoadConfig()
	return st, cfg, codedProfileError(err)
}

// loadSession liest alles, was ein prüfender Befehl braucht.
func (o *profileOpts) loadSession() (*profileSession, error) {
	st, cfg, err := o.loadConfigOnly()
	if err != nil {
		return nil, err
	}
	caps, err := st.LoadCapabilities()
	if err != nil {
		return nil, codedProfileError(err)
	}
	cat, err := loadCatalog(o.root)
	if err != nil {
		return nil, err
	}
	return &profileSession{store: st, cfg: cfg, caps: caps, cat: cat}, nil
}

// check löst die Rolle (und den Agenten) ohne Überschreibungen auf: Dasselbe Ergebnis wie
// später bei einer Ausführung. Ein Fehler bedeutet, dass nichts gespeichert werden darf.
func (s *profileSession) check(cfg profiles.Config, role domain.Role, agentID string) (domain.AgentProfile, error) {
	r, err := profiles.NewResolver(cfg, s.cat)
	if err != nil {
		return domain.AgentProfile{}, codedProfileError(err)
	}
	p, err := r.ResolveProfile(role, agentID, profiles.Overrides{}, s.caps)
	return p, codedProfileError(err)
}

// checkAgentsOfRole löst jeden vorhandenen Agenten der Rolle gegen die geänderte Konfiguration
// auf: ein Rollenstandard, der einen erbenden Agenten unauflösbar macht (zum Beispiel ein anderes
// Modell, das dessen Effort nicht unterstützt), darf nicht gespeichert werden. Der Fehler nennt
// den Agenten.
func (s *profileSession) checkAgentsOfRole(cfg profiles.Config, role domain.Role) error {
	for _, id := range cfg.AgentIDs() {
		if cfg.Agents[id].Role != role {
			continue
		}
		if _, err := s.check(cfg, role, id); err != nil {
			return fmt.Errorf("Agent %q der Rolle %s wäre danach nicht mehr auflösbar: %w", id, role, err)
		}
	}
	return nil
}

// codedProfileError zeigt Fehler des Pakets profiles (und die des Skill-Pakets) mit ihrem
// stabilen Code in eckigen Klammern; Exit-Code ExitError.
func codedProfileError(err error) error {
	if err == nil {
		return nil
	}
	var pe *profiles.Error
	if errors.As(err, &pe) {
		return &codedError{lines: []string{fmt.Sprintf("%s [%s]", pe.Message, pe.Code)}}
	}
	return withCodes(err)
}

func parseEffort(s string) (domain.Effort, error) {
	e := domain.Effort(s)
	if !e.Valid() {
		names := make([]string, 0, len(domain.Efforts()))
		for _, v := range domain.Efforts() {
			names = append(names, string(v))
		}
		return "", usageErrorf("unbekannte Effort-Stufe %q (erlaubt: %s)", s, strings.Join(names, ", "))
	}
	return e, nil
}

// skillValues prüft wiederholbare --skill-Werte; leere Angaben sind ein Bedienfehler.
func skillValues(vals []string) ([]string, error) {
	for _, v := range vals {
		if strings.TrimSpace(v) == "" {
			return nil, usageErrorf("--skill braucht eine Skill-ID")
		}
	}
	return vals, nil
}

func joinStrings(in []string) string {
	if len(in) == 0 {
		return "-"
	}
	return strings.Join(in, ",")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

const inheritNote = "leer = erbt vom Rollenprofil"

// ---- profile -------------------------------------------------------------------

func newProfileCommand() *cobra.Command {
	var opts profileOpts
	cmd := &cobra.Command{
		Use:     "profile",
		GroupID: groupAvailable,
		Short:   "Rollenprofile (Modell, Effort, Skills, Policy) verwalten",
		Long: "Rollenprofile legen je Rolle Modell, Effort, Skills und Policy-Profil fest. Sie liegen in " +
			"agents.yaml, die Fähigkeiten der Modelle (unterstützte Effort-Stufen, genehmigte Fallbacks) in " +
			"models.yaml, beide im Konfigurationsverzeichnis. Modell-IDs stehen nur dort, nie im Programm.\n\n" +
			"Rangfolge je Feld einzeln: ausdrücklicher Auftragswert > individueller Agentenwert > Wahl des " +
			"Supervisors > Rollenstandard, alles innerhalb der Rollenerlaubnis. Ein vom Modell nicht unterstützter " +
			"Effort wird abgelehnt (unsupported_effort), außer models.yaml genehmigt die Reduktion ausdrücklich; " +
			"sie steht dann sichtbar im aufgelösten Profil. Der wirksame Effort ist abgeleitet, nicht gemessen.\n\n" +
			"Das Konfigurationsverzeichnis ergibt sich aus --config-dir, sonst " + EnvConfigDir + ", sonst " +
			"<CCMAS-Softwareordner>/.ccmas/config. Neue Werte gelten nur für neue Ausführungen.\n\n" +
			"Exit-Codes: 0 Erfolg, 1 Befund oder Laufzeitfehler (auch unzulässige Werte; dann wird nichts " +
			"gespeichert), 2 Bedienfehler.",
		Args: unknownCommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	opts.addFlags(cmd)
	cmd.AddGroup(&cobra.Group{ID: groupProfileCommands, Title: "Befehle:"})
	cmd.AddCommand(
		newProfileInitCommand(&opts),
		newProfileListCommand(&opts),
		newProfileShowCommand(&opts),
		newProfileSetCommand(&opts),
		newProfileResolveCommand(&opts),
	)
	for _, c := range cmd.Commands() {
		c.GroupID = groupProfileCommands
		localizeSubcommandHelp(c)
	}
	return cmd
}

func newProfileInitCommand(opts *profileOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Startprofile aus den Beispielkonfigurationen anlegen",
		Long: "Legt models.yaml und agents.yaml im Konfigurationsverzeichnis aus config/models.example.yaml und " +
			"config/agents.example.yaml an (Startprofile: Supervisor und Reviewer opus/high, Implementierung und Konzept " +
			"sonnet/high, Dokumentation sonnet/medium). Eine vorhandene Datei wird nie überschrieben.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := opts.store()
			if err != nil {
				return err
			}
			res, err := st.Init(config.ModelsExample(), config.AgentsExample())
			if err != nil {
				return codedProfileError(err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Konfigurationsverzeichnis: %s\n", st.Dir())
			for _, r := range res {
				if r.Created {
					fmt.Fprintf(out, "%s: angelegt\n", r.File)
				} else {
					fmt.Fprintf(out, "%s: bereits vorhanden, nicht überschrieben\n", r.File)
				}
			}
			return nil
		},
	}
}

// roleView ist die Darstellung eines Rollenprofils in der JSON-Ausgabe.
type roleView struct {
	Role domain.Role `json:"role"`
	profiles.RoleProfile
}

func roleViews(cfg profiles.Config) []roleView {
	out := make([]roleView, 0, len(cfg.Roles))
	for _, r := range domain.Roles() {
		rp := cfg.Roles[r]
		rp.SkillIDs = nonNilList(rp.SkillIDs)
		rp.AllowedModels = nonNilList(rp.AllowedModels)
		rp.AllowedEfforts = nonNilEfforts(rp.AllowedEfforts)
		out = append(out, roleView{Role: r, RoleProfile: rp})
	}
	return out
}

func nonNilList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilEfforts(in []domain.Effort) []domain.Effort {
	if in == nil {
		return []domain.Effort{}
	}
	return in
}

func newProfileListCommand(opts *profileOpts) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Alle Rollenprofile auflisten",
		Long:  "Listet die in agents.yaml gespeicherten Rollenprofile. Skills leer (-) heißt Standardausstattung der Rolle aus dem Skill-Katalog.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, cfg, err := opts.loadConfigOnly()
			if err != nil {
				return err
			}
			views := roleViews(cfg)
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"config_dir": st.Dir(), "revision": cfg.Revision, "roles": views})
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ROLLE\tMODELL\tEFFORT\tPOLICY\tSKILLS\tERLAUBTE MODELLE\tERLAUBTE EFFORTS")
			for _, v := range views {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.Role, v.Model, v.Effort, v.PolicyProfile,
					joinStrings(v.SkillIDs), joinStrings(v.AllowedModels), join(v.AllowedEfforts))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nKonfiguration: %s (Revision %d)\n", st.Dir(), cfg.Revision)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func newProfileShowCommand(opts *profileOpts) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <rolle>",
		Short: "Das gespeicherte Rollenprofil und seine Agenten anzeigen",
		Long:  "Zeigt das gespeicherte Rollenprofil samt Erlaubnis und die Agenten dieser Rolle. Das aufgelöste Profil einer Ausführung zeigt 'profile resolve'.",
		Args:  exactArgs(1, "eine Rolle"),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := parseRole(args[0])
			if err != nil {
				return err
			}
			st, cfg, err := opts.loadConfigOnly()
			if err != nil {
				return err
			}
			rp := roleViews(cfg)[roleIndex(role)]
			var agents []string
			for _, id := range cfg.AgentIDs() {
				if cfg.Agents[id].Role == role {
					agents = append(agents, id)
				}
			}
			agents = nonNilList(agents)
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"config_dir": st.Dir(), "revision": cfg.Revision, "profile": rp, "agents": agents})
			}
			fmt.Fprintf(out, "Rolle:              %s\n", rp.Role)
			fmt.Fprintf(out, "Runtime:            %s\n", rp.Runtime)
			fmt.Fprintf(out, "Modell:             %s\n", rp.Model)
			fmt.Fprintf(out, "Effort:             %s\n", rp.Effort)
			fmt.Fprintf(out, "Policy:             %s\n", rp.PolicyProfile)
			fmt.Fprintf(out, "Skills:             %s\n", skillsOrDefault(rp.SkillIDs))
			fmt.Fprintf(out, "Erlaubte Modelle:   %s\n", orDefault(joinStrings(rp.AllowedModels), "alle konfigurierten"))
			fmt.Fprintf(out, "Erlaubte Efforts:   %s\n", orDefault(join(rp.AllowedEfforts), "alle konfigurierten"))
			fmt.Fprintf(out, "Agenten:            %s\n", joinStrings(agents))
			fmt.Fprintf(out, "Konfiguration:      %s (Revision %d)\n", st.Dir(), cfg.Revision)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func roleIndex(r domain.Role) int {
	for i, v := range domain.Roles() {
		if v == r {
			return i
		}
	}
	return 0
}

func skillsOrDefault(ids []string) string {
	if len(ids) == 0 {
		return "Standardausstattung der Rolle"
	}
	return strings.Join(ids, ",")
}

func orDefault(s, def string) string {
	if s == "-" {
		return def
	}
	return s
}

func newProfileSetCommand(opts *profileOpts) *cobra.Command {
	var (
		model, effort, policy string
		skillIDs              []string
		clearSkills           bool
	)
	cmd := &cobra.Command{
		Use:   "set <rolle> [--model M] [--effort E] [--skill ID … | --clear-skills] [--policy P]",
		Short: "Standardwerte eines Rollenprofils ändern",
		Long: "Ändert Modell, Effort, Skills oder Policy-Profil einer Rolle. Vor dem Speichern wird das Ergebnis wie bei " +
			"einer Ausführung aufgelöst und geprüft (bekanntes Modell, vom Modell unterstützter Effort oder genehmigter " +
			"Fallback, freigegebene und für die Rolle erlaubte Skills, bekanntes Policy-Profil); ebenso jeder vorhandene " +
			"Agent dieser Rolle, der den geänderten Wert erbt (der Fehler nennt den Agenten). Eine unzulässige " +
			"Kombination endet mit Exit-Code 1; es wird nichts geschrieben. --skill ersetzt die Skill-Liste " +
			"(wiederholbar), --clear-skills stellt die Standardausstattung der Rolle aus dem Skill-Katalog wieder her. " +
			"Laufende Ausführungen behalten ihr Profil; die neuen Werte gelten nur für neue Ausführungen.",
		Args: exactArgs(1, "eine Rolle"),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := parseRole(args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if !f.Changed("model") && !f.Changed("effort") && !f.Changed("skill") && !clearSkills && !f.Changed("policy") {
				return usageErrorf("%s braucht mindestens eine Änderung (--model, --effort, --skill, --clear-skills oder --policy)", cmd.CommandPath())
			}
			if f.Changed("skill") && clearSkills {
				return usageErrorf("--skill und --clear-skills schließen sich aus")
			}
			if f.Changed("model") && strings.TrimSpace(model) == "" {
				return usageErrorf("--model braucht eine Modell-ID")
			}
			if f.Changed("policy") && strings.TrimSpace(policy) == "" {
				return usageErrorf("--policy braucht ein Policy-Profil")
			}
			var eff domain.Effort
			if f.Changed("effort") {
				if eff, err = parseEffort(effort); err != nil {
					return err
				}
			}
			ids, err := skillValues(skillIDs)
			if err != nil {
				return err
			}

			s, err := opts.loadSession()
			if err != nil {
				return err
			}
			next := s.cfg.Clone()
			rp := next.Roles[role]
			if f.Changed("model") {
				rp.Model = model
			}
			if f.Changed("effort") {
				rp.Effort = eff
			}
			if f.Changed("policy") {
				rp.PolicyProfile = policy
			}
			if f.Changed("skill") {
				rp.SkillIDs = ids
			}
			if clearSkills {
				rp.SkillIDs = nil
			}
			next.Roles[role] = rp
			resolved, err := s.check(next, role, "")
			if err != nil {
				return err
			}
			if err := s.checkAgentsOfRole(next, role); err != nil {
				return err
			}
			saved, err := s.store.SaveConfig(next)
			if err != nil {
				return codedProfileError(err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Rolle %s gespeichert (%s, Revision %d)\n", role, profiles.AgentsFile, saved.Revision)
			printResolved(out, resolved)
			fmt.Fprintln(out, "Die neuen Werte gelten nur für neue Ausführungen.")
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Modell-ID (muss in models.yaml konfiguriert sein)")
	cmd.Flags().StringVar(&effort, "effort", "", "Effort-Stufe: low, medium, high, xhigh oder max")
	cmd.Flags().StringArrayVar(&skillIDs, "skill", nil, "Skill-ID (wiederholbar); ersetzt die bisherige Skill-Liste")
	cmd.Flags().BoolVar(&clearSkills, "clear-skills", false, "Standardausstattung der Rolle aus dem Skill-Katalog wiederherstellen")
	cmd.Flags().StringVar(&policy, "policy", "", "Policy-Profil (readonly oder scoped_write)")
	return cmd
}

// printResolved gibt das aufgelöste Profil kurz aus.
func printResolved(out io.Writer, p domain.AgentProfile) {
	fmt.Fprintf(out, "  Modell: %s, Effort: %s, Policy: %s, Skills: %s\n", p.Model, p.Effort, p.PolicyProfile, joinStrings(p.SkillIDs))
	if p.Fallback != nil {
		fmt.Fprintf(out, "  Effort reduziert: angefordert %s, wirksam %s (genehmigter Fallback: %s)\n", p.Fallback.From, p.Fallback.To, p.Fallback.Source)
	}
}

func newProfileResolveCommand(opts *profileOpts) *cobra.Command {
	var (
		asJSON  bool
		role    string
		agent   string
		model   string
		effort  string
		skillID []string
	)
	cmd := &cobra.Command{
		Use:   "resolve --role <rolle> [--agent <id>] [--model M] [--effort E] [--skill ID …]",
		Short: "Das aufgelöste Profil einer Ausführung anzeigen",
		Long: "Löst Rolle (und optional einen individuellen Agenten) mit der Rangfolge Auftragswert > Agentenwert > " +
			"Rollenstandard auf und zeigt Modell, angeforderten und wirksamen Effort, eventuelle genehmigte Reduktion, " +
			"Skills, Policy, Version und die Herkunft jedes Werts (value_sources). --model, --effort und --skill sind " +
			"ausdrückliche Auftragswerte. Der Effort ist abgeleitet (Konfiguration und dokumentierte Regel), nicht " +
			"gemessen. Es wird nichts gespeichert.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role == "" {
				return usageErrorf("--role ist erforderlich")
			}
			r, err := parseRole(role)
			if err != nil {
				return err
			}
			var ov profiles.Overrides
			ov.Order.Model = model
			if effort != "" {
				if ov.Order.Effort, err = parseEffort(effort); err != nil {
					return err
				}
			}
			if ov.Order.SkillIDs, err = skillValues(skillID); err != nil {
				return err
			}
			s, err := opts.loadSession()
			if err != nil {
				return err
			}
			res, err := profiles.NewResolver(s.cfg, s.cat)
			if err != nil {
				return codedProfileError(err)
			}
			p, err := res.ResolveProfile(r, agent, ov, s.caps)
			if err != nil {
				return codedProfileError(err)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, p)
			}
			fmt.Fprintf(out, "Profil:             %s (Version %d)\n", p.ProfileID, p.Version)
			fmt.Fprintf(out, "Rolle:              %s\n", p.Role)
			fmt.Fprintf(out, "Runtime:            %s\n", p.Runtime)
			fmt.Fprintf(out, "Modell:             %s (Herkunft: %s)\n", p.Model, p.ValueSources.Model)
			fmt.Fprintf(out, "Effort angefordert: %s (Herkunft: %s)\n", p.RequestedEffort, p.ValueSources.Effort)
			fmt.Fprintf(out, "Effort wirksam:     %s (abgeleitet, nicht gemessen)\n", p.Effort)
			if p.Fallback != nil {
				fmt.Fprintf(out, "Fallback:           %s von %s auf %s – %s\n", p.Fallback.Kind, p.Fallback.From, p.Fallback.To, p.Fallback.Source)
			}
			fmt.Fprintf(out, "Skills:             %s (Herkunft: %s)\n", joinStrings(p.SkillIDs), p.ValueSources.SkillIDs)
			fmt.Fprintf(out, "Policy:             %s\n", p.PolicyProfile)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Rolle (erforderlich)")
	cmd.Flags().StringVar(&agent, "agent", "", "individueller Agent dieser Rolle")
	cmd.Flags().StringVar(&model, "model", "", "Modell-ID als ausdrücklicher Auftragswert")
	cmd.Flags().StringVar(&effort, "effort", "", "Effort-Stufe als ausdrücklicher Auftragswert")
	cmd.Flags().StringArrayVar(&skillID, "skill", nil, "Skill-ID als ausdrücklicher Auftragswert (wiederholbar)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt (das AgentProfile)")
	return cmd
}
