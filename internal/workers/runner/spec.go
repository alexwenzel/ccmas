package runner

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Policy-Profile, die der Adapter kennt. readonly ist der Standard; scoped_write ist ein
// Prüfprofil und braucht Config.AllowScopedWrite.
const (
	PolicyReadonly    = "readonly"
	PolicyScopedWrite = "scoped_write"
)

// ErrPolicyNotAllowed: Das Policy-Profil des Auftrags ist in diesem Stand nicht zugelassen.
var ErrPolicyNotAllowed = errors.New("Policy-Profil nicht zugelassen")

// DefaultMaxRuntimeSeconds gilt, wenn der Auftrag keine Grenze nennt.
const DefaultMaxRuntimeSeconds = 600

// Request beschreibt eine Ausführung. Das Profil ist das aufgelöste AgentProfile
// (Modell, Effort, Rolle, Policy, Skills); alles Weitere sind Angaben dieser Ausführung.
type Request struct {
	ProjectID     string
	ExecutionID   string
	Attempt       int // ab 1; Standard 1
	CorrelationID string
	WorkOrderID   string

	Profile domain.AgentProfile
	Prompt  string
	// Cwd ist das Sitzungsverzeichnis (RunSpec.cwd), nicht das Arbeitsverzeichnis des Prozesses.
	Cwd string
	// ClaudeBinary ist der absolute Pfad des Binarys (siehe ResolveClaudeBinary).
	ClaudeBinary      string
	MaxRuntimeSeconds int
	ResumeSessionID   string

	// Catalog und SnapshotDir sind nötig, sobald das Profil Skills hat: aus dem Katalog entsteht
	// der Snapshot, der in SnapshotDir geschrieben wird. Produktive Aufrufer laden den Katalog mit
	// skills.LoadChecked (Katalog, Rollenregeln und Verträge geprüft), wie es die CLI tut (skills snapshot,
	// profile verify); BuildSpec weist einen nur mit skills.Load geladenen Katalog ab (Catalog.Checked).
	Catalog     *skills.Catalog
	SnapshotDir string

	// SkillSnapshot verweist auf einen bereits geschriebenen Snapshot (skills.Snapshot.Write) und ersetzt dann
	// Catalog und SnapshotDir. Wer die Inhalte der Skills selbst braucht (zum Beispiel der Planer des Supervisors),
	// löst den Snapshot einmal auf und gibt dieselbe Referenz weiter, statt den Katalog ein zweites Mal zu lesen. Der
	// Runner prüft den Snapshot vor dem Start gegen seine snapshot.json und die Skill-IDs des Profils.
	SkillSnapshot *domain.SkillSnapshotRef

	// SystemPrompt ersetzt den Systemprompt der Sitzung (RunSpec.system_prompt). Leer: kein eigener Prompt; das SDK
	// startet die CLI dann mit --system-prompt "" (kein Standardprompt von Claude Code).
	SystemPrompt string
	// OutputSchema ist das Draft-07-Schema der strukturierten Ausgabe (RunSpec.output_schema); BuildSpec prüft es mit
	// domain.CheckOutputSchema. Mit Schema endet ein erfolgreicher Versuch nur mit Result.StructuredOutput.
	OutputSchema json.RawMessage
	// MaxTurns begrenzt die Runden dieser Ausführung (--max-turns) und geht vor Config.MaxTurns; 0: Config.MaxTurns.
	MaxTurns int
}

// NewID liefert eine neue, zufällige ID im ID-Format der Verträge ("<prefix>-<16 Hexziffern>").
func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand scheitert nie auf unterstützten Plattformen
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// checkPolicy lässt readonly zu, scoped_write nur mit ausdrücklicher Option, alles andere nie.
func (r *Runner) checkPolicy(policy string) error {
	switch policy {
	case PolicyReadonly:
		return nil
	case PolicyScopedWrite:
		if r.cfg.AllowScopedWrite {
			return nil
		}
		return fmt.Errorf("%w: %s ist ein Prüfprofil und in diesem Stand nur mit ausdrücklicher Option für Prüfläufe zulässig (ein unbeaufsichtigter Modus ist nicht freigegeben)", ErrPolicyNotAllowed, policy)
	}
	return fmt.Errorf("%w: %q (zugelassen ist readonly)", ErrPolicyNotAllowed, policy)
}

