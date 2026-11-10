-- CCMAS 003: Nachrichten, Zustellung und Outbox (CC-45, W45.1 und W45.2).
--
-- Ergänzt Schema 001, ohne eine Tabelle umzubauen (ein Umbau bräuchte defer_foreign_keys). Die einzige Spaltenänderung ist
-- ein ADD COLUMN (outbox.reconcile_attempts). Alle Prüfungen sind Trigger; sie wirken auch gegen direktes SQL. Die
-- Prüfungen entsprechen genau den Go-Regeln:
--   * messages.type            -> domain.MessageKinds (Test TestMessageTypeIsCheckedByTheDatabase)
--   * outbox.kind              -> domain.CheckOutboxKind: <system>.<aktion>, eine Formatregel statt einer Liste,
--                                 weil die Aktionen je Adapter hinzukommen (Test TestOutboxKindIsCheckedByTheDatabase)
--   * message_deliveries.state -> domain.DeliveryState.CanTransition (Test TestDeliveryTriggers)
--   * outbox.state             -> domain.OutboxState.CanTransition, Startwerte, Zähler und Form je Zustand
--                                 (Tests TestOutboxInsertTriggers, TestOutboxTransitionTriggers)
--   * messages, message_deliveries, outbox sind append-only: kein DELETE (wie events in 001)
--                                 (Tests TestMessageJournalIsAppendOnly, TestOutboxIsAppendOnly)
-- Nicht in der Datenbank geprüft, sondern in Go: das Format von Absender und Empfänger (domain.CheckAddress), dass die
-- Ausführung eines Versuchs zum Auftrag der Nachricht gehört (nur das Projekt bindet der Fremdschlüssel) und die
-- Grenzen der Nutzdaten.

-- ---- messages: Art und Unveränderlichkeit ------------------------------------------------------

CREATE TRIGGER messages_type_insert BEFORE INSERT ON messages
WHEN NEW.type NOT IN ('task_start','task_progress','task_result','review_request','dependency_request','supervisor_decision','agent_message','question','answer','delegation_request')
BEGIN
    SELECT RAISE(ABORT, 'messages.type ist keine bekannte Nachrichtenart');
END;

-- Eine Nachricht ist Journal: einmal geschrieben, nie geändert. Nur der Zustellzustand wandert (message_deliveries).
CREATE TRIGGER messages_no_update BEFORE UPDATE ON messages
BEGIN SELECT RAISE(ABORT, 'messages sind unveraenderlich'); END;

-- ---- message_deliveries: Übergangstabelle ---------------------------------------------------------

-- Neue Zustellungen beginnen unzugestellt.
CREATE TRIGGER message_deliveries_initial BEFORE INSERT ON message_deliveries
WHEN NEW.state <> 'pending' OR NEW.attempts <> 0 OR NEW.delivered_at IS NOT NULL OR NEW.acknowledged_at IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'eine neue Zustellung beginnt pending, ohne Versuch und ohne Zeit');
END;

-- Erlaubt sind genau die Übergänge von domain.DeliveryState:
--   pending   -> delivered     (Versuch +1, Zustellzeit gesetzt)
--   delivered -> delivered     (Wiederholung: Versuch +1, Zustellzeit gesetzt)
--   delivered -> acknowledged  (Versuche und Zustellzeit unverändert, Bestätigungszeit gesetzt)
-- Nachricht und Empfänger ändern sich nie; aus acknowledged gibt es keinen Weg.
CREATE TRIGGER message_deliveries_transition BEFORE UPDATE ON message_deliveries
WHEN NEW.message_id IS NOT OLD.message_id OR NEW.recipient IS NOT OLD.recipient OR NOT (
       (OLD.state = 'pending' AND NEW.state = 'delivered'
            AND NEW.attempts = OLD.attempts + 1 AND NEW.delivered_at IS NOT NULL AND NEW.acknowledged_at IS NULL)
    OR (OLD.state = 'delivered' AND NEW.state = 'delivered'
            AND NEW.attempts = OLD.attempts + 1 AND NEW.delivered_at IS NOT NULL AND NEW.acknowledged_at IS NULL)
    OR (OLD.state = 'delivered' AND NEW.state = 'acknowledged'
            AND NEW.attempts = OLD.attempts AND NEW.delivered_at IS OLD.delivered_at AND NEW.acknowledged_at IS NOT NULL)
)
BEGIN
    SELECT RAISE(ABORT, 'Zustellzustand: Uebergang nicht erlaubt');
END;

-- Ausstehende Zustellungen je Empfänger (pending oder delivered, nicht bestätigt) in fester Reihenfolge lesen.
CREATE INDEX message_deliveries_open ON message_deliveries (recipient, message_id) WHERE state <> 'acknowledged';

