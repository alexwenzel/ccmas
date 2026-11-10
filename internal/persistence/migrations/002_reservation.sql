-- CCMAS 002: Reservierung und sichere Zustandsübergänge (CC-42).
--
-- Was Schema 001 für die Reservierung offen ließ:
--   * leases.worktree_path war Pflicht. Eine Reservierung entsteht aber vor dem Worktree (der wird erst für die
--     Ausführung vorbereitet); die Spalte wird optional (NULL: noch keiner). Ein genannter Worktree bleibt absolut und
--     gehört höchstens einer aktiven Lease.
--   * Dieselbe Entscheidung eines Plans darf nie zweimal reservieren: leases.batch_id und decision_id verknüpfen eine
--     Lease mit der Entscheidung, aus der sie stammt, und sind zusammen eindeutig (auch über beendete Leases hinweg).
--   * Der Versuch einer Lease gehört zum Auftrag der Lease. 001 band ihn nur an das Projekt. Go prüft das; die Trigger
--     unten sichern es ein zweites Mal ab.
--
-- Umbau von leases (neue Tabelle, Zeilen kopieren, alte löschen, umbenennen): SQLite kann eine NOT-NULL-Spalte nicht
-- ändern. Das Löschen einer referenzierten Tabelle gilt bei foreign_keys=ON als Verstoß; deshalb wird die Prüfung bis zum
-- Festschreiben aufgeschoben (PRAGMA defer_foreign_keys). Auf leases verweist keine andere Tabelle.

PRAGMA defer_foreign_keys = ON;

CREATE TABLE leases_new (
    lease_id      TEXT PRIMARY KEY CHECK (length(lease_id) BETWEEN 1 AND 128),
    project_id    TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id TEXT NOT NULL,
    execution_id  TEXT,
    attempt       INTEGER,
    agent_id      TEXT NOT NULL,
    generation    INTEGER NOT NULL CHECK (generation >= 1),
    state         TEXT NOT NULL CHECK (state IN ('active','released','expired','revoked')),
    -- NULL: noch kein Worktree vorbereitet.
    worktree_path TEXT CHECK (worktree_path IS NULL OR substr(worktree_path, 1, 1) = '/'),
    acquired_at   TEXT NOT NULL CHECK (acquired_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    expires_at    TEXT NOT NULL CHECK (expires_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    released_at   TEXT CHECK (released_at IS NULL OR released_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    -- Herkunft: die Entscheidung eines angenommenen Batches, aus der die Lease stammt (beide oder keins).
    batch_id      TEXT,
    decision_id   TEXT,
    UNIQUE (work_order_id, generation),
    UNIQUE (project_id, batch_id, decision_id),
    CHECK ((execution_id IS NULL) = (attempt IS NULL)),
    CHECK ((state = 'active') = (released_at IS NULL)),
    CHECK ((batch_id IS NULL) = (decision_id IS NULL)),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, execution_id, attempt) REFERENCES execution_attempts (project_id, execution_id, attempt),
    FOREIGN KEY (project_id, agent_id) REFERENCES agents (project_id, agent_id),
    FOREIGN KEY (project_id, batch_id, decision_id) REFERENCES decisions (project_id, batch_id, decision_id)
) STRICT;

INSERT INTO leases_new (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state, worktree_path,
                        acquired_at, expires_at, released_at)
SELECT lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state, worktree_path,
       acquired_at, expires_at, released_at
FROM leases;

DROP TABLE leases;
ALTER TABLE leases_new RENAME TO leases;

-- Höchstens eine aktive Lease je Auftrag, je Agent und je Worktree (NULL kollidiert nicht).
CREATE UNIQUE INDEX leases_one_active_per_order ON leases (work_order_id) WHERE state = 'active';
CREATE UNIQUE INDEX leases_one_active_per_agent ON leases (project_id, agent_id) WHERE state = 'active';
CREATE UNIQUE INDEX leases_one_active_per_worktree ON leases (worktree_path) WHERE state = 'active';
CREATE INDEX leases_by_state ON leases (project_id, state);
CREATE INDEX leases_by_attempt ON leases (project_id, execution_id, attempt);

-- Eine Lease wird nie gelöscht: Generationen je Auftrag bleiben lückenlos, und eine beendete Lease ist Historie.
CREATE TRIGGER leases_no_delete BEFORE DELETE ON leases
BEGIN SELECT RAISE(ABORT, 'leases werden nicht geloescht'); END;

-- Die Identität einer Lease ändert sich nie. Wer anderes will, reserviert neu (neue Lease, höhere Generation).
CREATE TRIGGER leases_identity_fixed BEFORE UPDATE OF lease_id, project_id, work_order_id, agent_id, generation, acquired_at, batch_id, decision_id ON leases
BEGIN SELECT RAISE(ABORT, 'Identitaet einer Lease ist unveraenderlich'); END;

-- Ein Endzustand ist endgültig: nichts an einer beendeten Lease ändert sich, keine Wiederbelebung.
CREATE TRIGGER leases_terminal_fixed BEFORE UPDATE ON leases
WHEN OLD.state <> 'active'
BEGIN SELECT RAISE(ABORT, 'beendete Lease ist unveraenderlich'); END;

-- Die Bindung an einen Versuch geschieht einmal.
CREATE TRIGGER leases_attempt_bound_once BEFORE UPDATE OF execution_id, attempt ON leases
WHEN OLD.execution_id IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'Lease ist schon an einen Versuch gebunden'); END;

-- Der Versuch gehört zum Auftrag der Lease, nicht nur zum Projekt. (Eine Ausführung eines anderen Projekts oder eine
-- unbekannte trifft den Fremdschlüssel, nicht diesen Trigger.)
CREATE TRIGGER leases_attempt_same_order_insert BEFORE INSERT ON leases
WHEN NEW.execution_id IS NOT NULL
 AND EXISTS (SELECT 1 FROM executions WHERE project_id = NEW.project_id AND execution_id = NEW.execution_id
             AND work_order_id IS NOT NEW.work_order_id)
BEGIN SELECT RAISE(ABORT, 'Versuch gehoert nicht zum Auftrag der Lease'); END;

CREATE TRIGGER leases_attempt_same_order_update BEFORE UPDATE OF execution_id ON leases
WHEN NEW.execution_id IS NOT NULL
 AND EXISTS (SELECT 1 FROM executions WHERE project_id = NEW.project_id AND execution_id = NEW.execution_id
             AND work_order_id IS NOT NEW.work_order_id)
BEGIN SELECT RAISE(ABORT, 'Versuch gehoert nicht zum Auftrag der Lease'); END;

-- Nur eine angenommene Entscheidung eines angenommenen Batches für genau diesen Auftrag reserviert.
CREATE TRIGGER leases_decision_accepted BEFORE INSERT ON leases
WHEN NEW.batch_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM plans p
    JOIN decisions d ON d.project_id = p.project_id AND d.batch_id = p.batch_id
    WHERE p.project_id = NEW.project_id AND p.batch_id = NEW.batch_id AND d.decision_id = NEW.decision_id
      AND p.verdict = 'accepted' AND d.validation = 'accepted'
      AND d.target_kind = 'work_order' AND d.target_id = NEW.work_order_id)
BEGIN SELECT RAISE(ABORT, 'Reservierung nur aus einer angenommenen Entscheidung fuer diesen Auftrag'); END;
