package domain

import (
	"sort"
	"time"
)

// SkillSnapshotSchemaVersion ist die Version der snapshot.json eines Skill-Snapshots.
const SkillSnapshotSchemaVersion = 1

// SkillSnapshotFile ist eine Datei eines Skills mit ihrer Prüfsumme (SHA-256 der rohen Bytes).
type SkillSnapshotFile struct {
	Path   string `json:"path"` // relativ zum Skill-Verzeichnis, mit /
	SHA256 string `json:"sha256"`
}

// SkillSource ist die Herkunft eines Skills (vault-relativer Pfad des Originalpakets).
type SkillSource struct {
	Package       string `json:"package"`
	PackageSHA256 string `json:"package_sha256"`
}

// SkillSnapshotSkill ist ein Skill in einem Snapshot.
type SkillSnapshotSkill struct {
	ID         string              `json:"id"`
	Version    int                 `json:"version"`
	TreeSHA256 string              `json:"tree_sha256"`
	Files      []SkillSnapshotFile `json:"files"`
	Source     SkillSource         `json:"source"`
}

// SkillSnapshot ist der Inhalt der snapshot.json (Vertrag skill-snapshot-v1). Die Hashes
// rechnet internal/skills; der Vertrag ist mit dem Python-Runner gemeinsam.
type SkillSnapshot struct {
	SchemaVersion  int                  `json:"schema_version"`
	SnapshotID     string               `json:"snapshot_id"`
	Role           Role                 `json:"role"`
	CreatedAt      time.Time            `json:"created_at"`
	ManifestSHA256 string               `json:"manifest_sha256"`
	Skills         []SkillSnapshotSkill `json:"skills"`
}

// Check prüft die strukturellen Invarianten, die das Schema nicht ausdrückt: Skills
// aufsteigend nach ID ohne Wiederholung, Dateien je Skill aufsteigend (bytewise) nach Pfad
// ohne Wiederholung. Hashes prüft internal/skills gegen den Dateibestand.
func (s SkillSnapshot) Check() error {
	if !sort.SliceIsSorted(s.Skills, func(i, j int) bool { return s.Skills[i].ID < s.Skills[j].ID }) {
		return newError(CatValue, "/skills", "Skills müssen aufsteigend nach ID sortiert sein")
	}
	for i, sk := range s.Skills {
		if i > 0 && s.Skills[i-1].ID == sk.ID {
			return newError(CatID, "/skills", "Skill %q kommt mehrfach vor", sk.ID)
		}
		for j, f := range sk.Files {
			if j > 0 && sk.Files[j-1].Path >= f.Path {
				return newError(CatPath, "/skills", "Dateien von %q müssen aufsteigend nach Pfad sortiert und eindeutig sein (%q)", sk.ID, f.Path)
			}
		}
	}
	return nil
}
