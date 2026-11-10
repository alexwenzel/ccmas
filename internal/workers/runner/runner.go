package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Standardwerte der Konfiguration.
const (
	DefaultStopGrace       = 10 * time.Second
	DefaultKillDelay       = 3 * time.Second
	DefaultStderrLimit     = 16 << 10
	DefaultMaxEvents       = 10000
	drainTimeout           = 2 * time.Second
	maxViolations          = 100
	maxViolationMessageLen = 300
)

// Config konfiguriert den Adapter. Der Nullwert ist bis auf SoftwareRoot (oder Command)
// brauchbar.
type Config struct {
	// SoftwareRoot ist der CCMAS-Softwareordner: Quelle des Runner-Projekts
	// (<root>/runners/claude_sdk) und der Schemas (<root>/contracts).
	SoftwareRoot string
	// Command ersetzt den Standardbefehl (nur für Tests). Der Standard ist
	// uv run --frozen --project <SoftwareRoot>/runners/claude_sdk python -m claude_sdk_runner.
	Command []string
	// MaxTurns begrenzt die Runden der Sitzung (--max-turns); 0: nicht angegeben.
	MaxTurns int
	// AllowScopedWrite lässt das Prüfprofil scoped_write zu (nur für Prüfläufe).
	AllowScopedWrite bool

	// StopGrace ist die Frist nach stop bis zum Beenden der Prozessgruppe; KillDelay die Frist
	// zwischen SIGTERM und SIGKILL.
	StopGrace time.Duration
	KillDelay time.Duration
	// StderrLimit und MaxEvents begrenzen, was der Datensatz festhält.
	StderrLimit int
	MaxEvents   int

	// Getenv liefert die Umgebung des Go-Prozesses für die Allowlist (Standard os.Getenv).
	Getenv func(string) string
	// Now liefert die Zeit (Standard time.Now).
	Now func() time.Time
}

// Runner startet Runner-Prozesse. Er ist nach New unveränderlich und nebenläufig benutzbar.
type Runner struct{ cfg Config }

// New legt einen Adapter an und setzt die Standardwerte.
func New(cfg Config) *Runner {
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = DefaultStopGrace
	}
	if cfg.KillDelay <= 0 {
		cfg.KillDelay = DefaultKillDelay
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = DefaultStderrLimit
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = DefaultMaxEvents
	}
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Runner{cfg: cfg}
}

// RunnerProjectDir ist das Python-Projekt des Runners im Softwareordner.
func RunnerProjectDir(softwareRoot string) string {
	return filepath.Join(softwareRoot, "runners", "claude_sdk")
}

// command liefert den Befehl des Runner-Prozesses.
func (r *Runner) command() ([]string, error) {
	if len(r.cfg.Command) > 0 {
		return append([]string{}, r.cfg.Command...), nil
	}
	if r.cfg.SoftwareRoot == "" {
		return nil, errors.New("Softwareordner nicht bekannt: Runner-Projekt nicht auffindbar")
	}
	uv, err := exec.LookPath("uv")
	if err != nil {
		return nil, errors.New("uv nicht gefunden (PATH)")
	}
	argv := []string{uv, "run", "--frozen", "--project", RunnerProjectDir(r.cfg.SoftwareRoot), "python", "-m", "claude_sdk_runner"}
	if r.cfg.MaxTurns > 0 {
		argv = append(argv, "--max-turns", strconv.Itoa(r.cfg.MaxTurns))
	}
	return argv, nil
}

func (r *Runner) contractsDir() string {
	if r.cfg.SoftwareRoot == "" {
		return ""
	}
	return filepath.Join(r.cfg.SoftwareRoot, "contracts")
}

// Execute startet die Ausführung und wartet auf ihr Ende. Ein Fehler bedeutet, dass der Prozess
// nicht gestartet wurde (ungültige Angaben, nicht zugelassene Policy, Startfehler); jedes Ende
// eines gestarteten Prozesses, auch ein Absturz, steht im Datensatz.
func (r *Runner) Execute(ctx context.Context, req Request) (Execution, error) {
	p, err := r.Start(ctx, req)
	if err != nil {
		return Execution{}, err
	}
	return p.Wait(), nil
}

