package supervisor

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/alexwenzel/ccmas/contracts"
	"github.com/alexwenzel/ccmas/internal/domain"
)

// ---- Hilfen: Verträge als Dokumente lesen ---------------------------------------------------------------

func contractDoc(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := contracts.Schemas.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// at folgt einem Pfad aus Schlüsseln durch verschachtelte Objekte und löst dabei $ref auf Definitionen der Verträge auf.
func at(t *testing.T, node any, path ...string) any {
	t.Helper()
	for _, key := range path {
		node = deref(t, node)
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("kein Objekt vor %q: %v", key, node)
		}
		next, ok := m[key]
		if !ok {
			t.Fatalf("Schlüssel %q fehlt in %v", key, keys(m))
		}
		node = next
	}
	return deref(t, node)
}

func deref(t *testing.T, node any) any {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return node
	}
	ref, ok := m["$ref"].(string)
	if !ok {
		return node
	}
	file, ptr, _ := strings.Cut(ref, "#")
	if file == "" {
		t.Fatalf("lokaler $ref %q ohne Dokument", ref)
	}
	doc := contractDoc(t, file)
	var cur any = doc
	for _, part := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		cur = cur.(map[string]any)[part]
	}
	return deref(t, cur)
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func enumOf(t *testing.T, node any) []string {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("kein Schema: %v", node)
	}
	raw, ok := m["enum"].([]any)
	if !ok {
		t.Fatalf("kein enum in %v", m)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = v.(string)
	}
	sort.Strings(out)
	return out
}

func modelDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(ModelOutputSchema(), &doc); err != nil {
		t.Fatalf("ModelOutputSchema ist kein JSON-Objekt: %v", err)
	}
	return doc
}

// ---- Tests -------------------------------------------------------------------------------------------------

func TestModelSchemaIsAValidFlatDraft07Schema(t *testing.T) {
	raw := ModelOutputSchema()
	if err := domain.CheckOutputSchema(raw); err != nil {
		t.Fatalf("kein gültiges Draft-07-Schema: %v", err)
	}
	if !bytes.Equal(raw, ModelOutputSchema()) {
		t.Error("das Schema ist nicht deterministisch")
	}
	if len(raw) > 16<<10 {
		t.Errorf("Schema hat %d Bytes; der Vertrag erlaubt 32 KiB, das Modellschema soll klein bleiben", len(raw))
	}
	// Flach: keine Verweise, keine Bedingungen, keine Schlüsselwörter von Draft 2020-12; Go prüft den Rest.
	forbidden := []string{"$ref", "$defs", "definitions", "if", "then", "else", "allOf", "anyOf", "oneOf", "not", "prefixItems",
		"unevaluatedProperties", "unevaluatedItems", "dependentSchemas", "dependentRequired", "$anchor", "$dynamicRef", "pattern"}
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch v := node.(type) {
		case map[string]any:
			for k, child := range v {
				// Namen von Eigenschaften (unter "properties") sind keine Schlüsselwörter.
				if !strings.HasSuffix(path, "/properties") && slices.Contains(forbidden, k) {
					t.Errorf("%s: Schlüsselwort %q ist im flachen Modellschema nicht vorgesehen", path, k)
				}
				walk(path+"/"+k, child)
			}
		case []any:
			for i, child := range v {
				walk(path+"/"+string(rune('0'+i%10)), child)
			}
		}
	}
	walk("", modelDoc(t))
	if modelDoc(t)["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("$schema = %v", modelDoc(t)["$schema"])
	}
}

// TestModelSchemaEnumsMatchTheContracts hält die Aufzählungen des Modellschemas mit decision-v1 und plan-v1 gleich: Das
// Modellschema wird in Go aus denselben Aufzählungen erzeugt; dieser Test fängt jede Drift zwischen Go-Aufzählung,
// Vertrag und Modellschema. Maßgeblich bleibt die Prüfung in Go (schema + Validator), nicht das Modellschema.
func TestModelSchemaEnumsMatchTheContracts(t *testing.T) {
	model := modelDoc(t)
	decision := contractDoc(t, "decision-v1.schema.json")
	plan := contractDoc(t, "plan-v1.schema.json")

	mItem := func(path ...string) any {
		return at(t, model, append([]string{"properties", "decisions", "items"}, path...)...)
	}
	dec := func(path ...string) any { return at(t, decision, append([]string{"$defs", "decision"}, path...)...) }

	pairs := []struct {
		name         string
		model, check any
	}{
		{"Aktion", mItem("properties", "action"), dec("properties", "action")},
		{"Zielart der Entscheidung", mItem("properties", "target", "properties", "kind"), dec("properties", "target", "properties", "kind")},
		{"Voraussetzung", mItem("properties", "preconditions", "items", "properties", "kind"), dec("properties", "preconditions", "items", "properties", "kind")},
		{"Rolle", mItem("properties", "selection", "properties", "role"), dec("properties", "selection", "properties", "role")},
		{"Effort", mItem("properties", "selection", "properties", "effort"), dec("properties", "selection", "properties", "effort")},
		{"Größe", mItem("properties", "estimate", "properties", "size"), dec("properties", "estimate", "properties", "size")},
		{"Komplexität", mItem("properties", "estimate", "properties", "complexity"), dec("properties", "estimate", "properties", "complexity")},
		{"Unsicherheit", mItem("properties", "estimate", "properties", "uncertainty"), dec("properties", "estimate", "properties", "uncertainty")},
		{"Art des neuen Auftrags", mItem("properties", "plan_extension", "properties", "work_orders", "items", "properties", "kind"),
			dec("properties", "plan_extension", "properties", "work_orders", "items", "properties", "kind")},
		{"Modus des neuen Auftrags", mItem("properties", "plan_extension", "properties", "work_orders", "items", "properties", "mode"),
			dec("properties", "plan_extension", "properties", "work_orders", "items", "properties", "mode")},
		{"Zielart des Batches", at(t, model, "properties", "goal", "properties", "kind"),
			at(t, plan, "$defs", "decision_batch", "properties", "goal", "properties", "kind")},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			got, want := enumOf(t, p.model), enumOf(t, p.check)
			if !slices.Equal(got, want) {
				t.Errorf("Modellschema %v != Vertrag %v", got, want)
			}
		})
	}
}

