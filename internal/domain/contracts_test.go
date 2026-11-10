package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/contracts"
)

// fixtureRoot ist das gemeinsame Fixture-Verzeichnis; Go und Python lesen dieselben Dateien.
const fixtureRoot = "../../contracts/fixtures"

// fixtureFiles liefert alle Fixture-Dateien eines Unterverzeichnisses (valid/invalid).
func fixtureFiles(t *testing.T, sub string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fixtureRoot, sub))
	if err != nil {
		t.Fatalf("Fixture-Verzeichnis %s nicht lesbar: %v", sub, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("unerwarteter Eintrag in fixtures/%s: %s", sub, e.Name())
		}
		names = append(names, e.Name())
	}
	return names
}

func readFixture(t *testing.T, sub, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, sub, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestContractFixtures prüft die gemeinsamen Fixtures. Namensschema:
//
//	valid/<ziel>--<name>.json
//	invalid/<ziel>--<fehlerklasse>--<name>.json
//
// Dieselbe Prüfung läuft in Python (test_contract_fixtures); beide Seiten müssen
// jede Fixture identisch akzeptieren beziehungsweise mit derselben Fehlerklasse abweisen.
func TestContractFixtures(t *testing.T) {
	for _, name := range fixtureFiles(t, "valid") {
		t.Run("valid/"+name, func(t *testing.T) {
			parts := strings.Split(strings.TrimSuffix(name, ".json"), "--")
			if len(parts) != 2 {
				t.Fatalf("Dateiname %q entspricht nicht <ziel>--<name>.json", name)
			}
			target := Target(parts[0])
			if !slices.Contains(Targets(), target) {
				t.Fatalf("unbekanntes Ziel %q", target)
			}
			raw := readFixture(t, "valid", name)
			if err := Validate(target, raw); err != nil {
				t.Fatalf("gültige Fixture abgewiesen: %v", err)
			}
			// Typisiertes Parsen und erneutes Serialisieren muss gültig bleiben:
			// deckt Abweichungen zwischen Go-Typen und Schema auf.
			value, err := Parse(target, raw)
			if err != nil {
				t.Fatalf("typisiertes Parsen: %v", err)
			}
			again, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(target, again); err != nil {
				t.Fatalf("Round-Trip erzeugt ungültiges Dokument: %v\n%s", err, again)
			}
		})
	}

	seen := map[Category]bool{}
	for _, name := range fixtureFiles(t, "invalid") {
		t.Run("invalid/"+name, func(t *testing.T) {
			parts := strings.Split(strings.TrimSuffix(name, ".json"), "--")
			if len(parts) != 3 {
				t.Fatalf("Dateiname %q entspricht nicht <ziel>--<klasse>--<name>.json", name)
			}
			target, want := Target(parts[0]), Category(parts[1])
			if !slices.Contains(Targets(), target) {
				t.Fatalf("unbekanntes Ziel %q", target)
			}
			if !slices.Contains(Categories(), want) {
				t.Fatalf("unbekannte Fehlerklasse %q", want)
			}
			seen[want] = true
			err := Validate(target, readFixture(t, "invalid", name))
			var ce *ContractError
			if !errors.As(err, &ce) {
				t.Fatalf("ungültige Fixture nicht mit ContractError abgewiesen: %v", err)
			}
			if !slices.Equal(ce.Categories, []Category{want}) {
				t.Fatalf("Fehlerklassen = %v, erwartet [%s] (%v)", ce.Categories, want, ce)
			}
		})
	}

	// Die geforderten Fehlerklassen müssen durch Fixtures belegt sein.
	for _, c := range []Category{CatVersion, CatID, CatPath, CatModel, CatEffort, CatSize, CatAction, CatField, CatUnknownField} {
		if !seen[c] {
			t.Errorf("keine ungültige Fixture für Fehlerklasse %q", c)
		}
	}
}

// ---- Hilfen zum Aufbau von Nachrichten -------------------------------------

func baseEvent(typ string, payload map[string]any) map[string]any {
	return map[string]any{
		"protocol_version": "1",
		"project_id":       "proj-1",
		"execution_id":     "exec-1",
		"attempt":          1,
		"correlation_id":   "corr-1",
		"type":             typ,
		"timestamp":        "2026-11-10T12:00:00Z",
		"payload":          payload,
	}
}

func activityEvent() map[string]any {
	return baseEvent("activity", map[string]any{"kind": "idle", "summary": "wartet"})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func wantCategory(t *testing.T, err error, want Category) {
	t.Helper()
	var ce *ContractError
	if !errors.As(err, &ce) {
		t.Fatalf("kein ContractError: %v", err)
	}
	if !slices.Equal(ce.Categories, []Category{want}) {
		t.Fatalf("Fehlerklassen = %v, erwartet [%s]: %v", ce.Categories, want, ce)
	}
	if ce.Category() != want {
		t.Fatalf("Category() = %s, erwartet %s", ce.Category(), want)
	}
}

// TestRejectInvalidEnvelope: falsche Version, ungültige execution_id und übergroße
// Nachrichten werden abgewiesen, jeweils mit der passenden Fehlerklasse.
func TestRejectInvalidEnvelope(t *testing.T) {
	if _, err := ParseRunEvent(mustJSON(t, activityEvent())); err != nil {
		t.Fatalf("Grundnachricht muss gültig sein: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(m map[string]any)
		want   Category
	}{
		{"falsche Version", func(m map[string]any) { m["protocol_version"] = "2" }, CatVersion},
		{"Version als Zahl", func(m map[string]any) { m["protocol_version"] = 1 }, CatVersion},
		{"Version fehlt", func(m map[string]any) { delete(m, "protocol_version") }, CatField},
		{"execution_id fehlt", func(m map[string]any) { delete(m, "execution_id") }, CatField},
		{"execution_id leer", func(m map[string]any) { m["execution_id"] = "" }, CatID},
		{"execution_id mit Pfadanteilen", func(m map[string]any) { m["execution_id"] = "../etc/passwd" }, CatID},
		{"execution_id mit Zeilenumbruch", func(m map[string]any) { m["execution_id"] = "exec-1\n" }, CatID},
		{"execution_id zu lang", func(m map[string]any) { m["execution_id"] = strings.Repeat("a", 129) }, CatID},
		{"execution_id keine Zeichenkette", func(m map[string]any) { m["execution_id"] = 7 }, CatID},
		{"unbekanntes Feld", func(m map[string]any) { m["extra"] = true }, CatUnknownField},
		{"attempt 0", func(m map[string]any) { m["attempt"] = 0 }, CatValue},
		{"unbekannter Typ", func(m map[string]any) { m["type"] = "teleport" }, CatMessageType},
		{"Typ der falschen Richtung", func(m map[string]any) { m["type"] = "start" }, CatMessageType},
		{"Zeitstempel ohne Zone", func(m map[string]any) { m["timestamp"] = "2026-11-10T12:00:00" }, CatTimestamp},
		{"Payload kein Objekt", func(m map[string]any) { m["payload"] = []any{} }, CatValue},
		{"unbekanntes Payload-Feld", func(m map[string]any) {
			m["payload"] = map[string]any{"kind": "idle", "summary": "x", "extra": 1}
		}, CatUnknownField},
		{"Pflichtfeld im Payload fehlt", func(m map[string]any) {
			m["payload"] = map[string]any{"kind": "idle"}
		}, CatField},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			m := activityEvent()
			tc.mutate(m)
			_, err := ParseRunEvent(mustJSON(t, m))
			wantCategory(t, err, tc.want)
		})
	}

	t.Run("Richtung Go nach Python", func(t *testing.T) {
		// Ein vom Runner gemeldeter Typ ist als Kommando ungültig.
		_, err := ParseRunCommand(mustJSON(t, activityEvent()))
		wantCategory(t, err, CatMessageType)
	})
}

// TestMessageSizeLimit: die Größengrenze gilt für die ganze Zeile und ist am Rand exakt.
func TestMessageSizeLimit(t *testing.T) {
	if MaxMessageBytes != 1<<20 {
		t.Fatalf("MaxMessageBytes = %d, erwartet 1 MiB", MaxMessageBytes)
	}
	base := mustJSON(t, activityEvent())

	exact := append(bytes.Clone(base), bytes.Repeat([]byte(" "), MaxMessageBytes-len(base))...)
	if len(exact) != MaxMessageBytes {
		t.Fatalf("Testaufbau: Länge %d", len(exact))
	}
	if _, err := ParseRunEvent(exact); err != nil {
		t.Fatalf("Nachricht mit exakt MaxMessageBytes muss gültig sein: %v", err)
	}

	over := append(bytes.Clone(exact), ' ')
	_, err := ParseRunEvent(over)
	wantCategory(t, err, CatSize)

	// Eine übergroße Payload (gültiges JSON, aber über der Grenze) ist ein Größenfehler,
	// kein Syntax- oder Wertfehler.
	huge := baseEvent("activity", map[string]any{
		"kind": "assistant_text", "summary": strings.Repeat("x", MaxMessageBytes),
	})
	_, err = ParseRunEvent(mustJSON(t, huge))
	wantCategory(t, err, CatSize)

	// Senden prüft ebenfalls: nichts Übergroßes verlässt Go.
	env := Envelope{
		ProtocolVersion: ProtocolVersion, ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 1,
		CorrelationID: "corr-1", Type: MsgStop, Timestamp: mustTime(t, "2026-11-10T12:00:00Z"),
	}
	if _, err := EncodeRunCommand(env, StopPayload{Reason: ReasonUserRequest}); err != nil {
		t.Fatalf("gültiges Kommando: %v", err)
	}
}

func TestStrictParsing(t *testing.T) {
	good := string(mustJSON(t, activityEvent()))
	cases := []struct {
		name string
		raw  string
		want Category
	}{
		{"kein JSON", "not json", CatSyntax},
		{"leer", "", CatSyntax},
		{"Array", "[]", CatSyntax},
		{"Folgedaten", good + good, CatSyntax},
		{"doppelter Schlüssel", `{"protocol_version":"1","protocol_version":"1"}`, CatSyntax},
		{"doppelter Schlüssel im Payload", strings.Replace(good, `"kind":"idle"`, `"kind":"idle","kind":"idle"`, 1), CatSyntax},
		{"NaN", strings.Replace(good, `"attempt":1`, `"attempt":NaN`, 1), CatSyntax},
		{"ungültiges UTF-8", strings.Replace(good, "wartet", "wart\xffet", 1), CatSyntax},
		{"BOM", "\xef\xbb\xbf" + good, CatSyntax},
		{"Kommazahl als attempt", strings.Replace(good, `"attempt":1`, `"attempt":1.0`, 1), CatValue},
		{"Exponent als attempt", strings.Replace(good, `"attempt":1`, `"attempt":1e0`, 1), CatValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRunEvent([]byte(tc.raw))
			wantCategory(t, err, tc.want)
		})
	}
	if _, err := ParseRunEvent([]byte(good + "\n  ")); err != nil {
		t.Fatalf("Whitespace nach der Nachricht ist erlaubt: %v", err)
	}
}