// Run ist eine laufende Ausführung.
type Run struct {
	r       *Runner
	req     Request
	env     domain.Envelope
	cmd     *exec.Cmd
	tracker *domain.ExecutionTracker
	cleanup []func()

	stdin   io.WriteCloser
	stdinMu sync.Mutex

	stopReq chan domain.StopReason
	done    chan struct{}

	mu          sync.Mutex
	exec        Execution
	stderr      tailBuffer
	stopSent    bool
	forcedKill  bool
	stderrReady chan struct{}
	readReady   chan struct{}
	result      Execution
}

// Start startet den Runner-Prozess und sendet den Start. Das Ende des Kontexts löst stop aus
// und nach der Frist das Beenden der Prozessgruppe.
func (r *Runner) Start(ctx context.Context, req Request) (*Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := r.normalize(req)
	if err != nil {
		return nil, err
	}
	spec, err := r.BuildSpec(req)
	if err != nil {
		return nil, err
	}
	env := domain.Envelope{
		ProtocolVersion: domain.ProtocolVersion, ProjectID: req.ProjectID, ExecutionID: req.ExecutionID,
		Attempt: req.Attempt, CorrelationID: req.CorrelationID, Type: domain.MsgStart, Timestamp: r.cfg.Now().UTC().Truncate(time.Second),
	}
	startLine, err := domain.EncodeRunCommand(env, spec)
	if err != nil {
		return nil, fmt.Errorf("Start nicht gültig: %w", err)
	}
	argv, err := r.command()
	if err != nil {
		return nil, err
	}

	// Neutrales Arbeitsverzeichnis des Prozesses: weder der Ziel-Worktree noch der Softwareordner.
	neutral, err := os.MkdirTemp("", "ccmas-runner-")
	if err != nil {
		return nil, fmt.Errorf("neutrales Arbeitsverzeichnis nicht anlegbar: %w", err)
	}
	p := &Run{r: r, req: req, env: env, tracker: domain.NewExecutionTracker(req.ProjectID, req.ExecutionID, req.Attempt),
		stopReq: make(chan domain.StopReason, 1), done: make(chan struct{}),
		stderrReady: make(chan struct{}), readReady: make(chan struct{}), stderr: tailBuffer{limit: r.cfg.StderrLimit}}
	p.cleanup = append(p.cleanup, func() { _ = os.RemoveAll(neutral) })

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = neutral
	cmd.Env = Environment(r.cfg.Getenv, r.contractsDir())
	configureProcess(cmd)
	outR, outW, err := os.Pipe()
	if err != nil {
		p.close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		p.close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	stdin, err := cmd.StdinPipe()
	if err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		p.close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		p.close()
		return nil, fmt.Errorf("Runner-Prozess nicht startbar (%s): %w", filepath.Base(argv[0]), err)
	}
	outW.Close()
	errW.Close()
	p.cmd, p.stdin = cmd, stdin
	p.exec = Execution{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID, Attempt: req.Attempt,
		Profile: cloneProfile(req.Profile), Spec: specRecord(spec), Events: []domain.RunEvent{},
		State: domain.ExecInitializing, StartedAt: r.cfg.Now().UTC(),
	}
	if spec.SkillSnapshot != nil {
		p.exec.SkillSnapshotID = spec.SkillSnapshot.SnapshotID
	}

	// Der Start geht zuerst auf stdin; stop und interrupt warten darauf (ein Prozess, der nicht liest,
	// blockiert hier nur diesen Schreiber, nicht das Beenden).
	p.stdinMu.Lock()
	go func() {
		defer p.stdinMu.Unlock()
		_, _ = stdin.Write(append(startLine, '\n'))
	}()
	go p.readEvents(outR)
	go p.readStderr(errR)
	go p.run(ctx, outR, errR)
	return p, nil
}

