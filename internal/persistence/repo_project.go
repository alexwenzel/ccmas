package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// ---- Projekt ------------------------------------------------------------------------------------------------

// ProjectRecord ist ein Projekt mit seiner Zustandsrevision.
type ProjectRecord struct {
	Config        domain.ProjectConfig
	StateRevision int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// validProjectConfig prüft die Konfiguration gegen Schema und Invarianten und liefert ihre kanonische Form.
func validProjectConfig(cfg domain.ProjectConfig) (domain.ProjectConfig, string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, "", wrapf(CodeInternal, err, "Projektkonfiguration")
	}
	parsed, err := domain.ParseProjectConfig(raw)
	if err != nil {
		return cfg, "", invalid(err)
	}
	text, err := jsonText(parsed)
	return parsed, text, err
}

// CreateProject legt ein Projekt an. Die Revision beginnt bei 0; die anlegende Transaktion erhöht sie nicht.
func (t *Tx) CreateProject(cfg domain.ProjectConfig) (ProjectRecord, error) {
	var out ProjectRecord
	err := t.write(func() error {
		parsed, text, err := validProjectConfig(cfg)
		if err != nil {
			return err
		}
		at := fmtTime(t.at)
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO projects (project_id, config_json, state_revision, created_at, updated_at)
			VALUES (?, ?, 0, ?, ?)`, parsed.ProjectID, text, at, at); err != nil {
			return err
		}
		t.revs[parsed.ProjectID], t.bumped[parsed.ProjectID] = 0, true
		out = ProjectRecord{Config: parsed, StateRevision: 0, CreatedAt: t.at, UpdatedAt: t.at}
		return t.emit(parsed.ProjectID, eventSpec{level: domain.AuditLevelConfig, kind: domain.AuditProjectCreated,
			entityType: "project", entityID: parsed.ProjectID,
			details: map[string]string{"max_workers": itoa(int64(parsed.MaxWorkers)), "default_mode": string(parsed.DefaultMode)}})
	})
	return out, err
}

// UpdateProjectConfig ersetzt die Konfiguration eines Projekts. Eine unveränderte Konfiguration ist keine Änderung.
func (t *Tx) UpdateProjectConfig(cfg domain.ProjectConfig) error {
	return t.write(func() error {
		parsed, text, err := validProjectConfig(cfg)
		if err != nil {
			return err
		}
		var old string
		err = t.q.QueryRowContext(t.ctx, "SELECT config_json FROM projects WHERE project_id = ?", parsed.ProjectID).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Projekt %q existiert nicht", parsed.ProjectID)
		}
		if err != nil {
			return err
		}
		if old == text {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE projects SET config_json = ?, updated_at = ? WHERE project_id = ?",
			text, fmtTime(t.at), parsed.ProjectID); err != nil {
			return err
		}
		return t.change(parsed.ProjectID, eventSpec{level: domain.AuditLevelConfig, kind: domain.AuditProjectConfigUpdated,
			entityType: "project", entityID: parsed.ProjectID})
	})
}

// Project liefert ein Projekt.
func (r *reader) Project(id string) (ProjectRecord, error) {
	var (
		text, created, updated string
		rec                    ProjectRecord
	)
	err := r.q.QueryRowContext(r.ctx, "SELECT config_json, state_revision, created_at, updated_at FROM projects WHERE project_id = ?", id).
		Scan(&text, &rec.StateRevision, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, errf(CodeNotFound, "Projekt %q existiert nicht", id)
	}
	if err != nil {
		return rec, mapErr(err)
	}
	if rec.Config, err = domain.ParseProjectConfig([]byte(text)); err != nil {
		return rec, wrapf(CodeInternal, err, "gespeicherte Projektkonfiguration")
	}
	if rec.CreatedAt, err = scanTime(created); err != nil {
		return rec, err
	}
	rec.UpdatedAt, err = scanTime(updated)
	return rec, err
}

// Projects liefert alle Projekte nach ID.
func (r *reader) Projects() ([]ProjectRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT project_id FROM projects ORDER BY project_id")
	if err != nil {
		return nil, mapErr(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	var out []ProjectRecord
	for _, id := range ids {
		p, err := r.Project(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Revision liefert die Zustandsrevision des Projekts.
func (r *reader) Revision(projectID string) (int64, error) {
	var rev int64
	err := r.q.QueryRowContext(r.ctx, "SELECT state_revision FROM projects WHERE project_id = ?", projectID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errf(CodeNotFound, "Projekt %q existiert nicht", projectID)
	}
	return rev, mapErr(err)
}

// ---- Profilversionen ----------------------------------------------------------------------------------------

// RecordProfile speichert eine Profilversion unveränderlich. Dieselbe Version mit demselben Inhalt ist ein No-op;
// mit anderem Inhalt ein Konflikt. Ein Profil ist ein Verweisziel für Ausführungen und verändert den
// Planungszustand nicht (Journaleintrag ohne Revisionsänderung).
func (t *Tx) RecordProfile(projectID string, p domain.AgentProfile) error {
	return t.write(func() error {
		if err := checkIDs(projectID); err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return wrapf(CodeInternal, err, "Profil")
		}
		if err := domain.Validate(domain.TargetAgentProfile, raw); err != nil {
			return invalid(err)
		}
		sum := sha256Hex(raw)
		var old string
		err = t.q.QueryRowContext(t.ctx, "SELECT sha256 FROM profile_versions WHERE project_id = ? AND profile_id = ? AND version = ?",
			projectID, p.ProfileID, p.Version).Scan(&old)
		switch {
		case err == nil && old == sum:
			return nil
		case err == nil:
			return errf(CodeConflict, "Profil %s Version %d existiert mit anderem Inhalt; Profilversionen sind unveränderlich", p.ProfileID, p.Version)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO profile_versions (project_id, profile_id, version, role, profile_json, sha256, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, projectID, p.ProfileID, p.Version, string(p.Role), string(raw), sum, fmtTime(t.at)); err != nil {
			return err
		}
		return t.journal(projectID, eventSpec{level: domain.AuditLevelConfig, kind: domain.AuditProfileRecorded, entityType: "profile",
			entityID: p.ProfileID + "#" + itoa(int64(p.Version)),
			details:  map[string]string{"role": string(p.Role), "model": p.Model, "effort": string(p.Effort)}})
	})
}

// Profile liefert eine Profilversion.
func (r *reader) Profile(projectID, profileID string, version int) (domain.AgentProfile, error) {
	var raw string
	err := r.q.QueryRowContext(r.ctx, "SELECT profile_json FROM profile_versions WHERE project_id = ? AND profile_id = ? AND version = ?",
		projectID, profileID, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AgentProfile{}, errf(CodeNotFound, "Profil %s Version %d existiert nicht", profileID, version)
	}
	if err != nil {
		return domain.AgentProfile{}, mapErr(err)
	}
	p, err := domain.ParseAgentProfile([]byte(raw))
	if err != nil {
		return p, wrapf(CodeInternal, err, "gespeichertes Profil")
	}
	return p, nil
}

// LatestProfile liefert die höchste Version eines Profils.
func (r *reader) LatestProfile(projectID, profileID string) (domain.AgentProfile, error) {
	var version sql.NullInt64
	err := r.q.QueryRowContext(r.ctx, "SELECT max(version) FROM profile_versions WHERE project_id = ? AND profile_id = ?",
		projectID, profileID).Scan(&version)
	if err != nil {
		return domain.AgentProfile{}, mapErr(err)
	}
	if !version.Valid {
		return domain.AgentProfile{}, errf(CodeNotFound, "Profil %s existiert nicht", profileID)
	}
	return r.Profile(projectID, profileID, int(version.Int64))
}

// Profiles liefert alle Profilversionen nach Profil-ID und Version.
func (r *reader) Profiles(projectID string) ([]domain.AgentProfile, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT profile_json FROM profile_versions WHERE project_id = ? ORDER BY profile_id, version", projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.AgentProfile
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, mapErr(err)
		}
		p, err := domain.ParseAgentProfile([]byte(raw))
		if err != nil {
			return nil, wrapf(CodeInternal, err, "gespeichertes Profil")
		}
		out = append(out, p)
	}
	return out, mapErr(rows.Err())
}

// ---- Skill-Snapshots ----------------------------------------------------------------------------------------

// SkillSnapshotRecord ist ein gespeicherter Skill-Snapshot mit dem Verzeichnis, in das er geschrieben wurde.
type SkillSnapshotRecord struct {
	Snapshot   domain.SkillSnapshot
	Path       string // kanonischer absoluter Pfad des Snapshot-Verzeichnisses
	RecordedAt time.Time
}

// RecordSkillSnapshot speichert einen Skill-Snapshot unveränderlich (Vertrag skill-snapshot-v1, geprüft vor dem
// Schreiben). Derselbe Snapshot am selben Pfad ist ein No-op, jede Abweichung ein Konflikt.
func (t *Tx) RecordSkillSnapshot(projectID string, snap domain.SkillSnapshot, path string) error {
	return t.write(func() error {
		if err := checkIDs(projectID); err != nil {
			return err
		}
		if err := domain.CheckCanonicalPath(path); err != nil {
			return invalid(err)
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			return wrapf(CodeInternal, err, "Skill-Snapshot")
		}
		parsed, err := domain.ParseSkillSnapshot(raw)
		if err != nil {
			return invalid(err)
		}
		canon, err := json.Marshal(parsed)
		if err != nil {
			return wrapf(CodeInternal, err, "Skill-Snapshot")
		}
		var oldJSON, oldPath string
		err = t.q.QueryRowContext(t.ctx, "SELECT snapshot_json, path FROM skill_snapshots WHERE project_id = ? AND snapshot_id = ?",
			projectID, parsed.SnapshotID).Scan(&oldJSON, &oldPath)
		switch {
		case err == nil && oldJSON == string(canon) && oldPath == path:
			return nil
		case err == nil:
			return errf(CodeConflict, "Skill-Snapshot %s existiert mit anderem Inhalt oder Pfad; Snapshots sind unveränderlich", parsed.SnapshotID)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO skill_snapshots
			(project_id, snapshot_id, role, manifest_sha256, path, snapshot_json, created_at, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, projectID, parsed.SnapshotID, string(parsed.Role), parsed.ManifestSHA256, path,
			string(canon), fmtTime(parsed.CreatedAt), fmtTime(t.at)); err != nil {
			return err
		}
		for _, sk := range parsed.Skills {
			if _, err := t.q.ExecContext(t.ctx, `INSERT INTO skill_snapshot_skills
				(project_id, snapshot_id, skill_id, version, tree_sha256, package, package_sha256) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				projectID, parsed.SnapshotID, sk.ID, sk.Version, sk.TreeSHA256, sk.Source.Package, sk.Source.PackageSHA256); err != nil {
				return err
			}
		}
		return t.journal(projectID, eventSpec{level: domain.AuditLevelConfig, kind: domain.AuditSkillSnapshotRecorded,
			entityType: "skill_snapshot", entityID: parsed.SnapshotID,
			details: map[string]string{"role": string(parsed.Role), "skills": itoa(int64(len(parsed.Skills)))}})
	})
}

// SkillSnapshot liefert einen gespeicherten Skill-Snapshot.
func (r *reader) SkillSnapshot(projectID, snapshotID string) (SkillSnapshotRecord, error) {
	var (
		raw, path, recorded string
		rec                 SkillSnapshotRecord
	)
	err := r.q.QueryRowContext(r.ctx, "SELECT snapshot_json, path, recorded_at FROM skill_snapshots WHERE project_id = ? AND snapshot_id = ?",
		projectID, snapshotID).Scan(&raw, &path, &recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, errf(CodeNotFound, "Skill-Snapshot %q existiert nicht", snapshotID)
	}
	if err != nil {
		return rec, mapErr(err)
	}
	if rec.Snapshot, err = domain.ParseSkillSnapshot([]byte(raw)); err != nil {
		return rec, wrapf(CodeInternal, err, "gespeicherter Skill-Snapshot")
	}
	rec.Path = path
	rec.RecordedAt, err = scanTime(recorded)
	return rec, err
}