-- ---- outbox: Form der Aktionsart --------------------------------------------------------------------

-- <system>.<aktion>: Kleinbuchstaben, Ziffern und _, genau ein Punkt, beide Teile beginnen mit einem Buchstaben.
CREATE TRIGGER outbox_kind_insert BEFORE INSERT ON outbox
WHEN NOT (NEW.kind GLOB '[a-z]*' AND NEW.kind NOT GLOB '*[^a-z0-9_.]*' AND NEW.kind GLOB '*.[a-z]*'
          AND length(NEW.kind) - length(replace(NEW.kind, '.', '')) = 1)
BEGIN
    SELECT RAISE(ABORT, 'outbox.kind hat nicht die Form system.aktion');
END;

CREATE TRIGGER outbox_kind_update BEFORE UPDATE OF kind ON outbox
WHEN NOT (NEW.kind GLOB '[a-z]*' AND NEW.kind NOT GLOB '*[^a-z0-9_.]*' AND NEW.kind GLOB '*.[a-z]*'
          AND length(NEW.kind) - length(replace(NEW.kind, '.', '')) = 1)
BEGIN
    SELECT RAISE(ABORT, 'outbox.kind hat nicht die Form system.aktion');
END;

-- ---- outbox: Abgleichzähler, Startwerte, Übergangstabelle ------------------------------------------------------------

-- Zählt die nicht schlüssigen Abgleiche der laufenden Ungewissheit (outbox.attempts zählt die Aufrufe). Ein Zähler ohne
-- eigene Spalte ginge bei einem Absturz verloren; die Grenze für needs_attention wäre dann nicht durchsetzbar.
ALTER TABLE outbox ADD COLUMN reconcile_attempts INTEGER NOT NULL DEFAULT 0 CHECK (reconcile_attempts >= 0);

-- Neue Aktionen beginnen pending, ohne Versuche, Claim, Referenz und Fehler.
CREATE TRIGGER outbox_initial BEFORE INSERT ON outbox
WHEN NEW.state <> 'pending' OR NEW.attempts <> 0 OR NEW.reconcile_attempts <> 0 OR NEW.claim_expires_at IS NOT NULL
  OR NEW.external_ref IS NOT NULL OR NEW.last_error <> ''
BEGIN
    SELECT RAISE(ABORT, 'eine neue Outbox-Aktion beginnt pending, ohne Versuch, Claim, Referenz und Fehler');
END;

-- operation_key ist global eindeutig und beginnt deshalb mit <projekt>/<art>/ und mindestens einem weiteren Zeichen
-- (domain.OperationKey, domain.ParseOperationKey).
CREATE TRIGGER outbox_key_insert BEFORE INSERT ON outbox
WHEN length(NEW.operation_key) <= length(NEW.project_id) + length(NEW.kind) + 2
  OR substr(NEW.operation_key, 1, length(NEW.project_id) + length(NEW.kind) + 2) <> NEW.project_id || '/' || NEW.kind || '/'
BEGIN
    SELECT RAISE(ABORT, 'outbox.operation_key beginnt nicht mit projekt/art/');
END;

-- Was eine Aktion ausmacht, ändert sich nie: nur Zustand, Zähler, Zeitpunkte, Fehler, Referenz und Änderungszeit.
CREATE TRIGGER outbox_immutable BEFORE UPDATE ON outbox
WHEN NEW.action_id IS NOT OLD.action_id OR NEW.project_id IS NOT OLD.project_id OR NEW.operation_key IS NOT OLD.operation_key
  OR NEW.kind IS NOT OLD.kind OR NEW.target IS NOT OLD.target OR NEW.payload_json IS NOT OLD.payload_json
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'Outbox-Aktion: Identitaet, Art, Ziel, Nutzdaten und Erzeugungszeit sind unveraenderlich');
END;

-- Erlaubt sind genau die Übergänge von domain.OutboxState (reconciling -> reconciling ist ein weiterer Abgleich).
CREATE TRIGGER outbox_transition BEFORE UPDATE ON outbox
WHEN NOT (
       (OLD.state = 'pending'         AND NEW.state IN ('claimed','cancelled'))
    OR (OLD.state = 'claimed'         AND NEW.state IN ('succeeded','retry_wait','reconciling','needs_attention'))
    OR (OLD.state = 'retry_wait'      AND NEW.state IN ('claimed','cancelled','needs_attention'))
    OR (OLD.state = 'reconciling'     AND NEW.state IN ('reconciling','succeeded','pending','needs_attention'))
    OR (OLD.state = 'needs_attention' AND NEW.state IN ('pending','succeeded','cancelled'))
)
BEGIN
    SELECT RAISE(ABORT, 'Outbox-Zustand: Uebergang nicht erlaubt');
