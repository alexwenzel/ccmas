package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
)

// ---- Adapter-Vertrag --------------------------------------------------------------------------------------------

// ExternalAdapter ist der Zugang zu einem externen System (Asana, GitHub). Die echten Adapter folgen mit M05/M06; die
// Outbox kennt nur diese Schnittstelle. Der Adapter eines Systems wird über den Teil der Aktionsart vor dem Punkt gewählt
// (asana.comment -> „asana“).
//
// Vertrag:
//
//   - Execute führt die Aktion einmal aus und klassifiziert den Ausgang (domain.OutboxOutcome). Ein Timeout, eine
//     abgebrochene Verbindung oder jede Antwort, die nicht belegt, dass das System nichts getan hat, ist ungewiss
//     (ClassifyTransportError). Nur sicher Nicht-Ausgeführtes ist wiederholbar. Bei Erfolg ist die externe Referenz Pflicht.
//   - Der Adapter schreibt den Marker der Aktion (OutboxAction.Marker) in das externe Objekt (Beschreibung, Kommentar,
//     PR-Text) und prüft vor dem Anlegen, ob ein Objekt mit diesem Marker oder bekannter externer ID schon existiert.
//     Das ist eine zweite Sicherung; die Outbox verlässt sich nicht darauf, sondern gleicht nach ungewissem Ausgang selbst ab.
//   - Lookup sucht das Objekt der Aktion im externen System, über bekannte externe IDs und den Marker. Nur ein
//     maßgebliches Ergebnis darf „nicht vorhanden“ (LookupAbsent) melden: ein direkter Lesezugriff, keine verzögerte Suche,
//     die ein frisch angelegtes Objekt noch nicht kennt. Im Zweifel LookupUnknown.
//   - Fehlertexte (domain.OutboxError) bestehen aus einem Code und einer kurzen Meldung. Adapter geben keine Zugangsdaten,
//     Header, Antwortkörper oder Nutzdaten hinein.
//   - Aufrufe sind nebenläufig sicher (zwei Prozesse arbeiten auf derselben Outbox) und beachten ctx (Zeitgrenze).
type ExternalAdapter interface {
	Execute(ctx context.Context, a domain.OutboxAction) ExecuteResult
	Lookup(ctx context.Context, a domain.OutboxAction) LookupResult
}

// ExecuteResult ist der Ausgang von Execute. Ein leerer oder unbekannter Outcome gilt als ungewiss.
type ExecuteResult struct {
	Outcome     domain.OutboxOutcome
	ExternalRef string // bei Erfolg Pflicht (domain.CheckExternalRef)
	Err         domain.OutboxError
}

// LookupStatus ist der Ausgang einer Suche im externen System.
type LookupStatus string

const (
	// LookupFound: Das Objekt existiert; ExternalRef nennt es.
	LookupFound LookupStatus = "found"
	// LookupAbsent: Die Suche belegt sicher, dass es nicht existiert. Nur dann ist ein erneuter Versuch zulässig.
	LookupAbsent LookupStatus = "absent"
	// LookupUnknown: Die Suche war nicht schlüssig (Fehler, Timeout, unvollständige Daten). Später erneut versuchen.
	LookupUnknown LookupStatus = "unknown"
	// LookupBlocked: Die Suche kann dauerhaft nicht gelingen (zum Beispiel keine Berechtigung): Klärungsbedarf.
	LookupBlocked LookupStatus = "blocked"
)

// LookupResult ist der Ausgang von Lookup. Ein leerer oder unbekannter Status gilt als LookupUnknown.
type LookupResult struct {
	Status      LookupStatus
	ExternalRef string // bei LookupFound Pflicht
	Err         domain.OutboxError
}

