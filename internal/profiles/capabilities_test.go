package profiles

import (
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

const capsYAML = `schema_version: 1
revision: 3
source: "Testquelle"
checked_on: "2026-11-10"
policies: [readonly, scoped_write]
models:
  model-big:
    efforts: [low, medium, high, xhigh, max]
  model-mid:
    efforts: [low, medium, high]
fallbacks:
  - model: model-mid
    requested: xhigh
    effective: high
    source: "Testdoku"
`

func TestParseCapabilities(t *testing.T) {
	c, err := ParseCapabilities([]byte(capsYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Revision != 3 || c.Source != "Testquelle" || c.CheckedOn != "2026-11-10" {
		t.Errorf("Kopfdaten = %+v", c)
	}
	if !c.Supports("model-big", domain.EffortMax) || c.Supports("model-mid", domain.EffortXHigh) || c.Supports("unbekannt", domain.EffortLow) {
		t.Error("Supports liefert falsche Antworten")
	}
	fb, ok := c.ApprovedFallback("model-mid", domain.EffortXHigh)
	if !ok || fb.Effective != domain.EffortHigh || fb.Source != "Testdoku" {
		t.Errorf("Fallback = %+v (%v)", fb, ok)
	}
	if _, ok := c.ApprovedFallback("model-mid", domain.EffortMax); ok {
		t.Error("Fallback für eine nicht genehmigte Stufe")
	}
	if _, ok := c.ApprovedFallback("model-big", domain.EffortXHigh); ok {
		t.Error("Fallback für ein anderes Modell")
	}
	if got := c.ModelIDs(); strings.Join(got, ",") != "model-big,model-mid" {
		t.Errorf("ModelIDs = %v", got)
	}
}

func TestCapabilitiesRoundTrip(t *testing.T) {
	c, err := ParseCapabilities([]byte(capsYAML))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseCapabilities(raw)
	if err != nil {
		t.Fatalf("eigene Ausgabe nicht lesbar: %v\n%s", err, raw)
	}
	raw2, _ := again.Marshal()
	if string(raw) != string(raw2) {
		t.Errorf("Marshal ist nicht stabil:\n%s\n---\n%s", raw, raw2)
	}
}

func TestParseCapabilitiesRejects(t *testing.T) {
	cases := map[string]string{
		"unbekannter Schlüssel":            strings.Replace(capsYAML, "revision: 3", "revision: 3\nzusatz: 1", 1),
		"falsche Schema-Version":           strings.Replace(capsYAML, "schema_version: 1", "schema_version: 2", 1),
		"Revision null":                    strings.Replace(capsYAML, "revision: 3", "revision: 0", 1),
		"kein Modell":                      "schema_version: 1\nrevision: 1\nsource: x\nchecked_on: \"2026-11-10\"\npolicies: [readonly]\nmodels: {}\n",
		"unbekannte Stufe":                 strings.Replace(capsYAML, "[low, medium, high]", "[low, mittel]", 1),
		"Stufe doppelt":                    strings.Replace(capsYAML, "[low, medium, high]", "[low, low]", 1),
		"keine Stufe":                      strings.Replace(capsYAML, "[low, medium, high]", "[]", 1),
		"Modell-ID mit Leerzeichen":        strings.Replace(capsYAML, "model-mid:", "\"model mid\":", 1),
		"Quelle fehlt":                     strings.Replace(capsYAML, "source: \"Testquelle\"\n", "", 1),
		"Prüfdatum kein Datum":             strings.Replace(capsYAML, "2026-11-10", "gestern", 1),
		"keine Policy":                     strings.Replace(capsYAML, "policies: [readonly, scoped_write]", "policies: []", 1),
		"Policy doppelt":                   strings.Replace(capsYAML, "[readonly, scoped_write]", "[readonly, readonly]", 1),
		"zwei Dokumente":                   capsYAML + "---\nrevision: 9\n",
		"leer":                             "",
		"Fallback: Modell unbekannt":       strings.Replace(capsYAML, "- model: model-mid", "- model: model-x", 1),
		"Fallback: Stufe wird unterstützt": strings.Replace(capsYAML, "requested: xhigh", "requested: high", 1),
		"Fallback: wirksam ist nicht die höchste darunter": strings.Replace(capsYAML, "effective: high", "effective: low", 1),
		"Fallback: wirksam nicht unterstützt":              strings.Replace(capsYAML, "effective: high", "effective: xhigh", 1),
		"Fallback: Quelle fehlt":                           strings.Replace(capsYAML, "    source: \"Testdoku\"\n", "", 1),
		"Fallback doppelt":                                 strings.Replace(capsYAML, "    source: \"Testdoku\"\n", "    source: \"Testdoku\"\n  - model: model-mid\n    requested: xhigh\n    effective: high\n    source: \"nochmal\"\n", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCapabilities([]byte(raw))
			wantCode(t, err, CodeInvalidConfig)
		})
	}
}

func TestCapabilitiesFallbackBelowEveryLevel(t *testing.T) {
	// Die Regel der Doku: höchste unterstützte Stufe auf oder unter der angeforderten.
	c := testCaps()
	c.Models["model-lücke"] = []domain.Effort{domain.EffortLow, domain.EffortHigh} // medium fehlt
	c.Fallbacks = []ApprovedFallback{{Model: "model-lücke", Requested: domain.EffortMedium, Effective: domain.EffortLow, Source: "Testdoku"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("gültiger Fallback abgewiesen: %v", err)
	}
	c.Fallbacks[0].Effective = domain.EffortHigh // darüber ist keine Reduktion
	wantCode(t, c.Validate(), CodeInvalidConfig)
	c.Models["model-nur-hoch"] = []domain.Effort{domain.EffortHigh}
	c.Fallbacks = []ApprovedFallback{{Model: "model-nur-hoch", Requested: domain.EffortMedium, Effective: domain.EffortHigh, Source: "x"}}
	wantCode(t, c.Validate(), CodeInvalidConfig) // unterhalb von medium gibt es nichts
}

func TestCapabilitiesCloneIsDeep(t *testing.T) {
	c := testCaps()
	d := c.Clone()
	d.Models[mBig][0] = domain.EffortMax
	d.Fallbacks[0].Source = "geändert"
	d.Policies[0] = "geändert"
	if c.Models[mBig][0] != domain.EffortLow || c.Fallbacks[0].Source == "geändert" || c.Policies[0] == "geändert" {
		t.Error("Clone teilt Speicher")
	}
}
