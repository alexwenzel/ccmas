package domain

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/alexwenzel/ccmas/contracts"
)

// Größenbegrenzung der freien Objekte im Runner-Protokoll (CC-14): start.output_schema und
// result.structured_output sind Objekte, deren Inhalt der Auftrag bestimmt. Das Schema kann ihre
// Größe in Bytes nicht ausdrücken; es trägt dafür die Annotation x-max-bytes, und Go und Python
// prüfen sie nach der Schema-Prüfung auf derselben Grundlage: kanonische Form des Werts
// (sortierte Schlüssel, kompakt, UTF-8, keine Maskierung von <, > und &).

// payloadLimit beschreibt ein begrenztes freies Objekt: Nachrichtentyp, Definition und Feld im Schema.
type payloadLimit struct {
	target Target
	msg    MessageType
	def    string
	field  string
}

var payloadLimits = []payloadLimit{
	{TargetToRunner, MsgStart, "run_spec", "output_schema"},
	{TargetFromRunner, MsgResult, "result", "structured_output"},
}

// maxBytes liest x-max-bytes des Felds aus runner-v1.schema.json (0: nicht begrenzt).
func (e *engine) maxBytes(l payloadLimit) int {
	doc, _ := e.docs[contracts.BaseURL+"runner-v1.schema.json"].(map[string]any)
	node := doc
	for _, key := range []string{"$defs", l.def, "properties", l.field} {
		next, _ := node[key].(map[string]any)
		if next == nil {
			return 0
		}
		node = next
	}
	switch v := node["x-max-bytes"].(type) {
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case float64:
		return int(v)
	}
	return 0
}

// canonicalSize ist die Größe der kanonischen Form in Bytes.
func canonicalSize(v any) (int, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0, err
	}
	return buf.Len() - 1, nil // ohne das Zeilenende des Encoders
}

// checkPayloadLimits prüft die x-max-bytes-Grenzen einer geprüften Nachricht.
func (e *engine) checkPayloadLimits(t Target, inst any) *ContractError {
	msg, _ := inst.(map[string]any)
	if msg == nil {
		return nil
	}
	payload, _ := msg["payload"].(map[string]any)
	for _, l := range payloadLimits {
		if l.target != t || msg["type"] != string(l.msg) {
			continue
		}
		v, ok := payload[l.field]
		if !ok {
			continue
		}
		limit := e.maxBytes(l)
		if limit == 0 {
			continue
		}
		n, err := canonicalSize(v)
		if err != nil {
			return newError(CatValue, "/payload/"+l.field, "%s nicht serialisierbar: %v", l.field, err)
		}
		if n > limit {
			return newError(CatSize, "/payload/"+l.field, "%s hat %d Bytes (kanonische Form), erlaubt sind höchstens %d", l.field, n, limit)
		}
	}
	return nil
}

// CheckOutputSchema prüft ein Ausgabeschema (RunSpec.OutputSchema) gegen das Metaschema von Draft-07, nach dem das
// SDK die strukturierte Ausgabe validiert: ein Objekt, kompilierbar, ohne Verweise auf fremde Dokumente. Der Vertrag
// start prüft nur die oberste Ebene (Objekt, $schema, Schlüsselwörter von 2020-12, Größe); diese Prüfung ergänzt sie
// auf allen Ebenen, soweit Draft-07 sie kennt. Die Prüfung erfolgt vor dem Prozessstart, damit ein ungültiges Schema
// nie ein Modell aufruft.
func CheckOutputSchema(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return newError(CatValue, "/output_schema", "Ausgabeschema fehlt")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return newError(CatSyntax, "/output_schema", "Ausgabeschema ist kein gültiges JSON: %v", err)
	}
	if _, ok := doc.(map[string]any); !ok {
		return newError(CatValue, "/output_schema", "Ausgabeschema muss ein JSON-Objekt sein")
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	c.UseLoader(noNetwork{})
	const url = "https://ccmas.invalid/output-schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return newError(CatValue, "/output_schema", "Ausgabeschema nicht ladbar: %v", err)
	}
	if _, err := c.Compile(url); err != nil {
		return newError(CatValue, "/output_schema", "Ausgabeschema ist kein gültiges Draft-07-Schema: %s", fmt.Sprint(err))
	}
	return nil
}