// ClassifyTransportError ordnet den Fehler eines Netzaufrufs ein. Ein Timeout, ein Abbruch des Kontexts, EOF, eine
// zurückgesetzte oder abgebrochene Verbindung und jeder unbekannte Fehler beweisen weder Erfolg noch Misserfolg: ungewiss.
// Wiederholbar sind nur Fehler, die belegen, dass nichts gesendet wurde: eine verweigerte Verbindung (dial) und ein
// Namensfehler (DNS „nicht gefunden“). nil ist Erfolg.
func ClassifyTransportError(err error) domain.OutboxOutcome {
	if err == nil {
		return domain.OutboxSuccess
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound && !dns.IsTimeout {
		return domain.OutboxRetryable
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" && errors.Is(err, syscall.ECONNREFUSED) {
		return domain.OutboxRetryable
	}
	return domain.OutboxUncertain
}

// ---- Richtlinie --------------------------------------------------------------------------------------------------

// OutboxPolicy fasst die Grenzen der Outbox zusammen. Nullwerte gelten als Standardwert (DefaultOutboxPolicy).
type OutboxPolicy struct {
	// ClaimTTL ist die Gültigkeit eines Claims. Sie muss länger sein als ExecuteTimeout und LookupTimeout: Ein Claim, der
	// während eines laufenden Aufrufs abläuft, würde diesen Aufruf als ungewiss behandeln und abgleichen lassen.
	ClaimTTL time.Duration
	// ExecuteTimeout und LookupTimeout begrenzen einen Adapteraufruf; PersistTimeout das Speichern des Ergebnisses
	// (mit vom Abbruch gelöstem Kontext, damit ein bekanntes Ergebnis nicht verloren geht).
	ExecuteTimeout, LookupTimeout, PersistTimeout time.Duration
	// MaxAttempts begrenzt die Aufrufe einer Aktion (Beanspruchungen); danach needs_attention.
	MaxAttempts int
	// MaxReconcileAttempts ist die Zahl nicht schlüssiger Abgleiche, die eine Aktion in reconciling verbleibt; der nächste nicht
	// schlüssige Abgleich führt nach needs_attention.
	MaxReconcileAttempts int
	// BatchSize begrenzt die Aufrufe, Abgleiche und die Zahl der Beanspruchungen eines Durchlaufs.
	BatchSize int
	// BackoffInitial und BackoffMax: Der Abstand verdoppelt sich je Versuch und ist gedeckelt (BackoffDelay). Er gilt für
	// Wiederholungen, für den ersten Abgleich nach einem ungewissen Ausgang und für weitere Abgleiche.
	BackoffInitial, BackoffMax time.Duration
	// PollInterval ist die längste Wartezeit der Schleife Run zwischen zwei Durchläufen.
	PollInterval time.Duration
	// Now ist die Uhr (Standard time.Now). Wait wartet d oder endet früher mit dem Fehler des Kontexts (Standard: ein Timer).
	// Beide sind für Tests austauschbar; kein Test schläft wirklich.
	Now  func() time.Time
	Wait func(ctx context.Context, d time.Duration) error
}

// DefaultOutboxPolicy liefert die Standardwerte: Claim 2 min, Aufruf und Suche je 30 s, höchstens 6 Aufrufe, 5 tolerierte
// nicht schlüssige Abgleiche, Backoff 10 s verdoppelt bis höchstens 10 min (10, 20, 40, 80, 160 s), Durchlauf mit höchstens
// 20 Aktionen, Schleife alle 30 s.
func DefaultOutboxPolicy() OutboxPolicy {
	return OutboxPolicy{
		ClaimTTL: 2 * time.Minute, ExecuteTimeout: 30 * time.Second, LookupTimeout: 30 * time.Second, PersistTimeout: 10 * time.Second,
		MaxAttempts: 6, MaxReconcileAttempts: 5, BatchSize: 20,
		BackoffInitial: 10 * time.Second, BackoffMax: 10 * time.Minute, PollInterval: 30 * time.Second,
		Now: time.Now, Wait: defaultOutboxWait,
	}
}

func (p OutboxPolicy) withDefaults() (OutboxPolicy, error) {
	d := DefaultOutboxPolicy()
	for _, v := range []struct {
		name string
		val  *time.Duration
		def  time.Duration
	}{
		{"ClaimTTL", &p.ClaimTTL, d.ClaimTTL}, {"ExecuteTimeout", &p.ExecuteTimeout, d.ExecuteTimeout},
		{"LookupTimeout", &p.LookupTimeout, d.LookupTimeout}, {"PersistTimeout", &p.PersistTimeout, d.PersistTimeout},
		{"BackoffInitial", &p.BackoffInitial, d.BackoffInitial}, {"BackoffMax", &p.BackoffMax, d.BackoffMax},
		{"PollInterval", &p.PollInterval, d.PollInterval},
	} {
		if *v.val < 0 {
			return p, fmt.Errorf("outbox: %s darf nicht negativ sein", v.name)
		}
		if *v.val == 0 {
			*v.val = v.def
		}
	}
	for _, v := range []struct {
		name string
		val  *int
		def  int
	}{{"MaxAttempts", &p.MaxAttempts, d.MaxAttempts}, {"MaxReconcileAttempts", &p.MaxReconcileAttempts, d.MaxReconcileAttempts}, {"BatchSize", &p.BatchSize, d.BatchSize}} {
		if *v.val < 0 {
			return p, fmt.Errorf("outbox: %s darf nicht negativ sein", v.name)
		}
		if *v.val == 0 {
			*v.val = v.def
		}
	}
	if p.Now == nil {
		p.Now = d.Now
	}
	if p.Wait == nil {
		p.Wait = d.Wait
	}
	switch {
	case p.ClaimTTL <= p.ExecuteTimeout || p.ClaimTTL <= p.LookupTimeout:
		return p, fmt.Errorf("outbox: ClaimTTL (%v) muss länger sein als ExecuteTimeout (%v) und LookupTimeout (%v)", p.ClaimTTL, p.ExecuteTimeout, p.LookupTimeout)
	case p.BackoffMax < p.BackoffInitial:
		return p, fmt.Errorf("outbox: BackoffMax (%v) darf nicht unter BackoffInitial (%v) liegen", p.BackoffMax, p.BackoffInitial)
	case p.BatchSize > 1000:
		return p, fmt.Errorf("outbox: BatchSize höchstens 1000")
	}
	return p, nil
}

// BackoffDelay liefert den Abstand vor dem Versuch attempt+1 nach dem attempt-ten Versuch: initial, verdoppelt je Versuch,
// höchstens max (kein Überlauf bei großen Zahlen). attempt < 1 zählt als 1. Ist max <= 0, gilt initial.
func BackoffDelay(attempt int, initial, max time.Duration) time.Duration {
	if max <= 0 || initial >= max {
		if max > 0 {
			return max
		}
		return initial
	}
	d := initial
	for i := 1; i < attempt; i++ {
		if d >= max/2 {
			return max
		}
		d *= 2
	}
	return d
}

func defaultOutboxWait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ---- Dienst -------------------------------------------------------------------------------------------------------

// Outbox führt die externen Aktionen eines Projekts aus (Spezifikation „Externe Aktionen ohne Dubletten“; Ticketvertrag
// Enqueue, Claim, Complete, Reconcile). Die Absicht steht mit stabilem Schlüssel in der Datenbank, bevor etwas ausgeführt
// wird. Ein Durchlauf (RunOnce) tut in dieser Reihenfolge:
//
//  1. Abgelaufene Claims (der Prozess starb während des Aufrufs) gelten als ungewiss: reconciling.
//  2. Fällige Abgleiche: Der Adapter sucht das Objekt (bekannte ID, Marker). Gefunden: succeeded mit der Referenz, ohne zweites
//     Execute. Sicher nicht vorhanden: pending (neuer Versuch zulässig, solange die Höchstzahl nicht erreicht ist). Nicht
//     schlüssig: bleibt reconciling, Zähler +1; nach der Grenze needs_attention.
//  3. Fällige Aktionen: einzeln beanspruchen, ausführen, das Ergebnis einzeln speichern. Erfolg: succeeded. Sicher
//     wiederholbarer Fehler: retry_wait mit Backoff, nach der Höchstzahl needs_attention. Dauerhafter Fehler: needs_attention.
//     Ungewiss (Timeout, abgebrochene Verbindung, Panic, Erfolg ohne verwertbare Referenz): reconciling, nie ein blindes
//     zweites Create.
//
// Jede Aktion eines Batches ist eine eigene Zeile mit eigenem Schlüssel und wird einzeln bestätigt; ein Teilfehler macht
// erfolgreiche Teile nicht rückgängig. Zwei Prozesse auf derselben Datenbank nehmen dieselbe Aktion nie zweimal (Claim in einer
// IMMEDIATE-Transaktion).
//
// needs_attention ist sichtbarer Klärungsbedarf (ccmas status zählt ihn je Zustand); die Klärung ist Resolve.
type Outbox struct {
	store    *persistence.Store
	actor    string
	adapters map[string]ExternalAdapter
	policy   OutboxPolicy
}

// NewOutbox bindet die Outbox an einen Store und die Adapter (Schlüssel: Teil der Aktionsart vor dem Punkt, zum Beispiel
// „asana“). actor erscheint in jedem Audit-Ereignis (zum Beispiel „orchestrator“). Eine ungültige Richtlinie ist ein Fehler.
func NewOutbox(store *persistence.Store, actor string, adapters map[string]ExternalAdapter, policy OutboxPolicy) (*Outbox, error) {
	if err := domain.CheckID(actor); err != nil {
		return nil, fmt.Errorf("outbox: Akteur: %w", err)
	}
	p, err := policy.withDefaults()
	if err != nil {
		return nil, err
	}
	copied := make(map[string]ExternalAdapter, len(adapters))
	for k, v := range adapters {
		copied[k] = v
	}
	return &Outbox{store: store, actor: actor, adapters: copied, policy: p}, nil
}

// Enqueue speichert die Absicht in einer eigenen Transaktion. created ist false, wenn es die Aktion mit gleichem Inhalt
// schon gab. Soll die Absicht atomar mit einer fachlichen Änderung entstehen, ruft der Aufrufer persistence.Tx.EnqueueOutbox
// in derselben Update-Transaktion auf.
func (o *Outbox) Enqueue(ctx context.Context, a domain.OutboxAction) (created bool, err error) {
	_, err = o.store.Update(ctx, o.actor, func(tx *persistence.Tx) error {
		_, created, err = tx.EnqueueOutbox(a)
		return err
	})
	return created, err
}

// EnqueueBatch speichert mehrere Absichten in einer Transaktion (ganz oder gar nicht). Jede Aktion hat ihre eigene Zeile und
// ihren eigenen Schlüssel und wird später einzeln ausgeführt und bestätigt. created zählt die neu angelegten.
func (o *Outbox) EnqueueBatch(ctx context.Context, as []domain.OutboxAction) (created int, err error) {
	_, err = o.store.Update(ctx, o.actor, func(tx *persistence.Tx) error {
		created = 0
		for _, a := range as {
			_, c, err := tx.EnqueueOutbox(a)
			if err != nil {
				return err
			}
			if c {
				created++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return created, nil
}

// NeedsAttention liefert die Aktionen des Projekts mit Klärungsbedarf.
func (o *Outbox) NeedsAttention(ctx context.Context, projectID string) ([]domain.OutboxAction, error) {
	var out []domain.OutboxAction
	err := o.store.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		out, err = r.OutboxActions(projectID, domain.OutboxNeedsAttention)
		return err
	})
	return out, err
}

// Resolve beendet einen Klärungsbedarf ausdrücklich: erneut versuchen, als erledigt bestätigen (mit externer Referenz) oder
// abbrechen. actor ist, wer klärt; Grund und Weg stehen im Audit-Ereignis outbox.resolved. Die Aktion muss needs_attention
// sein (sonst conflict). Die CLI-Anbindung folgt mit CC-51.
func (o *Outbox) Resolve(ctx context.Context, projectID, actionID, actor string, r domain.OutboxResolution) (domain.OutboxAction, error) {
	var out domain.OutboxAction
	_, err := o.store.Update(ctx, actor, func(tx *persistence.Tx) error {
		var err error
		out, err = tx.ResolveOutbox(projectID, actionID, r)
		return err
	})
	if err != nil {
		return domain.OutboxAction{}, err
	}
	return out, nil
}

// ---- Durchlauf -----------------------------------------------------------------------------------------------------

// OutboxStepKind benennt, was ein Schritt eines Durchlaufs tat.
type OutboxStepKind string

const (
	OutboxStepExpire    OutboxStepKind = "expire"    // abgelaufener Claim -> reconciling
	OutboxStepReconcile OutboxStepKind = "reconcile" // Abgleich mit dem externen System
	OutboxStepExecute   OutboxStepKind = "execute"   // Aufruf des Adapters
)

// OutboxStep ist das Ergebnis eines Schritts für eine Aktion. Err ist gesetzt, wenn das Ergebnis nicht gespeichert werden
// konnte (die Aktion bleibt im Zustand From; ein abgelaufener Claim führt später in den Abgleich).
type OutboxStep struct {
	ActionID string
	Kind     OutboxStepKind
	From, To domain.OutboxState
	Err      error
}

// OutboxRun ist der Bericht eines Durchlaufs.
type OutboxRun struct{ Steps []OutboxStep }

func (r *OutboxRun) add(kind OutboxStepKind, a domain.OutboxAction, to domain.OutboxState, err error) {
	from := a.State
	if err != nil {
		to = from
	}
	r.Steps = append(r.Steps, OutboxStep{ActionID: a.ActionID, Kind: kind, From: from, To: to, Err: err})
}

// RunOnce führt einen Durchlauf für das Projekt aus (siehe Typkommentar). Ein Fehler beim Beanspruchen oder Lesen beendet
// den Durchlauf; Fehler beim Speichern eines einzelnen Ergebnisses stehen im Schritt und stoppen die übrigen nicht. Ein
// abgebrochener Kontext beendet den Durchlauf vor dem nächsten Aufruf.
func (o *Outbox) RunOnce(ctx context.Context, projectID string) (OutboxRun, error) {
	var run OutboxRun
	if err := ctx.Err(); err != nil {
		return run, err
	}
	// 1. abgelaufene Claims, 2. fällige Abgleiche
	if err := o.reconcileStage(ctx, projectID, &run); err != nil {
		return run, err
	}
	// 3. fällige Aktionen, jede einzeln beansprucht: Der Claim läuft nie, während eine andere Aktion des Durchlaufs ausgeführt wird.
	for range o.policy.BatchSize {
		if err := ctx.Err(); err != nil {
			return run, err
		}
		var claimed []domain.OutboxAction
		if _, err := o.store.Update(ctx, o.actor, func(tx *persistence.Tx) error {
			var err error
			claimed, err = tx.ClaimOutbox(projectID, o.policy.Now(), o.policy.ClaimTTL, 1)
			return err
		}); err != nil {
			return run, err
		}
		if len(claimed) == 0 {
			break
		}
		o.execute(ctx, claimed[0], &run)
	}
	return run, nil
}

// Run führt Durchläufe aus, bis ctx endet (dann kommt dessen Fehler zurück), und wartet dazwischen bis zur nächsten
// Fälligkeit, höchstens PollInterval. Ein gesperrtes Datenbank (busy) wird übergangen, jeder andere Fehler beendet die Schleife.
func (o *Outbox) Run(ctx context.Context, projectID string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		run, err := o.RunOnce(ctx, projectID)
		if err != nil && persistence.CodeOf(err) != persistence.CodeBusy {
			return err
		}
		progress := err == nil
		for _, st := range run.Steps {
			progress = progress && st.Err == nil
		}
		if err := o.policy.Wait(ctx, o.nextWait(ctx, projectID, progress && len(run.Steps) > 0)); err != nil {
			return err
		}
	}
}

// nextWait berechnet die Wartezeit bis zum nächsten Durchlauf: bis zur frühesten Fälligkeit, höchstens PollInterval; ohne
// Wartezeit nur, wenn der letzte Durchlauf Fortschritt machte und noch Fälliges übrig ist.
func (o *Outbox) nextWait(ctx context.Context, projectID string, progress bool) time.Duration {
	var (
		at     time.Time
		wake   bool
		wakeOK = true
	)
	if err := o.store.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		at, wake, err = r.OutboxWakeup(projectID)
		return err
	}); err != nil {
		wakeOK = false
	}
	d := o.policy.PollInterval
	if !wakeOK || !wake {
		return d
	}
	if at.IsZero() {
		if progress {
			return 0
		}
		return d
	}
	if until := at.Sub(o.policy.Now()); until < d {
		d = max(until, 0)
	}
	return d
}