func (r *Runner) normalize(req Request) (Request, error) {
	if req.Attempt == 0 {
		req.Attempt = 1
	}
	if req.Attempt < 1 {
		return req, fmt.Errorf("Versuch %d ungültig", req.Attempt)
	}
	for name, id := range map[string]string{"project_id": req.ProjectID, "execution_id": req.ExecutionID} {
		if err := domain.CheckID(id); err != nil {
			return req, fmt.Errorf("%s: %w", name, err)
		}
	}
	if req.CorrelationID == "" {
		req.CorrelationID = req.ExecutionID + "-start"
	}
	if req.WorkOrderID == "" {
		req.WorkOrderID = req.ExecutionID
	}
	if req.MaxRuntimeSeconds == 0 {
		req.MaxRuntimeSeconds = DefaultMaxRuntimeSeconds
	}
	if req.Prompt == "" {
		return req, errors.New("Prompt fehlt")
	}
	if req.Cwd == "" {
		return req, errors.New("Sitzungsverzeichnis (cwd) fehlt")
	}
	real, err := filepath.EvalSymlinks(req.Cwd)
	if err != nil {
		return req, fmt.Errorf("Sitzungsverzeichnis %s nicht auflösbar: %w", req.Cwd, err)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return req, fmt.Errorf("Sitzungsverzeichnis %s ist kein Verzeichnis", req.Cwd)
	}
	if abs, err := filepath.Abs(real); err == nil {
		real = abs
	}
	req.Cwd = real
	if req.ClaudeBinary == "" {
		return req, errors.New("Claude-Binary fehlt (siehe ResolveClaudeBinary)")
	}
	if req.Profile.Runtime == "" {
		return req, errors.New("Profil ohne Runtime")
	}
	return req, nil
}

func (p *Run) close() {
	for _, f := range p.cleanup {
		f()
	}
	p.cleanup = nil
}

// Wait wartet auf das Ende und liefert den Ausführungsdatensatz.
func (p *Run) Wait() Execution {
	<-p.done
	return p.result
}

// State liefert den aktuellen Ausführungszustand.
func (p *Run) State() domain.ExecutionState { return p.tracker.State() }

// Stop fordert das kontrollierte Ende an: stop an den Runner, nach der Frist das Beenden der Prozessgruppe.
func (p *Run) Stop(reason domain.StopReason) {
	select {
	case p.stopReq <- reason:
	default:
	}
}

// Interrupt sendet interrupt an den Runner (ohne Frist und ohne Beenden der Prozessgruppe).
func (p *Run) Interrupt(reason domain.StopReason) error {
	return p.send(domain.MsgInterrupt, domain.InterruptPayload{Reason: reason}, false)
}

func (p *Run) send(t domain.MessageType, payload any, stopping bool) error {
	env := p.env
	env.Type = t
	env.CorrelationID = p.req.ExecutionID + "-" + string(t)
	env.Timestamp = p.r.cfg.Now().UTC().Truncate(time.Second)
	line, err := domain.EncodeRunCommand(env, payload)
	if err != nil {
		return err
	}
	if stopping {
		if err := p.tracker.StopSent(); err != nil {
			return err
		}
		p.mu.Lock()
		p.stopSent = true
		p.mu.Unlock()
	}
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	_, err = p.stdin.Write(append(line, '\n'))
	return err
}

func (p *Run) violate(cat domain.Category, msg string) {
	if len(msg) > maxViolationMessageLen {
		msg = msg[:maxViolationMessageLen] + "…"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.exec.Violations) < maxViolations {
		p.exec.Violations = append(p.exec.Violations, Violation{Category: cat, Message: msg})
	}
}

// readEvents liest stdout zeilenweise mit Größengrenze, prüft jede Zeile gegen den Vertrag
// from-runner und führt den Tracker. Ungültige, übergroße, verspätete und außerhalb der Reihenfolge
// liegende Nachrichten werden festgehalten und verändern den Zustand nicht.
func (p *Run) readEvents(stdout io.Reader) {
	defer close(p.readReady)
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, oversize, err := readLine(br, domain.MaxMessageBytes)
		if err != nil {
			return
		}
		if oversize {
			p.violate(domain.CatSize, fmt.Sprintf("Zeile über %d Bytes verworfen", domain.MaxMessageBytes))
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, err := domain.ParseRunEvent(line)
		if err != nil {
			var ce *domain.ContractError
			cat := domain.CatSyntax
			if errors.As(err, &ce) {
				cat = ce.Category()
			}
			p.violate(cat, err.Error())
			continue
		}
		if err := p.tracker.Apply(ev); err != nil {
			var ce *domain.ContractError
			cat := domain.CatSequence
			if errors.As(err, &ce) {
				cat = ce.Category()
			}
			p.violate(cat, fmt.Sprintf("%s: %v", ev.Envelope.Type, err))
			continue
		}
		p.record(ev)
	}
}

