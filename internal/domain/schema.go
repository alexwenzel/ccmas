package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/alexwenzel/ccmas/contracts"
)

// Target bezeichnet einen Vertrag, gegen den ein Dokument geprüft wird. Die Namen sind
// zugleich das Präfix der gemeinsamen Fixtures und die Zielnamen der Python-Seite.
type Target string

const (
	TargetToRunner           Target = "to-runner"           // Nachricht Go -> Python
	TargetFromRunner         Target = "from-runner"         // Nachricht Python -> Go
	TargetProjectConfig      Target = "project-config"      // ProjectConfig
	TargetAgentProfile       Target = "agent-profile"       // AgentProfile
	TargetWorkOrder          Target = "work-order"          // WorkOrder
	TargetDecision           Target = "decision"            // Decision
	TargetDecisionBatch      Target = "decision-batch"      // DecisionBatch (plan-v1)
	TargetIntegrationRequest Target = "integration-request" // IntegrationRequest
	TargetReviewReport       Target = "review-report"       // ReviewReport
	TargetSkillSnapshot      Target = "skill-snapshot"      // snapshot.json eines Skill-Snapshots
)

type targetSpec struct{ file, def string }

var targetSpecs = map[Target]targetSpec{
	TargetToRunner:           {"runner-v1.schema.json", "envelope_to_runner"},
	TargetFromRunner:         {"runner-v1.schema.json", "envelope_from_runner"},
	TargetProjectConfig:      {"project-v1.schema.json", "project_config"},
	TargetAgentProfile:       {"project-v1.schema.json", "agent_profile"},
	TargetWorkOrder:          {"project-v1.schema.json", "work_order"},
	TargetDecision:           {"decision-v1.schema.json", "decision"},
	TargetDecisionBatch:      {"plan-v1.schema.json", "decision_batch"},
	TargetIntegrationRequest: {"decision-v1.schema.json", "integration_request"},
	TargetReviewReport:       {"runner-v1.schema.json", "review_report"},
	TargetSkillSnapshot:      {"skill-snapshot-v1.schema.json", "skill_snapshot"},
}

// Targets liefert alle Prüfziele in fester Reihenfolge.
func Targets() []Target {
	return []Target{
		TargetToRunner, TargetFromRunner, TargetProjectConfig, TargetAgentProfile,
		TargetWorkOrder, TargetDecision, TargetDecisionBatch, TargetIntegrationRequest, TargetReviewReport,
		TargetSkillSnapshot,
	}
}

// engine hält die einmal kompilierten Schemas und die Rohdokumente für x-error-class.
type engine struct {
	schemas map[Target]*jsonschema.Schema
	docs    map[string]any // URL der Schema-Datei -> Rohdokument
}

// noNetwork verhindert, dass ein unerwarteter $ref über das Netzwerk oder Dateisystem lädt.
type noNetwork struct{}

func (noNetwork) Load(u string) (any, error) {
	return nil, fmt.Errorf("schema %s ist nicht eingebettet; Laden ist nicht erlaubt", u)
}

var loadEngine = sync.OnceValues(func() (*engine, error) {
	entries, err := contracts.Schemas.ReadDir(".")
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noNetwork{})
	e := &engine{schemas: map[Target]*jsonschema.Schema{}, docs: map[string]any{}}
	for _, ent := range entries {
		raw, err := contracts.Schemas.ReadFile(ent.Name())
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ent.Name(), err)
		}
		if err := c.AddResource(contracts.BaseURL+ent.Name(), doc); err != nil {
			return nil, err
		}
		plain, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		e.docs[contracts.BaseURL+ent.Name()] = plain
	}
	for t, spec := range targetSpecs {
		sch, err := c.Compile(contracts.BaseURL + spec.file + "#/$defs/" + spec.def)
		if err != nil {
			return nil, fmt.Errorf("schema für %s: %w", t, err)
		}
		e.schemas[t] = sch
	}
	return e, nil
})

// Validate prüft ein Dokument strikt gegen den Vertrag: Größe, JSON-Syntax (genau ein
// Objekt, keine doppelten Schlüssel, keine Kommazahlen), dann das Schema. Fehler sind
// *ContractError mit differenzierter Klasse.
func Validate(t Target, raw []byte) error {
	_, err := validate(t, raw)
	return err
}

func validate(t Target, raw []byte) (any, error) {
	e, err := loadEngine()
	if err != nil {
		return nil, fmt.Errorf("verträge nicht ladbar: %w", err)
	}
	sch, ok := e.schemas[t]
	if !ok {
		return nil, newError(CatValue, "", "unbekanntes Ziel %q", t)
	}
	if len(raw) > MaxMessageBytes {
		return nil, newError(CatSize, "", "Nachricht hat %d Bytes, erlaubt sind höchstens %d", len(raw), MaxMessageBytes)
	}
	if cerr := strictParse(raw); cerr != nil {
		return nil, cerr
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, newError(CatSyntax, "", "kein gültiges JSON: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return nil, e.classify(ve)
		}
		return nil, newError(CatValue, "", "schema-prüfung: %v", err)
	}
	return inst, nil
}

// maxNesting schützt den rekursiven Strict-Parser vor absurder Verschachtelung.
const maxNesting = 1000