// ---- Ausführen ------------------------------------------------------------------------------------------------------

func (o *Outbox) adapterFor(a domain.OutboxAction) ExternalAdapter {
	system, _, _ := strings.Cut(a.Kind, ".")
	return o.adapters[system]
}

// persist schreibt ein Ergebnis mit einem vom Abbruch gelösten Kontext: Ein Aufruf, der gelang, soll nicht verloren gehen,
// weil der Durchlauf inzwischen abgebrochen wurde.
func (o *Outbox) persist(ctx context.Context, fn func(tx *persistence.Tx) (domain.OutboxAction, error)) (domain.OutboxAction, error) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.policy.PersistTimeout)
	defer cancel()
	var out domain.OutboxAction
	_, err := o.store.Update(pctx, o.actor, func(tx *persistence.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	return out, err
}

func (o *Outbox) read(ctx context.Context, a domain.OutboxAction) (domain.OutboxAction, error) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.policy.PersistTimeout)
	defer cancel()
	var out domain.OutboxAction
	err := o.store.View(pctx, func(r *persistence.ReadTx) error {
		var err error
		out, err = r.OutboxAction(a.ProjectID, a.ActionID)
		return err
	})
	return out, err
}

// complete schließt die Aktion ab. Lief der Claim während des Aufrufs ab und hat ein anderer Prozess die Aktion schon in den
// Abgleich genommen, wird das Ergebnis aus reconciling übernommen: Ein bekannter Erfolg geht nicht verloren.
func (o *Outbox) complete(ctx context.Context, a domain.OutboxAction, ref string) (domain.OutboxAction, error) {
	out, err := o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(a.ProjectID, a.ActionID, a.State, ref)
	})
	if a.State == domain.OutboxClaimed && persistence.CodeOf(err) == persistence.CodeConflict {
		if cur, rerr := o.read(ctx, a); rerr == nil && cur.State == domain.OutboxReconciling {
			return o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
				return tx.CompleteOutbox(a.ProjectID, a.ActionID, domain.OutboxReconciling, ref)
			})
		}
	}
	return out, err
}

