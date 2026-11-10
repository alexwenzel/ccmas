package supervisor

import (
	"encoding/json"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// DraftSchemaURI kennzeichnet das Metaschema, nach dem das SDK die strukturierte Ausgabe validiert (Draft-07).
const DraftSchemaURI = "http://json-schema.org/draft-07/schema#"

// ModelOutputSchema liefert das Ausgabeschema, das der Planer als RunSpec.output_schema übergibt: das Planungsergebnis
// (DecisionBatch, plan-v1 und decision-v1), wie das Modell es liefert.
//
// Das Schema ist flach und klein: Draft-07, ohne Verweise ($ref), Bedingungen (if/then/else, allOf, anyOf), Muster oder
// Schlüsselwörter von Draft 2020-12; Aufzählungen und Pflichtfelder, sonst nichts. Es führt alle Felder des Vertrags bis
// auf die, die Go selbst setzt: schema_version, batch_id, project_id und state_revision (aus dem Planungs-Snapshot, nie
// aus der Modellausgabe). Die Aufzählungen entstehen aus denselben Go-Aufzählungen wie die Prüfung in internal/domain; ein
// Gleichlauftest hält sie mit decision-v1 und plan-v1 gleich.
//
// Das Schema hilft dem Modell, die Form zu treffen. Maßgeblich bleibt die Prüfung in Go: plan-v1 und decision-v1 (zum Beispiel
// „selection“ bei Beauftragungen, Muster der IDs und Längen) und der Validator gegen den ProjectSnapshot.
//
// Die Ausgabe ist deterministisch (sortierte Schlüssel, gleiche Bytes bei gleichem Stand).
func ModelOutputSchema() json.RawMessage {
	str := func(desc string) map[string]any {
		s := map[string]any{"type": "string"}
		if desc != "" {
			s["description"] = desc
		}
		return s
	}
	enum := func(vals ...string) map[string]any { return map[string]any{"type": "string", "enum": vals} }
	obj := func(props map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	list := func(items any) map[string]any { return map[string]any{"type": "array", "items": items} }

	targetKinds := make([]string, len(domain.TargetKinds()))
	for i, k := range domain.TargetKinds() {
		targetKinds[i] = string(k)
	}
	goalKinds := []string{string(domain.KindTicket), string(domain.KindFeature), string(domain.KindMilestone)}

	workOrder := obj(map[string]any{
		"ref":                 str("neue, noch nicht vergebene ID"),
		"kind":                enum(strs(domain.PlanKinds())...),
		"title":               str(""),
		"goal":                str(""),
		"acceptance_criteria": list(str("")),
		"depends_on":          list(str("vorhandene Aufträge oder andere neue refs")),
		"mode":                enum(strs(domain.IntegrationModes())...),
	}, "ref", "kind", "title", "goal", "acceptance_criteria", "depends_on")

	decision := obj(map[string]any{
		"decision_id":   str("eindeutig im Ergebnis, zum Beispiel dec-1"),
		"target":        obj(map[string]any{"kind": enum(targetKinds...), "id": str("")}, "kind", "id"),
		"action":        enum(strs(domain.DecisionActions())...),
		"preconditions": list(obj(map[string]any{"kind": enum(strs(domain.PreconditionKinds())...), "ref": str("")}, "kind")),
		"rationale":     str("kurze, überprüfbare Begründung"),
		"selection": obj(map[string]any{
			"role":      enum(strs(domain.Roles())...),
			"skill_ids": list(str("")),
			"model":     str("Modell aus dem Angebot der Rolle"),
			"effort":    enum(strs(domain.Efforts())...),
		}, "role", "skill_ids", "model", "effort"),
		"review_target": obj(map[string]any{
			"work_order_id":   str(""),
			"commit":          str("vollständiger Commit-Hash des Zustands"),
			"concept_version": str("review_concept_version des Auftrags laut Zustand; ohne Konzeptbezug " + NoConceptVersion),
		}, "work_order_id", "commit", "concept_version"),
		"question":         obj(map[string]any{"text": str(""), "context": str(""), "recommendation": str("")}, "text", "context"),
		"expected_outcome": str("erwartetes, überprüfbares Ergebnis; Pflicht bei Beauftragungen"),
		"estimate": map[string]any{
			"description": "Aufwandsschätzung der Arbeit, getrennt vom Effort der Auswahl; Pflicht bei Beauftragungen",
			"type":        "object",
			"properties": map[string]any{
				"size":        enum(strs(domain.EstimateSizes())...),
				"complexity":  enum(strs(domain.EstimateLevels())...),
				"uncertainty": enum(strs(domain.EstimateLevels())...),
			},
			"required":             []string{"size", "complexity", "uncertainty"},
			"additionalProperties": false,
		},
		"plan_extension": obj(map[string]any{"work_orders": list(workOrder)}, "work_orders"),
	}, "decision_id", "target", "action", "preconditions", "rationale")

	root := obj(map[string]any{
		"goal":      obj(map[string]any{"kind": enum(goalKinds...), "id": str("")}, "kind", "id"),
		"rationale": str("kurze Begründung des Plans"),
		"decisions": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": decision},
	}, "goal", "rationale", "decisions")
	root["$schema"] = DraftSchemaURI
	root["title"] = "Planungsergebnis des Supervisors"

	raw, err := json.Marshal(root) // sortiert die Schlüssel
	if err != nil {
		panic(err) // nur Zeichenketten, Listen und Objekte: kein Laufzeitfall
	}
	return raw
}

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}