// BuildSpec baut den RunSpec aus Profil und Auftrag. Die Policy wird vor jeder anderen Wirkung
// geprüft. Hat das Profil Skills, entsteht der Snapshot (Resolve und Write) in req.SnapshotDir und
// RunSpec.SkillSnapshot verweist darauf; ohne Skills gibt es keinen Snapshot.
func (r *Runner) BuildSpec(req Request) (domain.RunSpec, error) {
	p := req.Profile
	if err := r.checkPolicy(p.PolicyProfile); err != nil {
		return domain.RunSpec{}, err
	}
	spec := domain.RunSpec{
		WorkOrderID:       req.WorkOrderID,
		ProfileID:         p.ProfileID,
		ProfileVersion:    p.Version,
		Role:              p.Role,
		Runtime:           p.Runtime,
		Model:             p.Model,
		Effort:            p.Effort,
		SkillIDs:          append([]string{}, p.SkillIDs...),
		PolicyProfile:     p.PolicyProfile,
		Cwd:               req.Cwd,
		ClaudeBinary:      req.ClaudeBinary,
		Prompt:            req.Prompt,
		ResumeSessionID:   req.ResumeSessionID,
		MaxRuntimeSeconds: req.MaxRuntimeSeconds,
		SystemPrompt:      req.SystemPrompt,
	}
	if len(req.OutputSchema) > 0 {
		if err := domain.CheckOutputSchema(req.OutputSchema); err != nil {
			return domain.RunSpec{}, fmt.Errorf("Ausgabeschema ungültig: %w", err)
		}
		spec.OutputSchema = append(json.RawMessage(nil), req.OutputSchema...)
	}
	if len(p.SkillIDs) == 0 {
		if req.SkillSnapshot != nil {
			return domain.RunSpec{}, errors.New("Skill-Snapshot angegeben, aber das Profil hat keine Skills")
		}
		return spec, nil
	}
	if req.SkillSnapshot != nil {
		ref := *req.SkillSnapshot
		spec.SkillSnapshot = &ref
		return spec, nil
	}
	if req.Catalog == nil || req.SnapshotDir == "" {
		return domain.RunSpec{}, errors.New("das Profil hat Skills: Skill-Katalog und Snapshot-Verzeichnis sind erforderlich")
	}
	if !req.Catalog.Checked() {
		return domain.RunSpec{}, errors.New("der Skill-Katalog ist nicht geprüft geladen: Snapshots für Ausführungen entstehen nur aus einem Katalog, der mit skills.LoadChecked geladen ist (Rollenregeln und Workflow-Verträge geprüft)")
	}
	ref, err := writeSnapshot(req.Catalog, p.Role, p.SkillIDs, req.SnapshotDir)
	if err != nil {
		return domain.RunSpec{}, err
	}
	spec.SkillSnapshot = &ref
	return spec, nil
}

// writeSnapshot löst die Skills der Rolle auf und schreibt den Snapshot in dir. Der Pfad ist
// kanonisch (ohne Symlinks), wie ihn der Runner im Init-Frame erwartet.
func writeSnapshot(cat *skills.Catalog, role domain.Role, ids []string, dir string) (domain.SkillSnapshotRef, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.SkillSnapshotRef{}, fmt.Errorf("Snapshot-Verzeichnis %s nicht anlegbar: %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return domain.SkillSnapshotRef{}, fmt.Errorf("Snapshot-Verzeichnis %s nicht auflösbar: %w", dir, err)
	}
	snap, err := cat.Resolve(role, ids)
	if err != nil {
		return domain.SkillSnapshotRef{}, fmt.Errorf("Skill-Snapshot nicht auflösbar: %w", err)
	}
	want := slices.Clone(ids)
	slices.Sort(want)
	if got := snap.SkillIDs(); !slices.Equal(got, want) {
		return domain.SkillSnapshotRef{}, fmt.Errorf("Snapshot enthält %v statt der angeforderten Skills %v", got, want)
	}
	path, err := snap.Write(real)
	if err != nil {
		return domain.SkillSnapshotRef{}, fmt.Errorf("Skill-Snapshot nicht schreibbar: %w", err)
	}
	return domain.SkillSnapshotRef{SnapshotID: snap.ID(), Path: path}, nil
}

func specRecord(spec domain.RunSpec) SpecRecord {
	sum := sha256.Sum256([]byte(spec.Prompt))
	sysSum := sha256.Sum256([]byte(spec.SystemPrompt))
	schemaSum := sha256.Sum256(spec.OutputSchema)
	rec := SpecRecord{
		WorkOrderID: spec.WorkOrderID, ProfileID: spec.ProfileID, ProfileVersion: spec.ProfileVersion,
		Role: spec.Role, Runtime: spec.Runtime, Model: spec.Model, Effort: spec.Effort,
		SkillIDs: append([]string{}, spec.SkillIDs...), PolicyProfile: spec.PolicyProfile,
		Cwd: spec.Cwd, ClaudeBinary: spec.ClaudeBinary,
		PromptBytes: len(spec.Prompt), PromptSHA256: hex.EncodeToString(sum[:]),
		ResumeSessionID: spec.ResumeSessionID, MaxRuntimeSeconds: spec.MaxRuntimeSeconds,
	}
	if spec.SystemPrompt != "" {
		rec.SystemPromptBytes, rec.SystemPromptSHA256 = len(spec.SystemPrompt), hex.EncodeToString(sysSum[:])
	}
	if len(spec.OutputSchema) > 0 {
		rec.OutputSchemaBytes, rec.OutputSchemaSHA256 = len(spec.OutputSchema), hex.EncodeToString(schemaSum[:])
	}
	if spec.SkillSnapshot != nil {
		ref := *spec.SkillSnapshot
		rec.SkillSnapshot = &ref
	}
	return rec
}