func (o *Outbox) execute(ctx context.Context, a domain.OutboxAction, run *OutboxRun) {
	adapter := o.adapterFor(a)
	if adapter == nil {
		e := domain.OutboxError{Code: "no_adapter", Message: "kein Adapter für " + a.Kind}
		out, err := o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
			return tx.FailOutbox(a.ProjectID, a.ActionID, domain.OutboxPermanent, e, time.Time{})
		})
		run.add(OutboxStepExecute, a, out.State, err)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, o.policy.ExecuteTimeout)
	res := safeExecute(callCtx, adapter, a)
	cancel()

	var (
		out domain.OutboxAction
		err error
	)
	e := res.Err.Normalized()
	switch {
	case res.Outcome == domain.OutboxSuccess && domain.CheckExternalRef(res.ExternalRef) == nil:
		out, err = o.complete(ctx, a, res.ExternalRef)
	case res.Outcome == domain.OutboxRetryable && a.Attempts >= o.policy.MaxAttempts:
		out, err = o.escalate(ctx, a, domain.OutboxClaimed, "retries_exhausted", fmt.Sprintf("%d Versuche, letzter Fehler %s", a.Attempts, res.Err.String()))
	case res.Outcome == domain.OutboxRetryable:
		retryAt := o.policy.Now().Add(BackoffDelay(a.Attempts, o.policy.BackoffInitial, o.policy.BackoffMax))
		out, err = o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
			return tx.FailOutbox(a.ProjectID, a.ActionID, domain.OutboxRetryable, e, retryAt)
		})
	case res.Outcome == domain.OutboxPermanent:
		out, err = o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
			return tx.FailOutbox(a.ProjectID, a.ActionID, domain.OutboxPermanent, e, time.Time{})
		})
	default:
		// Ungewiss, ein leerer oder unbekannter Ausgang und ein Erfolg ohne verwertbare Referenz: Erfolg und Misserfolg sind
		// unbewiesen. Der Abgleich entscheidet; nichts wird blind wiederholt.
		if res.Outcome == domain.OutboxSuccess {
			e = domain.OutboxError{Code: "invalid_external_ref", Message: "Erfolg ohne verwertbare externe Referenz"}.Normalized()
		} else if res.Outcome != domain.OutboxUncertain {
			e = domain.OutboxError{Code: "unclassified", Message: "Ausgang des Adapters nicht klassifiziert"}.Normalized()
		}
		next := o.policy.Now().Add(o.policy.BackoffInitial)
		out, err = o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
			return tx.MarkOutboxUncertain(a.ProjectID, a.ActionID, e, next)
		})
	}
	run.add(OutboxStepExecute, a, out.State, err)
}

