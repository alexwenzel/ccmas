-- CCMAS 003: Nachrichten und Zustellung (CC-45, W45.1).
--
-- Ergänzt Schema 001, ohne eine Tabelle umzubauen (ein Umbau bräuchte defer_foreign_keys). Alle Prüfungen sind
-- Trigger; sie wirken auch gegen direktes SQL. Die Prüfungen entsprechen genau den Go-Regeln:
--   * messages.type            -> domain.MessageKinds (Test TestMessageTypeIsCheckedByTheDatabase)
--   * outbox.kind              -> domain.CheckOutboxKind: <system>.<aktion>, eine Formatregel statt einer Liste,
--                                 weil die Aktionen je Adapter hinzukommen (Test TestOutboxKindIsCheckedByTheDatabase)
--   * message_deliveries.state -> domain.DeliveryState.CanTransition (Test TestDeliveryTriggers)
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
