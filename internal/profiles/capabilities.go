package profiles

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// CapabilitiesSchemaVersion ist die unterstützte schema_version von models.yaml.
const CapabilitiesSchemaVersion = 1

// ApprovedFallback ist eine in models.yaml ausdrücklich genehmigte Reduktion: Fordert jemand
// für Model die Stufe Requested an und unterstützt das Modell sie nicht, läuft es mit Effective.
// Die Genehmigung gilt nur für genau dieses Paar aus Modell und angeforderter Stufe. Source
// nennt die Quelle (zum Beispiel die Dokumentation).
type ApprovedFallback struct {
	Model     string
	Requested domain.Effort
	Effective domain.Effort
	Source    string
}

// Capabilities sind die geprüften Fähigkeiten der Modelle (aus models.yaml). Sie sind die einzige
// Modellliste: der Domain-Katalog (BuildCatalog) wird aus ihnen abgeleitet.
type Capabilities struct {
	// Revision zählt die Änderungen der Datei (ab 1).
	Revision int
	// Source und CheckedOn nennen die Quelle der Stufen und das Prüfdatum (JJJJ-MM-TT).
	Source    string
	CheckedOn string
	// Models bildet die volle Modell-ID auf die unterstützten Effort-Stufen ab.
	Models map[string][]domain.Effort
	// Fallbacks sind die genehmigten Reduktionen. Ohne Eintrag wird abgelehnt.
	Fallbacks []ApprovedFallback
	// Policies sind die bekannten Policy-Profile.
	Policies []string
}

// ---- YAML-Form ---------------------------------------------------------------

type capsDoc struct {
	SchemaVersion int                 `yaml:"schema_version"`
	Revision      int                 `yaml:"revision"`
	Source        string              `yaml:"source"`
	CheckedOn     string              `yaml:"checked_on"`
	Policies      []string            `yaml:"policies"`
	Models        map[string]modelDoc `yaml:"models"`
	Fallbacks     []fallbackDoc       `yaml:"fallbacks,omitempty"`
}

type modelDoc struct {
	Efforts []domain.Effort `yaml:"efforts"`
}

type fallbackDoc struct {
	Model     string        `yaml:"model"`
	Requested domain.Effort `yaml:"requested"`
	Effective domain.Effort `yaml:"effective"`
	Source    string        `yaml:"source"`
}

// ParseCapabilities liest models.yaml streng (unbekannte und doppelte Schlüssel, weitere
// Dokumente sind Fehler) und prüft die Invarianten (Validate).
func ParseCapabilities(raw []byte) (Capabilities, error) {
	var doc capsDoc
	if err := decodeStrict(raw, &doc); err != nil {
		return Capabilities{}, &Error{Code: CodeInvalidConfig, Message: "models.yaml ist ungültig: " + err.Error(), cause: err}
	}
	if doc.SchemaVersion != CapabilitiesSchemaVersion {
		return Capabilities{}, newErr(CodeInvalidConfig, "schema_version", "models.yaml: schema_version %d wird nicht unterstützt (erwartet %d)", doc.SchemaVersion, CapabilitiesSchemaVersion)
	}
	c := Capabilities{
		Revision: doc.Revision, Source: doc.Source, CheckedOn: doc.CheckedOn,
		Policies: slices.Clone(doc.Policies), Models: map[string][]domain.Effort{},
	}
	for id, m := range doc.Models {
		c.Models[id] = slices.Clone(m.Efforts)
	}
	for _, f := range doc.Fallbacks {
		c.Fallbacks = append(c.Fallbacks, ApprovedFallback{Model: f.Model, Requested: f.Requested, Effective: f.Effective, Source: f.Source})
	}
	if err := c.Validate(); err != nil {
		return Capabilities{}, err
	}
	return c, nil
}