func (o *Outbox) escalate(ctx context.Context, a domain.OutboxAction, from domain.OutboxState, code, message string) (domain.OutboxAction, error) {
	return o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
		return tx.EscalateOutbox(a.ProjectID, a.ActionID, from, domain.OutboxError{Code: code, Message: message})
	})
}

// safeExecute ruft den Adapter auf. Ein Panic beweist nichts über das externe System: ungewiss. Der Panic-Text wird nicht
// übernommen (er könnte Daten des Aufrufs enthalten).
func safeExecute(ctx context.Context, adapter ExternalAdapter, a domain.OutboxAction) (res ExecuteResult) {
	defer func() {
		if recover() != nil {
			res = ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "adapter_panic", Message: "Adapter hat einen Panic ausgelöst"}}
		}
	}()
	return adapter.Execute(ctx, a)
}

func safeLookup(ctx context.Context, adapter ExternalAdapter, a domain.OutboxAction) (res LookupResult) {
	defer func() {
		if recover() != nil {
			res = LookupResult{Status: LookupUnknown, Err: domain.OutboxError{Code: "adapter_panic", Message: "Adapter hat einen Panic ausgelöst"}}
		}
	}()
	return adapter.Lookup(ctx, a)
}

// ---- Abgleich ----------------------------------------------------------------------------------------------------------

