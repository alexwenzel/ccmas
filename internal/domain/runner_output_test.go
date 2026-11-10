package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixtureMessage liest eine gültige Fixture als veränderbare Map.
func fixtureMessage(t *testing.T, name string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(readFixture(t, "valid", name), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// bigObject liefert ein Objekt mit einem Textfeld, dessen kanonische Form etwas über n Bytes groß ist.
func bigObject(n int) map[string]any {
	return map[string]any{"type": "object", "description": strings.Repeat("a", n)}
}

func TestPayloadByteLimits(t *testing.T) {
	start := func(schema any) []byte {
		m := fixtureMessage(t, "to-runner--start_with_system_prompt_and_output_schema.json")
		m["payload"].(map[string]any)["output_schema"] = schema
		raw, _ := json.Marshal(m)
		return raw
	}
	result := func(out any) []byte {
		m := fixtureMessage(t, "from-runner--result_with_structured_output.json")
		m["payload"].(map[string]any)["structured_output"] = out
		raw, _ := json.Marshal(m)
		return raw
	}
	// Die Grenze steht im Schema (x-max-bytes); der Test liest sie nicht, sondern prüft beide Seiten davon.
	t.Run("output_schema knapp unter und über 32 KiB", func(t *testing.T) {
		if err := Validate(TargetToRunner, start(bigObject(32000))); err != nil {
			t.Fatalf("unter der Grenze abgewiesen: %v", err)
		}
		wantCategory(t, Validate(TargetToRunner, start(bigObject(33000))), CatSize)
	})
	t.Run("structured_output knapp unter und über 256 KiB", func(t *testing.T) {
		if err := Validate(TargetFromRunner, result(map[string]any{"text": strings.Repeat("a", 260000)})); err != nil {
			t.Fatalf("unter der Grenze abgewiesen: %v", err)
		}
		wantCategory(t, Validate(TargetFromRunner, result(map[string]any{"text": strings.Repeat("a", 263000)})), CatSize)
	})
	t.Run("das Maß ist die kanonische Form, nicht die Schreibweise der Zeile", func(t *testing.T) {
		// Viel Leerraum in der Zeile ändert die kanonische Größe nicht.
		m := fixtureMessage(t, "to-runner--start_with_system_prompt_and_output_schema.json")
		raw, _ := json.MarshalIndent(m, "                                        ", "                    ")
		if err := Validate(TargetToRunner, raw); err != nil {
			t.Fatalf("Einrückung verändert die Größe: %v", err)
		}
	})
	t.Run("ein anderer Nachrichtentyp ist nicht betroffen", func(t *testing.T) {
		m := fixtureMessage(t, "from-runner--activity.json")
		m["payload"].(map[string]any)["structured_output"] = bigObject(300000)
		raw, _ := json.Marshal(m)
		wantCategory(t, Validate(TargetFromRunner, raw), CatUnknownField) // Schema lehnt das Feld dort ab, nicht die Größe
	})
}

func TestStructuredOutputAndSystemPromptSurviveRoundTrip(t *testing.T) {
	ev, err := ParseRunEvent(readFixture(t, "valid", "from-runner--result_with_structured_output.json"))
	if err != nil {
		t.Fatal(err)
	}
	res := ev.Payload.(Result)
	var out map[string]any
	if err := json.Unmarshal(res.StructuredOutput, &out); err != nil || out["answer"] != "ok" {
		t.Fatalf("StructuredOutput = %s (%v)", res.StructuredOutput, err)
	}
	cmd, err := ParseRunCommand(readFixture(t, "valid", "to-runner--start_with_system_prompt_and_output_schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec := cmd.Payload.(RunSpec)
	if !strings.HasPrefix(spec.SystemPrompt, "Du bist der Supervisor") || !strings.Contains(string(spec.OutputSchema), `"definitions"`) {
		t.Fatalf("RunSpec = %+v", spec)
	}
	// Ohne die optionalen Felder bleibt die Nachricht wie vor CC-14 (keine leeren Felder).
	spec.SystemPrompt, spec.OutputSchema = "", nil
	raw, _ := json.Marshal(spec)
	if strings.Contains(string(raw), "system_prompt") || strings.Contains(string(raw), "output_schema") {
		t.Fatalf("leere optionale Felder werden mitgesendet: %s", raw)
	}
	se, err := ParseRunEvent(readFixture(t, "valid", "from-runner--session_started_with_sdk_version.json"))
	if err != nil || se.Payload.(SessionStarted).SDKVersion != "0.2.180" {
		t.Fatalf("sdk_version = %+v (%v)", se.Payload, err)
	}
}

func TestCheckOutputSchema(t *testing.T) {
	ok := []string{
		`{"type":"object","properties":{"a":{"type":"string","enum":["x","y"]}},"required":["a"],"additionalProperties":false}`,
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","definitions":{"d":{"type":"integer"}},"properties":{"a":{"$ref":"#/definitions/d"}}}`,
		`{"type":"object","if":{"properties":{"k":{"const":1}}},"then":{"required":["k"]}}`,
	}
	for _, s := range ok {
		if err := CheckOutputSchema(json.RawMessage(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := map[string]string{
		"kein Objekt":                  `["a"]`,
		"kein JSON":                    `{"type":`,
		"leer":                         ``,
		"unbekannter Typ":              `{"type":"objekt"}`,
		"required kein Array":          `{"type":"object","required":"a"}`,
		"Verweis ins Leere":            `{"type":"object","properties":{"a":{"$ref":"#/definitions/fehlt"}}}`,
		"Verweis auf fremdes Dokument": `{"type":"object","properties":{"a":{"$ref":"https://example.invalid/x.json"}}}`,
		"minimum keine Zahl":           `{"type":"object","properties":{"a":{"minimum":"a"}}}`,
	}
	for name, s := range bad {
		if err := CheckOutputSchema(json.RawMessage(s)); err == nil {
			t.Errorf("%s: nicht abgewiesen: %s", name, s)
		}
	}
}