// strictParse prüft, was das Schema nicht sieht: gültiges UTF-8, genau ein JSON-Objekt,
// keine Folgedaten, keine doppelten Schlüssel und keine Kommazahlen (alle Zahlen der
// Verträge sind Ganzzahlen; Nachkomma- und Exponentenschreibweise sind unzulässig).
func strictParse(raw []byte) *ContractError {
	if !utf8.Valid(raw) {
		return newError(CatSyntax, "", "ungültiges UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return newError(CatSyntax, "", "kein gültiges JSON: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return newError(CatSyntax, "", "ein JSON-Objekt wird erwartet")
	}
	if cerr := walkObject(dec, "", 1); cerr != nil {
		return cerr
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return newError(CatSyntax, "", "unerwartete Daten nach dem JSON-Objekt")
	}
	return nil
}

func walkObject(dec *json.Decoder, loc string, depth int) *ContractError {
	if depth > maxNesting {
		return newError(CatSyntax, loc, "Verschachtelung zu tief")
	}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return newError(CatSyntax, loc, "kein gültiges JSON: %v", err)
		}
		key, _ := keyTok.(string)
		if seen[key] {
			return newError(CatSyntax, loc, "doppelter Schlüssel %q", key)
		}
		seen[key] = true
		if cerr := walkValue(dec, loc+"/"+escapePointer(key), depth); cerr != nil {
			return cerr
		}
	}
	if _, err := dec.Token(); err != nil { // schließende Klammer
		return newError(CatSyntax, loc, "kein gültiges JSON: %v", err)
	}
	return nil
}

func walkArray(dec *json.Decoder, loc string, depth int) *ContractError {
	if depth > maxNesting {
		return newError(CatSyntax, loc, "Verschachtelung zu tief")
	}
	for i := 0; dec.More(); i++ {
		if cerr := walkValue(dec, loc+"/"+strconv.Itoa(i), depth); cerr != nil {
			return cerr
		}
	}
	if _, err := dec.Token(); err != nil {
		return newError(CatSyntax, loc, "kein gültiges JSON: %v", err)
	}
	return nil
}

func walkValue(dec *json.Decoder, loc string, depth int) *ContractError {
	tok, err := dec.Token()
	if err != nil {
		return newError(CatSyntax, loc, "kein gültiges JSON: %v", err)
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			return walkObject(dec, loc, depth+1)
		}
		return walkArray(dec, loc, depth+1)
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return newError(CatValue, loc, "Zahl %s: nur Ganzzahlen ohne Nachkomma- und Exponentenschreibweise sind zulässig", v)
		}
	}
	return nil
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// classify wandelt die Blattfehler der Schema-Prüfung in einen ContractError. Die Klasse
// eines Blatts ergibt sich aus dem verletzten Schlüsselwort (required, unbekannte Felder),
// sonst aus x-error-class des verletzten Teilschemas, sonst aus der Größenbegrenzung
// (maxLength, maxItems, maxProperties), sonst value. Python wendet dieselbe Regel an.
func (e *engine) classify(ve *jsonschema.ValidationError) *ContractError {
	var leaves []*jsonschema.ValidationError
	var walk func(v *jsonschema.ValidationError)
	walk = func(v *jsonschema.ValidationError) {
		if len(v.Causes) == 0 {
			leaves = append(leaves, v)
			return
		}
		for _, c := range v.Causes {
			walk(c)
		}
	}
	walk(ve)

	var cats []Category
	var locs, msgs []string
	for _, l := range leaves {
		cat := e.leafCategory(l)
		cats = append(cats, cat)
		loc := ""
		if len(l.InstanceLocation) > 0 {
			parts := make([]string, len(l.InstanceLocation))
			for i, p := range l.InstanceLocation {
				parts[i] = escapePointer(p)
			}
			loc = "/" + strings.Join(parts, "/")
		}
		locs = append(locs, loc)
		msgs = append(msgs, fmt.Sprintf("%s verletzt %s", orRoot(loc), keywordOf(l)))
	}
	sort.Strings(msgs)
	loc := ""
	if len(locs) > 0 {
		loc = locs[0]
	}
	return &ContractError{Categories: categorySet(cats), Location: loc, Message: strings.Join(msgs, "; ")}
}

func orRoot(loc string) string {
	if loc == "" {
		return "/"
	}
	return loc
}

func keywordOf(l *jsonschema.ValidationError) string {
	switch l.ErrorKind.(type) {
	case *kind.Not:
		return "not"
	case *kind.FalseSchema:
		return "unbekanntes Feld"
	}
	if kp := l.ErrorKind.KeywordPath(); len(kp) > 0 {
		return kp[len(kp)-1]
	}
	return "Schema"
}

func (e *engine) leafCategory(l *jsonschema.ValidationError) Category {
	switch l.ErrorKind.(type) {
	case *kind.Required:
		return CatField
	case *kind.AdditionalProperties, *kind.FalseSchema: // additional-/unevaluatedProperties: false
		return CatUnknownField
	}
	if ann := e.annotation(l.SchemaURL); ann != "" {
		return Category(ann)
	}
	switch keywordOf(l) {
	case "maxLength", "maxItems", "maxProperties":
		return CatSize
	}
	return CatValue
}

// annotation liest x-error-class des Teilschemas, in dem das verletzte Schlüsselwort steht.
func (e *engine) annotation(schemaURL string) string {
	base, frag, _ := strings.Cut(schemaURL, "#")
	doc, ok := e.docs[base]
	if !ok {
		return ""
	}
	node := doc
	if frag != "" {
		frag, err := url.PathUnescape(frag)
		if err != nil {
			return ""
		}
		for _, part := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch n := node.(type) {
			case map[string]any:
				node = n[part]
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(n) {
					return ""
				}
				node = n[i]
			default:
				return ""
			}
		}
	}
	if m, ok := node.(map[string]any); ok {
		if s, ok := m["x-error-class"].(string); ok {
			return s
		}
	}
	return ""
}