// Reconcile gleicht die fälligen Aktionen im Abgleich (und zuvor abgelaufene Claims) mit dem externen System ab, ohne
// neue Aktionen auszuführen. RunOnce tut dasselbe als Teil seines Durchlaufs.
func (o *Outbox) Reconcile(ctx context.Context, projectID string) (OutboxRun, error) {
	var run OutboxRun
	err := o.reconcileStage(ctx, projectID, &run)
	return run, err
}

// reconcileStage nimmt abgelaufene Claims in den Abgleich und gleicht die fälligen Aktionen ab.
func (o *Outbox) reconcileStage(ctx context.Context, projectID string, run *OutboxRun) error {
	var expired []domain.OutboxAction
	if _, err := o.store.Update(ctx, o.actor, func(tx *persistence.Tx) error {
		var err error
		expired, err = tx.ExpireOutboxClaims(projectID, o.policy.Now())
		return err
	}); err != nil {
		return err
	}
	for _, a := range expired {
		a.State = domain.OutboxClaimed
		run.add(OutboxStepExpire, a, domain.OutboxReconciling, nil)
	}
	var due []domain.OutboxAction
	if err := o.store.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		due, err = r.DueReconcileOutbox(projectID, o.policy.Now(), o.policy.BatchSize)
		return err
	}); err != nil {
		return err
	}
	for _, a := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		o.reconcile(ctx, a, run)
	}
	return nil
}

