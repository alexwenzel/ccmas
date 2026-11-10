package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validSkillSnapshot() SkillSnapshot {
	hex := func(c string) string { return strings.Repeat(c, 64) }
	return SkillSnapshot{
		SchemaVersion: SkillSnapshotSchemaVersion, SnapshotID: "sha256:" + hex("a"), Role: RoleConcept,
		CreatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), ManifestSHA256: hex("b"),
		Skills: []SkillSnapshotSkill{
			{ID: "ccmas-a", Version: 1, TreeSHA256: hex("c"), Source: SkillSource{Package: "x/a.skill", PackageSHA256: hex("d")},
				Files: []SkillSnapshotFile{{Path: "SKILL.md", SHA256: hex("e")}, {Path: "a-b.txt", SHA256: hex("e")}, {Path: "a/x.txt", SHA256: hex("e")}}},
			{ID: "ccmas-b", Version: 2, TreeSHA256: hex("c"), Source: SkillSource{Package: "x/b.skill", PackageSHA256: hex("d")},
				Files: []SkillSnapshotFile{{Path: "SKILL.md", SHA256: hex("e")}}},
		},
	}
}

func TestSkillSnapshotCheck(t *testing.T) {
	if err := validSkillSnapshot().Check(); err != nil {
		t.Fatalf("gültiger Snapshot abgewiesen: %v", err)
	}
	cases := map[string]struct {
		mutate func(s *SkillSnapshot)
		want   Category
	}{
		"Skills unsortiert":  {func(s *SkillSnapshot) { s.Skills[0], s.Skills[1] = s.Skills[1], s.Skills[0] }, CatValue},
		"Skill doppelt":      {func(s *SkillSnapshot) { s.Skills[1].ID = "ccmas-a" }, CatID},
		"Dateien unsortiert": {func(s *SkillSnapshot) { f := s.Skills[0].Files; f[0], f[1] = f[1], f[0] }, CatPath},
		"Datei doppelt":      {func(s *SkillSnapshot) { s.Skills[0].Files[1].Path = "SKILL.md" }, CatPath},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSkillSnapshot()
			tc.mutate(&s)
			err := s.Check()
			var ce *ContractError
			if !errors.As(err, &ce) || ce.Category() != tc.want {
				t.Fatalf("Check() = %v, erwartet Klasse %s", err, tc.want)
			}
		})
	}
}

// TestParseSkillSnapshotRejectsUnsorted: auch der Parser wendet Check an, nicht nur das Schema.
func TestParseSkillSnapshotRejectsUnsorted(t *testing.T) {
	s := validSkillSnapshot()
	s.Skills[0], s.Skills[1] = s.Skills[1], s.Skills[0]
	_, err := ParseSkillSnapshot(mustJSON(t, s))
	var ce *ContractError
	if !errors.As(err, &ce) || ce.Category() != CatValue {
		t.Fatalf("ParseSkillSnapshot = %v", err)
	}
	if _, err := ParseSkillSnapshot(mustJSON(t, validSkillSnapshot())); err != nil {
		t.Fatalf("gültiger Snapshot: %v", err)
	}
}