func (p *Run) record(ev domain.RunEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ss, ok := ev.Payload.(domain.SessionStarted); ok && p.exec.SessionStarted == nil {
		c := ss
		c.Skills = append([]string(nil), ss.Skills...)
		p.exec.SessionStarted = &c
	}
	_, isResult := ev.Payload.(domain.Result)
	e, isError := ev.Payload.(domain.ErrorPayload)
	terminal := isResult || (isError && e.Terminal)
	if len(p.exec.Events) >= p.r.cfg.MaxEvents && !terminal {
		p.exec.DroppedEvents++
		return
	}
	p.exec.Events = append(p.exec.Events, ev)
}

func (p *Run) readStderr(stderr io.Reader) {
	defer close(p.stderrReady)
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.stderr.write(buf[:n])
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// run überwacht den Prozess: Stop und Kontextende, Fristen, Wait, Aufräumen der Prozessgruppe und
// Abschluss des Datensatzes.
func (p *Run) run(ctx context.Context, outR, errR *os.File) {
	defer close(p.done)
	waitCh := make(chan error, 1)
	go func() { waitCh <- p.cmd.Wait() }()

	var (
		waitErr   error
		ctxDone   = ctx.Done()
		stopping  bool
		graceC    <-chan time.Time
		killC     <-chan time.Time
		graceTime *time.Timer
		killTime  *time.Timer
	)
	beginStop := func(reason domain.StopReason) {
		if stopping {
			return
		}
		stopping = true
		go func() { _ = p.send(domain.MsgStop, domain.StopPayload{Reason: reason}, true) }()
		graceTime = time.NewTimer(p.r.cfg.StopGrace)
		graceC = graceTime.C
	}
loop:
	for {
		select {
		case waitErr = <-waitCh:
			break loop
		case <-ctxDone:
			ctxDone = nil
			reason := domain.ReasonUserRequest
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				reason = domain.ReasonTimeout
			}
			beginStop(reason)
		case reason := <-p.stopReq:
			beginStop(reason)
		case <-graceC:
			graceC = nil
			p.mu.Lock()
			p.forcedKill = true
			p.mu.Unlock()
			signalGroup(p.cmd, false)
			killTime = time.NewTimer(p.r.cfg.KillDelay)
			killC = killTime.C
		case <-killC:
			killC = nil
			signalGroup(p.cmd, true)
		}
	}
	for _, t := range []*time.Timer{graceTime, killTime} {
		if t != nil {
			t.Stop()
		}
	}

	// Der Runner ist beendet. Nach Abbruch oder ohne terminales Ergebnis bleiben keine Kindprozesse
	// der Gruppe übrig.
	_, hasTerminal := p.tracker.Terminal()
	if stopping || !hasTerminal {
		signalGroup(p.cmd, true)
	}
	// Alles Geschriebene lesen; hält ein Kindprozess die Pipe weiter offen, wird sie nach einer Frist geschlossen.
	deadline := time.Now().Add(drainTimeout)
	waitOrClose(p.readReady, outR, deadline)
	waitOrClose(p.stderrReady, errR, deadline)
	outR.Close()
	errR.Close()

	exit, note := exitStatus(p.cmd, waitErr)
	_ = p.tracker.ProcessExited(p.req.Attempt, exit)
	p.finish(exit, note)
	p.close()
}

// waitOrClose wartet bis deadline auf ready und schließt sonst f, damit der Leser endet.
func waitOrClose(ready <-chan struct{}, f *os.File, deadline time.Time) {
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-ready:
	case <-t.C:
		f.Close()
		<-ready
	}
}

func exitStatus(cmd *exec.Cmd, waitErr error) (int, string) {
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			return code, ""
		}
		return -1, cmd.ProcessState.String()
	}
	if waitErr != nil {
		return -1, waitErr.Error()
	}
	return -1, "unbekannt"
}

func (p *Run) finish(exit int, note string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.exec
	e.State = p.tracker.State()
	if t, ok := p.tracker.Terminal(); ok {
		tc := t
		e.Terminal = &tc
	}
	e.Crashed = e.State == domain.ExecCrashed
	e.ExitCode, e.ExitNote = exit, note
	e.StopSent, e.ForcedKill = p.stopSent, p.forcedKill
	e.Stderr, e.StderrTruncated = p.stderr.String(), p.stderr.truncated
	e.EndedAt = p.r.cfg.Now().UTC()
	p.result = e
}

// tailBuffer hält die letzten limit Bytes.
type tailBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

func (t *tailBuffer) write(b []byte) {
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.limit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.limit:]...)
		t.truncated = true
	}
}

func (t *tailBuffer) String() string { return strings.ToValidUTF8(string(t.buf), "?") }