func (o *Outbox) reconcile(ctx context.Context, a domain.OutboxAction, run *OutboxRun) {
	adapter := o.adapterFor(a)
	if adapter == nil {
		out, err := o.escalate(ctx, a, domain.OutboxReconciling, "no_adapter", "kein Adapter für "+a.Kind)
		run.add(OutboxStepReconcile, a, out.State, err)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, o.policy.LookupTimeout)
	res := safeLookup(callCtx, adapter, a)
	cancel()

	var (
		out domain.OutboxAction
		err error
	)
	e := res.Err.Normalized()
	status := res.Status
	if status == LookupFound && domain.CheckExternalRef(res.ExternalRef) != nil {
		status, e = LookupUnknown, domain.OutboxError{Code: "lookup_invalid_ref", Message: "Objekt gefunden, aber ohne verwertbare Referenz"}.Normalized()
	}
	switch status {
	case LookupFound:
		out, err = o.complete(ctx, a, res.ExternalRef)
	case LookupAbsent:
		if a.Attempts >= o.policy.MaxAttempts {
			out, err = o.escalate(ctx, a, domain.OutboxReconciling, "retries_exhausted", fmt.Sprintf("Objekt nicht vorhanden, aber %d Versuche aufgebraucht", a.Attempts))
		} else {
			out, err = o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
				return tx.RequeueOutbox(a.ProjectID, a.ActionID)
			})
		}
	case LookupBlocked:
		out, err = o.escalate(ctx, a, domain.OutboxReconciling, "lookup_blocked", "Abgleich dauerhaft unmöglich: "+res.Err.String())
	default: // LookupUnknown, leer oder unbekannt
		if status != LookupUnknown {
			e = domain.OutboxError{Code: "unclassified", Message: "Ausgang der Suche nicht klassifiziert"}.Normalized()
		}
		if a.ReconcileAttempts >= o.policy.MaxReconcileAttempts {
			out, err = o.escalate(ctx, a, domain.OutboxReconciling, "reconcile_exhausted",
				fmt.Sprintf("%d Abgleiche ohne Ergebnis, letzter Fehler %s", a.ReconcileAttempts, e.String()))
		} else {
			next := o.policy.Now().Add(BackoffDelay(a.ReconcileAttempts+1, o.policy.BackoffInitial, o.policy.BackoffMax))
			out, err = o.persist(ctx, func(tx *persistence.Tx) (domain.OutboxAction, error) {
				return tx.RetryOutboxReconcile(a.ProjectID, a.ActionID, e, next)
			})
		}
	}
	run.add(OutboxStepReconcile, a, out.State, err)
}