// TestModelSchemaCoversTheContractFields: Das Modellschema führt alle Felder und Pflichtfelder der Entscheidung und des
// Batches, bis auf die, die Go selbst setzt (schema_version, batch_id, project_id, state_revision).
func TestModelSchemaCoversTheContractFields(t *testing.T) {
	model := modelDoc(t)
	goSet := []string{"schema_version", "project_id", "state_revision"}
	strip := func(in []string, drop ...string) []string {
		var out []string
		for _, s := range in {
			if !slices.Contains(drop, s) {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return out
	}
	required := func(node any) []string {
		raw, _ := node.(map[string]any)["required"].([]any)
		var out []string
		for _, v := range raw {
			out = append(out, v.(string))
		}
		return out
	}
	t.Run("Entscheidung", func(t *testing.T) {
		dec := at(t, contractDoc(t, "decision-v1.schema.json"), "$defs", "decision")
		item := at(t, model, "properties", "decisions", "items")
		if got, want := keys(at(t, item, "properties").(map[string]any)), strip(keys(at(t, dec, "properties").(map[string]any)), goSet...); !slices.Equal(got, want) {
			t.Errorf("Felder %v != Vertrag ohne von Go gesetzte %v", got, want)
		}
		if got, want := strip(required(item)), strip(required(dec), goSet...); !slices.Equal(got, want) {
			t.Errorf("Pflichtfelder %v != Vertrag %v", got, want)
		}
	})
	t.Run("Batch", func(t *testing.T) {
		batch := at(t, contractDoc(t, "plan-v1.schema.json"), "$defs", "decision_batch")
		drop := append([]string{"batch_id"}, goSet...)
		if got, want := keys(at(t, model, "properties").(map[string]any)), strip(keys(at(t, batch, "properties").(map[string]any)), drop...); !slices.Equal(got, want) {
			t.Errorf("Felder %v != Vertrag %v", got, want)
		}
		if got, want := strip(required(model)), strip(required(batch), drop...); !slices.Equal(got, want) {
			t.Errorf("Pflichtfelder %v != Vertrag %v", got, want)
		}
	})
}

// modelSample ist eine Ausgabe, wie sie ein Modell nach dem Schema liefert.
const modelSample = `{
  "goal": {"kind": "milestone", "id": "ms-1"},
  "rationale": "Zwei unabhängige Umsetzungen parallel.",
  "decisions": [
    {"decision_id": "dec-1", "target": {"kind": "work_order", "id": "wo-a"}, "action": "implementation",
     "preconditions": [{"kind": "concept_version_approved", "ref": "1.0"}], "rationale": "Konzept freigegeben, unabhängig.",
     "selection": {"role": "implementer", "skill_ids": ["ccmas-t-impl"], "model": "model-mid", "effort": "high"},
     "expected_outcome": "Getesteter Stand im Worktree.", "estimate": {"size": "m", "complexity": "medium", "uncertainty": "low"}},
    {"decision_id": "dec-2", "target": {"kind": "work_order", "id": "wo-c"}, "action": "wait",
     "preconditions": [{"kind": "work_order_completed", "ref": "wo-a"}], "rationale": "Wartet auf wo-a."}
  ]
}`

func TestModelSchemaAcceptsASampleAndRejectsDeviations(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(ModelOutputSchema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("mem://model.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem://model.json")
	if err != nil {
		t.Fatal(err)
	}
	validate := func(raw string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	if err := validate(modelSample); err != nil {
		t.Fatalf("Beispiel abgewiesen: %v", err)
	}
	mutate := func(f func(m map[string]any)) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(modelSample), &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	decision := func(m map[string]any, i int) map[string]any { return m["decisions"].([]any)[i].(map[string]any) }
	for name, f := range map[string]func(m map[string]any){
		"unbekannte Aktion":    func(m map[string]any) { decision(m, 1)["action"] = "deploy" },
		"unbekannter Effort":   func(m map[string]any) { decision(m, 0)["selection"].(map[string]any)["effort"] = "ultra" },
		"unbekanntes Feld":     func(m map[string]any) { m["project_id"] = "x" },
		"Größe außerhalb":      func(m map[string]any) { decision(m, 0)["estimate"].(map[string]any)["size"] = "xxl" },
		"keine Entscheidungen": func(m map[string]any) { m["decisions"] = []any{} },
		"Begründung fehlt":     func(m map[string]any) { delete(m, "rationale") },
		"Voraussetzungsart": func(m map[string]any) {
			decision(m, 1)["preconditions"].([]any)[0].(map[string]any)["kind"] = "tests_green"
		},
		"Entscheidung ohne ID":        func(m map[string]any) { delete(decision(m, 0), "decision_id") },
		"Zielart work_order im Batch": func(m map[string]any) { m["goal"].(map[string]any)["kind"] = "work_order" },
	} {
		if err := validate(mutate(f)); err == nil {
			t.Errorf("%s: nicht abgewiesen", name)
		}
	}
}