// TestDecisionActions: nur die vereinbarten Supervisor-Aktionen sind gültig.
func TestDecisionActions(t *testing.T) {
	want := []string{
		"plan_extend", "concept_work", "implementation", "review",
		"ask_question", "request_integration", "wait", "request_completion",
	}
	var got []string
	for _, a := range DecisionActions() {
		got = append(got, string(a))
		if !a.Valid() {
			t.Errorf("Aktion %q meldet sich nicht als gültig", a)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Aktionen = %v, erwartet %v", got, want)
	}

	// Go-Konstanten und Schema-Enum dürfen nicht auseinanderlaufen.
	if enum := schemaEnum(t, "decision-v1.schema.json", "decision", "action"); !slices.Equal(sorted(enum), sorted(want)) {
		t.Fatalf("Schema-Enum action = %v, erwartet %v", enum, want)
	}

	for _, action := range want {
		t.Run("gültig/"+action, func(t *testing.T) {
			raw := mustJSON(t, decisionFor(action))
			if err := Validate(TargetDecision, raw); err != nil {
				t.Fatalf("Aktion %s abgewiesen: %v", action, err)
			}
			d, err := ParseDecision(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(d.Action) != action {
				t.Fatalf("Aktion = %q", d.Action)
			}
		})
	}

	for _, bad := range []string{"", "deploy", "merge", "PLAN_EXTEND", "wait ", "request-completion", "implement"} {
		t.Run("ungültig/"+bad, func(t *testing.T) {
			if Action(bad).Valid() {
				t.Fatalf("Action(%q).Valid() = true", bad)
			}
			d := decisionFor("wait")
			d["action"] = bad
			_, err := ParseDecision(mustJSON(t, d))
			wantCategory(t, err, CatAction)
		})
	}

	t.Run("Bedingte Pflichtfelder", func(t *testing.T) {
		for action, field := range map[string]string{
			"implementation": "selection", "concept_work": "selection", "review": "review_target", "ask_question": "question",
		} {
			d := decisionFor(action)
			delete(d, field)
			_, err := ParseDecision(mustJSON(t, d))
			wantCategory(t, err, CatField)
		}
	})
}

func decisionFor(action string) map[string]any {
	d := map[string]any{
		"schema_version": 1,
		"decision_id":    "dec-1",
		"project_id":     "proj-1",
		"state_revision": 7,
		"target":         map[string]any{"kind": "work_order", "id": "wo-1"},
		"action":         action,
		"preconditions":  []any{},
		"rationale":      "Kurze, überprüfbare Begründung.",
	}
	switch action {
	case "concept_work", "implementation", "review":
		role := map[string]string{"concept_work": "concept", "implementation": "implementer", "review": "reviewer"}[action]
		d["selection"] = map[string]any{"role": role, "skill_ids": []any{"ccmas-feature-parallel"}, "model": "model-a", "effort": "high"}
	}
	if action == "review" {
		d["review_target"] = map[string]any{
			"work_order_id": "wo-1", "commit": strings.Repeat("a", 40), "concept_version": "1.2.0",
		}
	}
	if action == "ask_question" {
		d["question"] = map[string]any{"text": "Welche Variante?", "context": "Kontext", "recommendation": "Variante A"}
	}
	return d
}

// schemaDoc lädt eine eingebettete Schema-Datei als generisches JSON.
func schemaDoc(t *testing.T, file string) map[string]any {
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

// enumOf liest die Enum-Werte eines Schemaknotens und folgt dabei einem $ref auf common-v1.
func enumOf(t *testing.T, node map[string]any) []string {
	t.Helper()
	if ref, ok := node["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "common-v1.schema.json#/$defs/")
		node = schemaDoc(t, "common-v1.schema.json")["$defs"].(map[string]any)[name].(map[string]any)
	}
	var out []string
	for _, v := range node["enum"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// schemaEnum liest die Enum-Werte einer Eigenschaft direkt aus dem eingebetteten Schema.
func schemaEnum(t *testing.T, file, def, prop string) []string {
	t.Helper()
	node := schemaDoc(t, file)["$defs"].(map[string]any)[def].(map[string]any)["properties"].(map[string]any)[prop].(map[string]any)
	return enumOf(t, node)
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

// TestEnumsMatchSchema: die Go-Konstanten der Aufzählungen entsprechen den Schema-Enums.
func TestEnumsMatchSchema(t *testing.T) {
	strs := func(in any) []string {
		v := reflect.ValueOf(in)
		var out []string
		for i := 0; i < v.Len(); i++ {
			out = append(out, v.Index(i).String())
		}
		return out
	}
	check := func(name string, got []string, want []string) {
		t.Helper()
		if !slices.Equal(sorted(got), sorted(want)) {
			t.Errorf("%s: Go %v != Schema %v", name, sorted(got), sorted(want))
		}
	}
	check("work_order.state", strs(WorkOrderStates()), schemaEnum(t, "project-v1.schema.json", "work_order", "state"))
	check("work_order.mode", strs(IntegrationModes()), schemaEnum(t, "project-v1.schema.json", "work_order", "mode"))
	check("agent_profile.role", strs(Roles()), schemaEnum(t, "project-v1.schema.json", "agent_profile", "role"))
	check("agent_profile.runtime", strs(Runtimes()), schemaEnum(t, "project-v1.schema.json", "agent_profile", "runtime"))
	check("agent_profile.effort", strs(Efforts()), schemaEnum(t, "project-v1.schema.json", "agent_profile", "effort"))
	check("review_report.verdict", strs(Verdicts()), schemaEnum(t, "runner-v1.schema.json", "review_report", "verdict"))
	check("result.outcome", strs(Outcomes()), schemaEnum(t, "runner-v1.schema.json", "result", "outcome"))
	check("error.kind", strs(ErrorKinds()), schemaEnum(t, "runner-v1.schema.json", "error", "kind"))
	check("interrupt.reason", strs(StopReasons()), schemaEnum(t, "runner-v1.schema.json", "interrupt", "reason"))
	check("tool_event.phase", strs(ToolPhases()), schemaEnum(t, "runner-v1.schema.json", "tool_event", "phase"))
	check("activity.kind", strs(ActivityKinds()), schemaEnum(t, "runner-v1.schema.json", "activity", "kind"))
	check("decision.target.kind", strs(TargetKinds()), schemaNestedEnum(t, "decision-v1.schema.json", "decision", "target", "kind"))
}

func schemaNestedEnum(t *testing.T, file, def, prop, nested string) []string {
	t.Helper()
	node := schemaDoc(t, file)["$defs"].(map[string]any)[def].(map[string]any)["properties"].(map[string]any)[prop].(map[string]any)
	return enumOf(t, node["properties"].(map[string]any)[nested].(map[string]any))
}

// TestMessageTypesMatchSchema: Nachrichtentypen je Richtung stimmen mit dem Schema überein.
func TestMessageTypesMatchSchema(t *testing.T) {
	doc := schemaDoc(t, "runner-v1.schema.json")
	defs := doc["$defs"].(map[string]any)
	typeEnum := func(def string) []string {
		return enumOf(t, defs[def].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any))
	}
	var toRunner, fromRunner []string
	for _, m := range MessageTypesToRunner() {
		toRunner = append(toRunner, string(m))
	}
	for _, m := range MessageTypesFromRunner() {
		fromRunner = append(fromRunner, string(m))
	}
	if !slices.Equal(sorted(toRunner), sorted(typeEnum("envelope_to_runner"))) {
		t.Errorf("Go->Python: %v != Schema %v", toRunner, typeEnum("envelope_to_runner"))
	}
	if !slices.Equal(sorted(fromRunner), sorted(typeEnum("envelope_from_runner"))) {
		t.Errorf("Python->Go: %v != Schema %v", fromRunner, typeEnum("envelope_from_runner"))
	}
	all := append(slices.Clone(toRunner), fromRunner...)
	if !slices.Equal(sorted(all), sorted(typeEnum("envelope_common"))) {
		t.Errorf("alle Typen: %v != Schema %v", all, typeEnum("envelope_common"))
	}
	if got, want := doc["x-max-message-bytes"], float64(MaxMessageBytes); got != want {
		t.Errorf("x-max-message-bytes = %v, Go-Konstante = %v", got, want)
	}
}

// TestTimestampPatternMatchesCalendar: das Muster des Schemas lässt genau die Zeitstempel zu,
// die auch time.Parse als RFC 3339 akzeptiert, einschließlich Schaltjahren (Jahrhundertregel).
func TestTimestampPatternMatchesCalendar(t *testing.T) {
	common := schemaDoc(t, "common-v1.schema.json")["$defs"].(map[string]any)["timestamp"].(map[string]any)
	re := regexp.MustCompile(common["pattern"].(string))

	years := []int{1000, 1100, 1200, 1600, 1700, 1900, 2000, 2100, 2400, 9996, 9999}
	for y := 1990; y <= 2110; y++ {
		years = append(years, y)
	}
	for _, y := range years {
		for m := 1; m <= 12; m++ {
			for d := 1; d <= 31; d++ {
				ts := fmt.Sprintf("%04d-%02d-%02dT00:00:00Z", y, m, d)
				_, err := time.Parse(time.RFC3339, ts)
				if got, want := re.MatchString(ts), err == nil; got != want {
					t.Fatalf("%s: Muster = %v, time.Parse akzeptiert = %v", ts, got, want)
				}
			}
		}
	}
	for ts, want := range map[string]bool{
		"2026-11-10T12:00:00Z":            true,
		"2026-11-10T12:00:00.123456789Z":  true,
		"2026-11-10T12:00:00+01:00":       true,
		"2026-11-10T12:00:00-23:59":       true,
		"2026-11-10T12:00:00.Z":           false,
		"2026-11-10T12:00:00.1234567890Z": false,
		"2026-11-10T12:00:60Z":            false,
		"2026-11-10T12:60:00Z":            false,
		"2026-11-10T12:00:00+24:00":       false,
		"2026-11-10T12:00:00+0100":        false,
		"2026-11-10 12:00:00Z":            false,
		"0999-11-10T12:00:00Z":            false,
		"2026-11-10T12:00:00Z\n":          false,
	} {
		if got := re.MatchString(ts); got != want {
			t.Errorf("%q: Muster = %v, erwartet %v", ts, got, want)
		}
		if want {
			if _, err := time.Parse(time.RFC3339, ts); err != nil {
				t.Errorf("%q: time.Parse lehnt ab: %v", ts, err)
			}
		}
	}
}

// TestCategoriesMatchSchema: die schemaseitigen Fehlerklassen stehen in common-v1 und
// sind eine Teilmenge der Go-Klassen; die übrigen sind reine Folge- und Zustandsfehler.
func TestCategoriesMatchSchema(t *testing.T) {
	raw := schemaDoc(t, "common-v1.schema.json")["x-error-classes"].([]any)
	schemaLevel := map[Category]bool{}
	for _, c := range raw {
		schemaLevel[Category(c.(string))] = true
	}
	goOnly := map[Category]bool{CatSequence: true, CatStale: true, CatRevision: true, CatState: true}
	for _, c := range Categories() {
		if schemaLevel[c] == goOnly[c] {
			t.Errorf("Klasse %q: im Schema = %v, nur Go = %v", c, schemaLevel[c], goOnly[c])
		}
		delete(schemaLevel, c)
	}
	if len(schemaLevel) != 0 {
		t.Errorf("Schema nennt Klassen, die Go nicht kennt: %v", schemaLevel)
	}
}

// TestAnnotationsAreDeclaredClasses: jede x-error-class in den Schemas ist eine bekannte Klasse.
func TestAnnotationsAreDeclaredClasses(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range schemaDoc(t, "common-v1.schema.json")["x-error-classes"].([]any) {
		declared[c.(string)] = true
	}
	var walk func(file string, n any)
	walk = func(file string, n any) {
		switch v := n.(type) {
		case map[string]any:
			for k, child := range v {
				if k == "x-error-class" {
					if !declared[child.(string)] {
						t.Errorf("%s: unbekannte x-error-class %q", file, child)
					}
					continue
				}
				walk(file, child)
			}
		case []any:
			for _, child := range v {
				walk(file, child)
			}
		}
	}
	entries, err := contracts.Schemas.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		walk(e.Name(), schemaDoc(t, e.Name()))
	}
}