// Validate prüft die Invarianten: Revision, Quelle und Prüfdatum, Modelle mit gültigen,
// eindeutigen Stufen, bekannte Policies und widerspruchsfreie Fallbacks. Ein Fallback ist nur
// gültig, wenn das Modell die angeforderte Stufe nicht unterstützt und die wirksame Stufe genau
// der dokumentierten Regel entspricht: die höchste unterstützte Stufe auf oder unter der
// angeforderten.
func (c Capabilities) Validate() error {
	bad := func(field, format string, args ...any) error {
		return newErr(CodeInvalidConfig, field, "models.yaml: "+format, args...)
	}
	switch {
	case c.Revision < 1:
		return bad("revision", "revision muss eine Ganzzahl >= 1 sein (ist %d)", c.Revision)
	case c.Source == "":
		return bad("source", "source (Quelle der Effort-Stufen) fehlt")
	}
	if _, err := time.Parse("2006-01-02", c.CheckedOn); err != nil {
		return bad("checked_on", "checked_on %q ist kein Datum (JJJJ-MM-TT)", c.CheckedOn)
	}
	if len(c.Policies) == 0 {
		return bad("policies", "policies darf nicht leer sein")
	}
	for _, p := range c.Policies {
		if err := domain.CheckID(p); err != nil {
			return bad("policies", "Policy-Profil %q ist keine gültige ID", p)
		}
	}
	if d, dup := hasDuplicates(c.Policies); dup {
		return bad("policies", "Policy-Profil %q kommt mehrfach vor", d)
	}
	if len(c.Models) == 0 {
		return bad("models", "models darf nicht leer sein")
	}
	for _, id := range c.ModelIDs() {
		if err := checkModelID(id); err != nil {
			return bad("models", "%v", err)
		}
		efforts := c.Models[id]
		if len(efforts) == 0 {
			return bad("models", "Modell %s: efforts darf nicht leer sein (Modelle ohne Effort-Unterstützung sind nicht vorgesehen)", id)
		}
		for _, e := range efforts {
			if err := checkEffortLevel(e); err != nil {
				return bad("models", "Modell %s: %v", id, err)
			}
		}
		if d, dup := hasDuplicates(efforts); dup {
			return bad("models", "Modell %s: Stufe %s kommt mehrfach vor", id, d)
		}
	}
	type key struct {
		model string
		level domain.Effort
	}
	seen := map[key]bool{}
	for i, f := range c.Fallbacks {
		field := fmt.Sprintf("fallbacks[%d]", i)
		supported, known := c.Models[f.Model]
		switch {
		case !known:
			return bad(field, "Fallback nennt das Modell %q, das in models nicht steht", f.Model)
		case checkEffortLevel(f.Requested) != nil:
			return bad(field, "Fallback für %s: %v", f.Model, checkEffortLevel(f.Requested))
		case checkEffortLevel(f.Effective) != nil:
			return bad(field, "Fallback für %s: %v", f.Model, checkEffortLevel(f.Effective))
		case slices.Contains(supported, f.Requested):
			return bad(field, "Fallback für %s: %s wird vom Modell unterstützt, eine Reduktion ist nicht nötig und nicht zulässig", f.Model, f.Requested)
		case f.Source == "":
			return bad(field, "Fallback für %s und %s: source (Quelle der Genehmigung) fehlt", f.Model, f.Requested)
		case seen[key{f.Model, f.Requested}]:
			return bad(field, "Fallback für %s und %s kommt mehrfach vor", f.Model, f.Requested)
		}
		seen[key{f.Model, f.Requested}] = true
		want, ok := highestAtOrBelow(supported, f.Requested)
		if !ok {
			return bad(field, "Fallback für %s: unterhalb von %s unterstützt das Modell keine Stufe", f.Model, f.Requested)
		}
		if f.Effective != want {
			return bad(field, "Fallback für %s: %s läuft laut Regel als %s (höchste unterstützte Stufe auf oder unter der angeforderten), nicht als %s", f.Model, f.Requested, want, f.Effective)
		}
	}
	return nil
}

// highestAtOrBelow liefert die höchste Stufe aus supported, die nicht über limit liegt.
func highestAtOrBelow(supported []domain.Effort, limit domain.Effort) (domain.Effort, bool) {
	best, found := domain.Effort(""), false
	for _, e := range supported {
		if effortRank(e) <= effortRank(limit) && (!found || effortRank(e) > effortRank(best)) {
			best, found = e, true
		}
	}
	return best, found
}

// Supports meldet, ob das Modell konfiguriert ist und die Stufe unterstützt.
func (c Capabilities) Supports(model string, effort domain.Effort) bool {
	return slices.Contains(c.Models[model], effort)
}

// ApprovedFallback liefert die genehmigte Reduktion für genau dieses Modell und diese
// angeforderte Stufe, sonst false.
func (c Capabilities) ApprovedFallback(model string, requested domain.Effort) (ApprovedFallback, bool) {
	for _, f := range c.Fallbacks {
		if f.Model == model && f.Requested == requested {
			return f, true
		}
	}
	return ApprovedFallback{}, false
}

// ModelIDs liefert die konfigurierten Modell-IDs aufsteigend.
func (c Capabilities) ModelIDs() []string {
	ids := make([]string, 0, len(c.Models))
	for id := range c.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Clone liefert eine tiefe Kopie.
func (c Capabilities) Clone() Capabilities {
	out := c
	out.Models = make(map[string][]domain.Effort, len(c.Models))
	for id, e := range c.Models {
		out.Models[id] = slices.Clone(e)
	}
	out.Fallbacks = slices.Clone(c.Fallbacks)
	out.Policies = slices.Clone(c.Policies)
	return out
}

const capsHeader = "# models.yaml – Fähigkeiten der Modelle (von ccmas verwaltet; Kommentare gehen beim Schreiben verloren).\n" +
	"# Modell-IDs stehen nur hier, nie im Code. Keine Zugangsdaten in dieser Datei.\n"

// Marshal liefert die YAML-Form (stabil sortiert).
func (c Capabilities) Marshal() ([]byte, error) {
	doc := capsDoc{
		SchemaVersion: CapabilitiesSchemaVersion, Revision: c.Revision, Source: c.Source, CheckedOn: c.CheckedOn,
		Policies: c.Policies, Models: map[string]modelDoc{},
	}
	for id, e := range c.Models {
		doc.Models[id] = modelDoc{Efforts: e}
	}
	for _, f := range c.Fallbacks {
		doc.Fallbacks = append(doc.Fallbacks, fallbackDoc{Model: f.Model, Requested: f.Requested, Effective: f.Effective, Source: f.Source})
	}
	return encodeYAML(capsHeader, doc)
}
