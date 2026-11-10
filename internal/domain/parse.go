package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeStrict dekodiert bereits schema-geprüftes JSON in einen Go-Typ und lehnt unbekannte
// Felder ab. Ein Fehler hier bedeutet eine Abweichung zwischen Schema und Go-Typ.
func decodeStrict(raw []byte, into any) *ContractError {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return newError(CatValue, "", "typisiertes Dekodieren: %v", err)
	}
	return nil
}

func parseInto[T any](t Target, raw []byte, into *T) error {
	if _, err := validate(t, raw); err != nil {
		return err
	}
	if cerr := decodeStrict(raw, into); cerr != nil {
		return cerr
	}
	return nil
}

// ParseProjectConfig prüft und dekodiert eine ProjectConfig; fehlende optionale Felder
// erhalten die Standardwerte (max_workers=3, Modus pr, ...). Anschließend prüft Check.
func ParseProjectConfig(raw []byte) (ProjectConfig, error) {
	cfg := DefaultProjectConfig()
	if err := parseInto(TargetProjectConfig, raw, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := cfg.Check(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

// ParseAgentProfile prüft und dekodiert ein AgentProfile.
func ParseAgentProfile(raw []byte) (AgentProfile, error) {
	var v AgentProfile
	if err := parseInto(TargetAgentProfile, raw, &v); err != nil {
		return AgentProfile{}, err
	}
	return v, nil
}

// ParseWorkOrder prüft und dekodiert einen WorkOrder.
func ParseWorkOrder(raw []byte) (WorkOrder, error) {
	var v WorkOrder
	if err := parseInto(TargetWorkOrder, raw, &v); err != nil {
		return WorkOrder{}, err
	}
	return v, nil
}

// ParseDecision prüft und dekodiert eine Decision formal. Ausführbar ist sie erst nach Check.
func ParseDecision(raw []byte) (Decision, error) {
	var v Decision
	if err := parseInto(TargetDecision, raw, &v); err != nil {
		return Decision{}, err
	}
	return v, nil
}

// ParseIntegrationRequest prüft und dekodiert einen IntegrationRequest.
func ParseIntegrationRequest(raw []byte) (IntegrationRequest, error) {
	var v IntegrationRequest
	if err := parseInto(TargetIntegrationRequest, raw, &v); err != nil {
		return IntegrationRequest{}, err
	}
	return v, nil
}

// ParseReviewReport prüft und dekodiert einen ReviewReport und seine Invarianten.
func ParseReviewReport(raw []byte) (ReviewReport, error) {
	var v ReviewReport
	if err := parseInto(TargetReviewReport, raw, &v); err != nil {
		return ReviewReport{}, err
	}
	if err := v.Check(); err != nil {
		return ReviewReport{}, err
	}
	return v, nil
}

// ParseSkillSnapshot prüft und dekodiert die snapshot.json eines Skill-Snapshots samt
// ihrer strukturellen Invarianten (SkillSnapshot.Check).
func ParseSkillSnapshot(raw []byte) (SkillSnapshot, error) {
	var v SkillSnapshot
	if err := parseInto(TargetSkillSnapshot, raw, &v); err != nil {
		return SkillSnapshot{}, err
	}
	if err := v.Check(); err != nil {
		return SkillSnapshot{}, err
	}
	return v, nil
}

// Parse prüft und dekodiert ein Dokument des Ziels t und liefert den typisierten Wert:
// RunCommand, RunEvent, ProjectConfig, AgentProfile, WorkOrder, Decision,
// IntegrationRequest, ReviewReport oder SkillSnapshot.
func Parse(t Target, raw []byte) (any, error) {
	switch t {
	case TargetToRunner:
		return ParseRunCommand(raw)
	case TargetFromRunner:
		return ParseRunEvent(raw)
	case TargetProjectConfig:
		return ParseProjectConfig(raw)
	case TargetAgentProfile:
		return ParseAgentProfile(raw)
	case TargetWorkOrder:
		return ParseWorkOrder(raw)
	case TargetDecision:
		return ParseDecision(raw)
	case TargetIntegrationRequest:
		return ParseIntegrationRequest(raw)
	case TargetReviewReport:
		return ParseReviewReport(raw)
	case TargetSkillSnapshot:
		return ParseSkillSnapshot(raw)
	}
	return nil, newError(CatValue, "", "unbekanntes Ziel %q", t)
}

// ---- Runner-Nachrichten -----------------------------------------------------

// ParseRunEvent prüft und dekodiert eine Nachricht Python -> Go.
func ParseRunEvent(raw []byte) (RunEvent, error) {
	env, payload, err := parseMessage(TargetFromRunner, raw, newFromRunnerPayload)
	if err != nil {
		return RunEvent{}, err
	}
	return RunEvent{Envelope: env, Payload: payload}, nil
}

// ParseRunCommand prüft und dekodiert eine Nachricht Go -> Python.
func ParseRunCommand(raw []byte) (RunCommand, error) {
	env, payload, err := parseMessage(TargetToRunner, raw, newToRunnerPayload)
	if err != nil {
		return RunCommand{}, err
	}
	return RunCommand{Envelope: env, Payload: payload}, nil
}

func parseMessage(t Target, raw []byte, newPayload func(MessageType) any) (Envelope, any, error) {
	if _, err := validate(t, raw); err != nil {
		return Envelope{}, nil, err
	}
	var env Envelope
	if cerr := decodeStrict(raw, &env); cerr != nil {
		return Envelope{}, nil, cerr
	}
	p := newPayload(env.Type)
	if p == nil {
		return Envelope{}, nil, newError(CatMessageType, "/type", "unbekannter Nachrichtentyp %q", env.Type)
	}
	if cerr := decodeStrict(env.Payload, p); cerr != nil {
		cerr.Location = "/payload"
		return Envelope{}, nil, cerr
	}
	return env, deref(p), nil
}

// newFromRunnerPayload liefert einen Zeiger auf den Payload-Typ des Nachrichtentyps.
func newFromRunnerPayload(t MessageType) any {
	switch t {
	case MsgSessionStarted:
		return new(SessionStarted)
	case MsgActivity:
		return new(Activity)
	case MsgToolEvent:
		return new(ToolEvent)
	case MsgQuestion:
		return new(QuestionPayload)
	case MsgResult:
		return new(Result)
	case MsgError:
		return new(ErrorPayload)
	}
	return nil
}

func newToRunnerPayload(t MessageType) any {
	switch t {
	case MsgStart:
		return new(RunSpec)
	case MsgAnswer:
		return new(Answer)
	case MsgInterrupt:
		return new(InterruptPayload)
	case MsgStop:
		return new(StopPayload)
	}
	return nil
}

func deref(p any) any {
	switch v := p.(type) {
	case *SessionStarted:
		return *v
	case *Activity:
		return *v
	case *ToolEvent:
		return *v
	case *QuestionPayload:
		return *v
	case *Result:
		return *v
	case *ErrorPayload:
		return *v
	case *RunSpec:
		return *v
	case *Answer:
		return *v
	case *InterruptPayload:
		return *v
	case *StopPayload:
		return *v
	}
	return p
}

// messageTypeOf ordnet einen Payload-Wert seinem Nachrichtentyp zu.
func messageTypeOf(payload any) (MessageType, bool) {
	switch payload.(type) {
	case RunSpec:
		return MsgStart, true
	case Answer:
		return MsgAnswer, true
	case InterruptPayload:
		return MsgInterrupt, true
	case StopPayload:
		return MsgStop, true
	case SessionStarted:
		return MsgSessionStarted, true
	case Activity:
		return MsgActivity, true
	case ToolEvent:
		return MsgToolEvent, true
	case QuestionPayload:
		return MsgQuestion, true
	case Result:
		return MsgResult, true
	case ErrorPayload:
		return MsgError, true
	}
	return "", false
}

func marshalMessage(env Envelope, payload any) ([]byte, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	env.Payload = rawPayload
	return json.Marshal(env)
}

// MarshalJSON serialisiert das Ereignis als Runner-Nachricht.
func (e RunEvent) MarshalJSON() ([]byte, error) { return marshalMessage(e.Envelope, e.Payload) }

// MarshalJSON serialisiert das Kommando als Runner-Nachricht.
func (c RunCommand) MarshalJSON() ([]byte, error) { return marshalMessage(c.Envelope, c.Payload) }

// EncodeRunCommand baut die JSON-Zeile eines Kommandos Go -> Python. Typ und Payload
// müssen zusammenpassen; das Ergebnis wird vor dem Senden gegen den Vertrag geprüft, damit
// nichts Ungültiges oder Übergroßes den Prozess verlässt.
func EncodeRunCommand(env Envelope, payload any) ([]byte, error) {
	if !contains(MessageTypesToRunner(), env.Type) {
		return nil, newError(CatMessageType, "/type", "Typ %q wird nicht von Go an den Runner gesendet", env.Type)
	}
	if want, ok := messageTypeOf(payload); !ok || want != env.Type {
		return nil, newError(CatMessageType, "/payload", "Payload %T passt nicht zum Typ %q", payload, env.Type)
	}
	if a, ok := payload.(Answer); ok && a.DeliveryConfirmedAt != nil {
		return nil, newError(CatValue, "/payload/delivery_confirmed_at", "die Zustellbestätigung entsteht erst nach dem Senden")
	}
	raw, err := marshalMessage(env, payload)
	if err != nil {
		return nil, err
	}
	if err := Validate(TargetToRunner, raw); err != nil {
		return nil, err
	}
	return raw, nil
}
