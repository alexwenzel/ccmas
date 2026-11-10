package profiles

import (
	"slices"
	"sort"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// ConfigSchemaVersion ist die unterstützte schema_version von agents.yaml.
const ConfigSchemaVersion = 1

// maxAgentIDLen begrenzt Agenten-IDs, damit profile_id ("<rolle>:<agent>") in die 128 Zeichen der ID passt.
const maxAgentIDLen = 64

// RoleProfile ist das Profil einer Rolle: Standardwerte und Erlaubnis. AllowedModels und
// AllowedEfforts begrenzen, was für diese Rolle überhaupt gewählt werden darf (Rollenstandard,
// Agentenwert, Wahl des Supervisors und Auftragswert); leer heißt alle konfigurierten.
type RoleProfile struct {
	Runtime        domain.Runtime  `json:"runtime" yaml:"runtime"`
	Model          string          `json:"model" yaml:"model"`
	Effort         domain.Effort   `json:"effort" yaml:"effort"`
	SkillIDs       []string        `json:"skills" yaml:"skills,omitempty"` // leer: Standardausstattung der Rolle aus dem Skill-Katalog
	PolicyProfile  string          `json:"policy" yaml:"policy"`
	AllowedModels  []string        `json:"allowed_models" yaml:"allowed_models,omitempty"`
	AllowedEfforts []domain.Effort `json:"allowed_efforts" yaml:"allowed_efforts,omitempty"`
}

// Agent ist ein individuell angelegter Agent einer Rolle. Leere Felder erben vom Rollenprofil.
type Agent struct {
	Role     domain.Role   `json:"role" yaml:"role"`
	Model    string        `json:"model,omitempty" yaml:"model,omitempty"`
	Effort   domain.Effort `json:"effort,omitempty" yaml:"effort,omitempty"`
	SkillIDs []string      `json:"skills,omitempty" yaml:"skills,omitempty"`
}

// Config ist der Inhalt von agents.yaml. Revision zählt die Änderungen der Datei (ab 1).
type Config struct {
	Revision int
	Roles    map[domain.Role]RoleProfile
	Agents   map[string]Agent
}

type configDoc struct {
	SchemaVersion int                         `yaml:"schema_version"`
	Revision      int                         `yaml:"revision"`
	Roles         map[domain.Role]RoleProfile `yaml:"roles"`
	Agents        map[string]Agent            `yaml:"agents"`
}

// ParseConfig liest agents.yaml streng und prüft die Invarianten (Validate).
func ParseConfig(raw []byte) (Config, error) {
	var doc configDoc
	if err := decodeStrict(raw, &doc); err != nil {
		return Config{}, &Error{Code: CodeInvalidConfig, Message: "agents.yaml ist ungültig: " + err.Error(), cause: err}
	}
	if doc.SchemaVersion != ConfigSchemaVersion {
		return Config{}, newErr(CodeInvalidConfig, "schema_version", "agents.yaml: schema_version %d wird nicht unterstützt (erwartet %d)", doc.SchemaVersion, ConfigSchemaVersion)
	}
	cfg := Config{Revision: doc.Revision, Roles: doc.Roles, Agents: doc.Agents}
	if cfg.Roles == nil {
		cfg.Roles = map[domain.Role]RoleProfile{}
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]Agent{}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg.Clone(), nil
}

// Validate prüft die Form ohne Wissen über Modelle und Skills: Revision, jede Rolle genau einmal
// mit gültigen Werten, gültige Agenten. Ob Modelle, Stufen und Skills zulässig sind, entscheidet
// die Auflösung (ResolveProfile) gegen Capabilities und Skill-Katalog.
func (c Config) Validate() error {
	bad := func(field, format string, args ...any) error {
		return newErr(CodeInvalidConfig, field, "agents.yaml: "+format, args...)
	}
	if c.Revision < 1 {
		return bad("revision", "revision muss eine Ganzzahl >= 1 sein (ist %d)", c.Revision)
	}
	for r := range c.Roles {
		if !r.Valid() {
			return bad("roles", "unbekannte Rolle %q", r)
		}
	}
	for _, r := range domain.Roles() {
		rp, ok := c.Roles[r]
		if !ok {
			return bad("roles", "Rolle %s hat kein Rollenprofil", r)
		}
		if err := validateRoleProfile(r, rp); err != nil {
			return bad("roles."+string(r), "Rolle %s: %v", r, err)
		}
	}
	for _, id := range c.AgentIDs() {
		a := c.Agents[id]
		if err := domain.CheckID(id); err != nil || len(id) > maxAgentIDLen {
			return bad("agents", "Agent %q hat keine gültige ID (Buchstaben, Ziffern und . _ : -, höchstens %d Zeichen)", id, maxAgentIDLen)
		}
		if err := validateAgent(a); err != nil {
			return bad("agents."+id, "Agent %s: %v", id, err)
		}
	}
	return nil
}

func validateRoleProfile(r domain.Role, rp RoleProfile) error {
	if !slices.Contains(domain.Runtimes(), rp.Runtime) {
		return errf("unbekannte Runtime %q", rp.Runtime)
	}
	if err := checkModelID(rp.Model); err != nil {
		return err
	}
	if err := checkEffortLevel(rp.Effort); err != nil {
		return err
	}
	if err := checkSkillIDs(rp.SkillIDs); err != nil {
		return err
	}
	if err := domain.CheckID(rp.PolicyProfile); err != nil {
		return errf("policy %q ist keine gültige Policy-ID", rp.PolicyProfile)
	}
	for _, m := range rp.AllowedModels {
		if err := checkModelID(m); err != nil {
			return errf("allowed_models: %v", err)
		}
	}
	if d, dup := hasDuplicates(rp.AllowedModels); dup {
		return errf("allowed_models enthält %q mehrfach", d)
	}
	for _, e := range rp.AllowedEfforts {
		if err := checkEffortLevel(e); err != nil {
			return errf("allowed_efforts: %v", err)
		}
	}
	if d, dup := hasDuplicates(rp.AllowedEfforts); dup {
		return errf("allowed_efforts enthält %q mehrfach", d)
	}
	return nil
}

func validateAgent(a Agent) error {
	if !a.Role.Valid() {
		return errf("unbekannte Rolle %q", a.Role)
	}
	if a.Model != "" {
		if err := checkModelID(a.Model); err != nil {
			return err
		}
	}
	if a.Effort != "" {
		if err := checkEffortLevel(a.Effort); err != nil {
			return err
		}
	}
	return checkSkillIDs(a.SkillIDs)
}

func checkSkillIDs(ids []string) error {
	for _, id := range ids {
		if err := domain.CheckID(id); err != nil {
			return errf("Skill-ID %q ist ungültig", id)
		}
	}
	if d, dup := hasDuplicates(ids); dup {
		return errf("Skill %q kommt mehrfach vor", d)
	}
	return nil
}

// AgentIDs liefert die IDs der individuellen Agenten aufsteigend.
func (c Config) AgentIDs() []string {
	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Clone liefert eine tiefe Kopie (kein gemeinsamer Speicher in Maps und Slices).
func (c Config) Clone() Config {
	out := Config{Revision: c.Revision, Roles: make(map[domain.Role]RoleProfile, len(c.Roles)), Agents: make(map[string]Agent, len(c.Agents))}
	for r, rp := range c.Roles {
		rp.SkillIDs = slices.Clone(rp.SkillIDs)
		rp.AllowedModels = slices.Clone(rp.AllowedModels)
		rp.AllowedEfforts = slices.Clone(rp.AllowedEfforts)
		out.Roles[r] = rp
	}
	for id, a := range c.Agents {
		a.SkillIDs = slices.Clone(a.SkillIDs)
		out.Agents[id] = a
	}
	return out
}

const configHeader = "# agents.yaml – Rollenprofile und individuelle Agenten (von ccmas verwaltet; Kommentare gehen beim Schreiben verloren).\n" +
	"# Rangfolge je Feld: Auftragswert > Agent > Wahl des Supervisors > Rolle. Keine Zugangsdaten in dieser Datei.\n"

// Marshal liefert die YAML-Form (stabil sortiert).
func (c Config) Marshal() ([]byte, error) {
	return encodeYAML(configHeader, configDoc{SchemaVersion: ConfigSchemaVersion, Revision: c.Revision, Roles: c.Roles, Agents: c.Agents})
}