END;

-- Zähler: Ein Aufruf (-> claimed) zählt als Versuch. Ein weiterer Abgleich (reconciling -> reconciling) zählt als
-- Abgleichversuch. Wer den Abgleich mit „nicht vorhanden“ beendet (reconciling -> pending) oder den Klärungsbedarf
-- aufhebt (needs_attention -> pending), beginnt mit zurückgesetzten Zählern (die Klärung auch ohne Versuche).
CREATE TRIGGER outbox_counters BEFORE UPDATE ON outbox
WHEN NEW.attempts <> CASE
        WHEN NEW.state = 'claimed' THEN OLD.attempts + 1
        WHEN OLD.state = 'needs_attention' AND NEW.state = 'pending' THEN 0
        ELSE OLD.attempts END
  OR NEW.reconcile_attempts <> CASE
        WHEN OLD.state = 'reconciling' AND NEW.state = 'reconciling' THEN OLD.reconcile_attempts + 1
        WHEN NEW.state = 'pending' AND OLD.state IN ('reconciling','needs_attention') THEN 0
        ELSE OLD.reconcile_attempts END
BEGIN
    SELECT RAISE(ABORT, 'Outbox-Zaehler: Versuche und Abgleichversuche folgen dem Uebergang');
END;

-- Form je Zustand (domain.OutboxAction.Check): ein Claim-Ablauf nur bei claimed; ein Zeitpunkt bei retry_wait und
-- (optional) pending und reconciling; eine Referenz genau bei succeeded.
CREATE TRIGGER outbox_shape BEFORE UPDATE ON outbox
WHEN NOT (
       (NEW.state = 'claimed')    = (NEW.claim_expires_at IS NOT NULL)
   AND (NEW.state = 'succeeded')  = (NEW.external_ref IS NOT NULL AND NEW.external_ref <> '')
   AND (NEW.external_ref IS NULL OR NEW.state = 'succeeded')
   AND (NEW.state <> 'retry_wait' OR NEW.next_attempt_at IS NOT NULL)
   AND (NEW.state NOT IN ('claimed','succeeded','needs_attention','cancelled') OR NEW.next_attempt_at IS NULL)
)
BEGIN
    SELECT RAISE(ABORT, 'Outbox-Aktion: Claim, Zeitpunkt oder Referenz passen nicht zum Zustand');
END;

-- Fällige Aktionen (pending, retry_wait, reconciling) und abgelaufene Claims liest outbox_by_state aus Migration 001
-- (project_id, state, next_attempt_at); ein weiterer Index ist nicht nötig.

-- ---- Journal: kein Löschen --------------------------------------------------------------------------------

-- Nachrichten, Zustellungen und externe Aktionen sind Journal wie events: Sie werden angehängt und fortgeschrieben, nie
-- gelöscht (auch ein erledigter oder abgebrochener Eintrag bleibt als Beleg stehen). Kein Codepfad löscht aus diesen Tabellen.
-- Wechselwirkung mit 001: Dort gibt es kein ON DELETE CASCADE; die Fremdschluessel (messages -> projects, work_orders,
-- execution_attempts; message_deliveries -> messages; outbox -> projects) verhindern ohnehin das Löschen eines Elternteils, das
-- noch Kinder hat. Die Trigger schützen zusätzlich die Zeilen selbst, auch mit ausgeschaltetem Fremdschluessel-Schalter.
CREATE TRIGGER messages_no_delete BEFORE DELETE ON messages
BEGIN SELECT RAISE(ABORT, 'messages sind Journal und werden nicht geloescht'); END;

CREATE TRIGGER message_deliveries_no_delete BEFORE DELETE ON message_deliveries
BEGIN SELECT RAISE(ABORT, 'message_deliveries sind Journal und werden nicht geloescht'); END;

CREATE TRIGGER outbox_no_delete BEFORE DELETE ON outbox
BEGIN SELECT RAISE(ABORT, 'outbox ist Journal und wird nicht geloescht'); END;

-- Grenze: INSERT OR REPLACE löscht eine kollidierende Zeile, ohne dass ein BEFORE-DELETE-Trigger feuert (SQLite feuert
-- Löschtrigger bei einer Ersetzung nur mit PRAGMA recursive_triggers). Das gilt ebenso für events in 001. Kein Codepfad nutzt
-- REPLACE; die Trigger schützen gegen DELETE, nicht gegen diesen Umweg.
