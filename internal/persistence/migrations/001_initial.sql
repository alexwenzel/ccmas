-- CCMAS 001: führender interner Zustand (SQLite). Nur der Go-Dienst schreibt.
--
-- Konventionen
--   * Zeitstempel: TEXT, RFC 3339 in UTC mit genau neun Nachkommastellen (…:SS.nnnnnnnnnZ), damit die
--     Textordnung der Zeitordnung entspricht.
--   * Aufzählungen: CHECK-Listen, die genau den Go-Listen entsprechen (Test TestEnumChecksMatchDomain).
--   * Bezüge: Fremdschlüssel über (project_id, …); ein Bezug kann kein anderes Projekt treffen.
--   * Keine Spalten für Zugangsdaten (Test TestNoSecretColumns).
--   * Dokumente (…_json) sind vor dem Schreiben gegen ihren Schemavertrag geprüft; json_valid ist die
--     letzte Absicherung.

-- ---- Projekt und Konfiguration -----------------------------------------------------------------

CREATE TABLE projects (
    project_id     TEXT PRIMARY KEY CHECK (length(project_id) BETWEEN 1 AND 128),
    config_json    TEXT NOT NULL CHECK (json_valid(config_json) AND json_extract(config_json, '$.project_id') = project_id),
    -- Zustandsrevision: +1 je festgeschriebener Änderungstransaktion (siehe doc.go), ab 0.
    state_revision INTEGER NOT NULL DEFAULT 0 CHECK (state_revision >= 0),
    created_at     TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at     TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;

CREATE TRIGGER projects_revision_step BEFORE UPDATE OF state_revision ON projects
WHEN NEW.state_revision <> OLD.state_revision AND NEW.state_revision <> OLD.state_revision + 1
BEGIN
    SELECT RAISE(ABORT, 'state_revision steigt nur um 1');
END;

CREATE TRIGGER projects_id_fixed BEFORE UPDATE OF project_id ON projects
BEGIN
    SELECT RAISE(ABORT, 'project_id ist unveraenderlich');
END;

-- Aufgelöstes AgentProfile je Version; unveränderlich, weil Ausführungen darauf verweisen.
CREATE TABLE profile_versions (
    project_id   TEXT NOT NULL REFERENCES projects (project_id),
    profile_id   TEXT NOT NULL CHECK (length(profile_id) BETWEEN 1 AND 128),
    version      INTEGER NOT NULL CHECK (version >= 1),
    role         TEXT NOT NULL CHECK (role IN ('supervisor','project_setup','concept','concept_reviewer','implementer','reviewer','documentation','completion','question_handler')),
    profile_json TEXT NOT NULL CHECK (json_valid(profile_json)
                     AND json_extract(profile_json, '$.profile_id') = profile_id
                     AND json_extract(profile_json, '$.version') = version),
    sha256       TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    recorded_at  TEXT NOT NULL CHECK (recorded_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (project_id, profile_id, version)
) STRICT;

CREATE TRIGGER profile_versions_no_update BEFORE UPDATE ON profile_versions
BEGIN SELECT RAISE(ABORT, 'profile_versions sind unveraenderlich'); END;
CREATE TRIGGER profile_versions_no_delete BEFORE DELETE ON profile_versions
BEGIN SELECT RAISE(ABORT, 'profile_versions sind unveraenderlich'); END;

-- Skill-Snapshot (snapshot.json) mit den Eckdaten je Skill; unveränderlich. snapshot_json ist der
-- vollständige, validierte Vertragsinhalt (domain.SkillSnapshot), die Tabellen daneben sind Abfrageindex.
CREATE TABLE skill_snapshots (
    project_id      TEXT NOT NULL REFERENCES projects (project_id),
    snapshot_id     TEXT NOT NULL CHECK (length(snapshot_id) BETWEEN 1 AND 128),
    role            TEXT NOT NULL CHECK (role IN ('supervisor','project_setup','concept','concept_reviewer','implementer','reviewer','documentation','completion','question_handler')),
    manifest_sha256 TEXT NOT NULL CHECK (length(manifest_sha256) = 64 AND manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
    path            TEXT NOT NULL CHECK (substr(path, 1, 1) = '/' AND length(path) <= 4096),
    snapshot_json   TEXT NOT NULL CHECK (json_valid(snapshot_json) AND json_extract(snapshot_json, '$.snapshot_id') = snapshot_id),
    created_at      TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    recorded_at     TEXT NOT NULL CHECK (recorded_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (project_id, snapshot_id)
) STRICT;

CREATE TABLE skill_snapshot_skills (
    project_id     TEXT NOT NULL,
    snapshot_id    TEXT NOT NULL,
    skill_id       TEXT NOT NULL CHECK (length(skill_id) BETWEEN 1 AND 128),
    version        INTEGER NOT NULL CHECK (version >= 1),
    tree_sha256    TEXT NOT NULL CHECK (length(tree_sha256) = 64 AND tree_sha256 NOT GLOB '*[^0-9a-f]*'),
    package        TEXT NOT NULL,
    package_sha256 TEXT NOT NULL CHECK (length(package_sha256) = 64 AND package_sha256 NOT GLOB '*[^0-9a-f]*'),
    PRIMARY KEY (project_id, snapshot_id, skill_id),
    FOREIGN KEY (project_id, snapshot_id) REFERENCES skill_snapshots (project_id, snapshot_id)
) STRICT;

CREATE TRIGGER skill_snapshots_no_update BEFORE UPDATE ON skill_snapshots
BEGIN SELECT RAISE(ABORT, 'skill_snapshots sind unveraenderlich'); END;
CREATE TRIGGER skill_snapshots_no_delete BEFORE DELETE ON skill_snapshots
BEGIN SELECT RAISE(ABORT, 'skill_snapshots sind unveraenderlich'); END;
CREATE TRIGGER skill_snapshot_skills_no_update BEFORE UPDATE ON skill_snapshot_skills
BEGIN SELECT RAISE(ABORT, 'skill_snapshots sind unveraenderlich'); END;
CREATE TRIGGER skill_snapshot_skills_no_delete BEFORE DELETE ON skill_snapshot_skills
BEGIN SELECT RAISE(ABORT, 'skill_snapshots sind unveraenderlich'); END;

-- Agenten: Rolle und Verfügbarkeit (eigene Ebene). revision zählt die Änderungen (Compare-and-Set).
CREATE TABLE agents (
    project_id   TEXT NOT NULL REFERENCES projects (project_id),
    agent_id     TEXT NOT NULL CHECK (length(agent_id) BETWEEN 1 AND 128),
    role         TEXT NOT NULL CHECK (role IN ('supervisor','project_setup','concept','concept_reviewer','implementer','reviewer','documentation','completion','question_handler')),
    availability TEXT NOT NULL CHECK (availability IN ('available','reserved','active','unavailable')),
    revision     INTEGER NOT NULL CHECK (revision >= 1),
    created_at   TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at   TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (project_id, agent_id)
) STRICT;

-- ---- Ziele, Aufträge, Aufgaben -----------------------------------------------------------------

CREATE TABLE goals (
    project_id             TEXT NOT NULL REFERENCES projects (project_id),
    goal_id                TEXT NOT NULL CHECK (length(goal_id) BETWEEN 1 AND 128),
    kind                   TEXT NOT NULL CHECK (kind IN ('ticket','feature','milestone')),
    title                  TEXT NOT NULL DEFAULT '',
    acceptance_criteria_json TEXT NOT NULL CHECK (json_valid(acceptance_criteria_json) AND json_type(acceptance_criteria_json) = 'array'),
    scope_json             TEXT NOT NULL CHECK (json_valid(scope_json) AND json_type(scope_json) = 'array'),
    out_of_scope_json      TEXT NOT NULL CHECK (json_valid(out_of_scope_json) AND json_type(out_of_scope_json) = 'array'),
    -- Abschlussnachweise (supervisor.Evidence); NULL: noch keine.
    evidence_json          TEXT CHECK (evidence_json IS NULL OR (json_valid(evidence_json) AND json_type(evidence_json) = 'object')),
    integration_mode       TEXT NOT NULL CHECK (integration_mode IN ('pr','auto')),
    parent_goal_id         TEXT,
    created_at             TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at             TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (project_id, goal_id),
    CHECK (parent_goal_id IS NULL OR parent_goal_id <> goal_id),
    FOREIGN KEY (project_id, parent_goal_id) REFERENCES goals (project_id, goal_id)
) STRICT;

-- Arbeitsauftrag: die reservierbare, ausführbare Einheit. state ist der fachliche Auftragszustand.
CREATE TABLE work_orders (
    work_order_id            TEXT PRIMARY KEY CHECK (length(work_order_id) BETWEEN 1 AND 128),
    project_id               TEXT NOT NULL REFERENCES projects (project_id),
    goal_id                  TEXT NOT NULL,
    target_kind              TEXT NOT NULL CHECK (target_kind IN ('ticket','feature','milestone')),
    target_ref               TEXT NOT NULL CHECK (length(target_ref) BETWEEN 1 AND 128),
    user_order               TEXT NOT NULL,
    goal                     TEXT NOT NULL,
    acceptance_criteria_json TEXT NOT NULL CHECK (json_valid(acceptance_criteria_json) AND json_type(acceptance_criteria_json) = 'array'),
    allowed_scope_json       TEXT NOT NULL CHECK (json_valid(allowed_scope_json) AND json_type(allowed_scope_json) = 'array'),
    mode                     TEXT NOT NULL CHECK (mode IN ('pr','auto')),
    plan_version             INTEGER NOT NULL CHECK (plan_version >= 0),
    state                    TEXT NOT NULL CHECK (state IN ('waiting','in_progress','validation','review','waiting_integration','completed','blocked','cancelled')),
    order_kind               TEXT NOT NULL CHECK (order_kind IN ('concept','implementation','bug','organisation')),
    title                    TEXT NOT NULL DEFAULT '',
    head_commit              TEXT NOT NULL DEFAULT '' CHECK (head_commit = '' OR (length(head_commit) = 40 AND head_commit NOT GLOB '*[^0-9a-f]*')),
    branch                   TEXT NOT NULL DEFAULT '',
    code_repair_rounds       INTEGER NOT NULL DEFAULT 0 CHECK (code_repair_rounds >= 0),
    concept_repair_rounds    INTEGER NOT NULL DEFAULT 0 CHECK (concept_repair_rounds >= 0),
    last_result_role         TEXT CHECK (last_result_role IS NULL OR last_result_role IN ('supervisor','project_setup','concept','concept_reviewer','implementer','reviewer','documentation','completion','question_handler')),
    last_result_result       TEXT,
    -- Zeilenrevision (ab 1) für Compare-and-Set; steigt mit jeder Änderung des Auftrags und seiner Teile.
    revision                 INTEGER NOT NULL CHECK (revision >= 1),
    created_at               TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at               TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, work_order_id),
    CHECK ((last_result_role IS NULL) = (last_result_result IS NULL)),
    FOREIGN KEY (project_id, goal_id) REFERENCES goals (project_id, goal_id)
) STRICT;
CREATE INDEX work_orders_by_goal ON work_orders (project_id, goal_id);
CREATE INDEX work_orders_by_state ON work_orders (project_id, state);

CREATE TABLE work_order_dependencies (
    project_id    TEXT NOT NULL,
    work_order_id TEXT NOT NULL,
    depends_on_id TEXT NOT NULL,
    PRIMARY KEY (project_id, work_order_id, depends_on_id),
    CHECK (work_order_id <> depends_on_id),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, depends_on_id) REFERENCES work_orders (project_id, work_order_id)
) STRICT;
CREATE INDEX work_order_dependencies_reverse ON work_order_dependencies (project_id, depends_on_id);

-- Konzeptstand eines Auftrags. Die Kennung 'kein-konzept' (supervisor.NoConceptVersion) ist reserviert.
CREATE TABLE concept_states (
    project_id       TEXT NOT NULL,
    work_order_id    TEXT PRIMARY KEY,
    version          TEXT NOT NULL CHECK (length(version) BETWEEN 1 AND 128 AND version <> 'kein-konzept'),
    class            TEXT NOT NULL DEFAULT '' CHECK (class IN ('','routine','strategic')),
    criteria_json    TEXT NOT NULL CHECK (json_valid(criteria_json) AND json_type(criteria_json) = 'array'),
    approval_version TEXT CHECK (approval_version IS NULL OR length(approval_version) BETWEEN 1 AND 128),
    approver         TEXT CHECK (approver IS NULL OR approver IN ('concept_reviewer','user')),
    updated_at       TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    CHECK ((approval_version IS NULL) = (approver IS NULL)),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id)
) STRICT;

CREATE TABLE pull_requests (
    project_id    TEXT NOT NULL,
    work_order_id TEXT PRIMARY KEY,
    number        INTEGER NOT NULL CHECK (number >= 1),
    state         TEXT NOT NULL CHECK (state IN ('open','merged','closed')),
    ci            TEXT NOT NULL CHECK (ci IN ('passed','failed','pending','missing')),
    head_commit   TEXT NOT NULL DEFAULT '' CHECK (head_commit = '' OR (length(head_commit) = 40 AND head_commit NOT GLOB '*[^0-9a-f]*')),
    updated_at    TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, number),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id)
) STRICT;

-- Asana-Abbild: interne Task-ID, GID, optional der eine Auftrag; kein fachlicher Zustand.
CREATE TABLE tasks (
    task_id       TEXT PRIMARY KEY CHECK (length(task_id) BETWEEN 1 AND 128),
    project_id    TEXT NOT NULL REFERENCES projects (project_id),
    asana_gid     TEXT NOT NULL CHECK (length(asana_gid) BETWEEN 1 AND 64),
    work_order_id TEXT,
    title         TEXT NOT NULL DEFAULT '',
    priority      TEXT NOT NULL DEFAULT '',
    section       TEXT NOT NULL DEFAULT '',
    synced_at     TEXT NOT NULL CHECK (synced_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    created_at    TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at    TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, asana_gid),
    UNIQUE (project_id, task_id),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id)
) STRICT;
CREATE INDEX tasks_by_order ON tasks (project_id, work_order_id);

-- ---- Ausführungen ------------------------------------------------------------------------------

-- Logische Ausführung; Versuche stehen in execution_attempts.
CREATE TABLE executions (
    execution_id  TEXT PRIMARY KEY CHECK (length(execution_id) BETWEEN 1 AND 128),
    project_id    TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id TEXT,
    agent_id      TEXT,
    role          TEXT NOT NULL CHECK (role IN ('supervisor','project_setup','concept','concept_reviewer','implementer','reviewer','documentation','completion','question_handler')),
    created_at    TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, execution_id),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, agent_id) REFERENCES agents (project_id, agent_id)
) STRICT;
CREATE INDEX executions_by_order ON executions (project_id, work_order_id);

-- Ein Versuch. state: Ausführungszustand; process_* und os_process_*: Prozessidentität und beobachteter
-- Prozesszustand (eigene Ebenen); der Auftragszustand liegt in work_orders.
CREATE TABLE execution_attempts (
    project_id          TEXT NOT NULL,
    execution_id        TEXT NOT NULL,
    attempt             INTEGER NOT NULL CHECK (attempt >= 1),
    profile_id          TEXT NOT NULL,
    profile_version     INTEGER NOT NULL,
    skill_snapshot_id   TEXT,
    session_id          TEXT NOT NULL DEFAULT '',
    state               TEXT NOT NULL CHECK (state IN ('initializing','running','awaiting_answer','stopping','succeeded','controlled_failure','crashed','cancelled')),
    process_pid         INTEGER CHECK (process_pid IS NULL OR process_pid >= 1),
    process_start_time  TEXT CHECK (process_start_time IS NULL OR process_start_time GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    os_process_state    TEXT CHECK (os_process_state IS NULL OR os_process_state IN ('running','sleeping','stopped','zombie','exited','unknown')),
    os_observed_at      TEXT CHECK (os_observed_at IS NULL OR os_observed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    worktree_path       TEXT NOT NULL DEFAULT '',
    branch              TEXT NOT NULL DEFAULT '',
    started_at          TEXT NOT NULL CHECK (started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    ended_at            TEXT CHECK (ended_at IS NULL OR ended_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    terminal_source     TEXT CHECK (terminal_source IS NULL OR terminal_source IN ('result','error','process_exit')),
    exit_code           INTEGER,
    outcome             TEXT CHECK (outcome IS NULL OR outcome IN ('succeeded','controlled_failure','cancelled')),
    summary             TEXT NOT NULL DEFAULT '' CHECK (length(CAST(summary AS BLOB)) <= 2048),
    spec_json           TEXT CHECK (spec_json IS NULL OR (json_valid(spec_json) AND json_type(spec_json) = 'object')),
    PRIMARY KEY (execution_id, attempt),
    UNIQUE (project_id, execution_id, attempt),
    -- Identität: PID und Startzeit, beide oder keins. Prozesszustand und Beobachtungszeit ebenso.
    CHECK ((process_pid IS NULL) = (process_start_time IS NULL)),
    CHECK ((os_process_state IS NULL) = (os_observed_at IS NULL)),
    CHECK (os_process_state IS NULL OR process_pid IS NOT NULL),
    -- Ein Versuch hat höchstens ein terminales Ergebnis: genau die Endzustände tragen es.
    CHECK ((state IN ('succeeded','controlled_failure','crashed','cancelled')) = (ended_at IS NOT NULL)),
    CHECK ((ended_at IS NOT NULL) = (terminal_source IS NOT NULL)),
    CHECK ((terminal_source IS 'process_exit') = (exit_code IS NOT NULL)),
    CHECK ((terminal_source IS 'result') = (outcome IS NOT NULL)),
    FOREIGN KEY (project_id, execution_id) REFERENCES executions (project_id, execution_id),
    FOREIGN KEY (project_id, profile_id, profile_version) REFERENCES profile_versions (project_id, profile_id, version),
    FOREIGN KEY (project_id, skill_snapshot_id) REFERENCES skill_snapshots (project_id, snapshot_id)
) STRICT;
CREATE INDEX execution_attempts_by_state ON execution_attempts (project_id, state);

-- Das terminale Ergebnis eines Versuchs ändert sich nicht mehr.
CREATE TRIGGER execution_attempts_terminal_fixed BEFORE UPDATE ON execution_attempts
WHEN OLD.ended_at IS NOT NULL AND (
       NEW.state IS NOT OLD.state OR NEW.ended_at IS NOT OLD.ended_at OR NEW.terminal_source IS NOT OLD.terminal_source
    OR NEW.exit_code IS NOT OLD.exit_code OR NEW.outcome IS NOT OLD.outcome)
BEGIN
    SELECT RAISE(ABORT, 'terminales Ergebnis eines Versuchs ist unveraenderlich');
END;

-- Reservierungen (Logik folgt in CC-42). Höchstens eine aktive Lease je Auftrag, je Agent und je Worktree.
CREATE TABLE leases (
    lease_id      TEXT PRIMARY KEY CHECK (length(lease_id) BETWEEN 1 AND 128),
    project_id    TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id TEXT NOT NULL,
    execution_id  TEXT,
    attempt       INTEGER,
    agent_id      TEXT NOT NULL,
    generation    INTEGER NOT NULL CHECK (generation >= 1),
    state         TEXT NOT NULL CHECK (state IN ('active','released','expired','revoked')),
    worktree_path TEXT NOT NULL CHECK (substr(worktree_path, 1, 1) = '/'),
    acquired_at   TEXT NOT NULL CHECK (acquired_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    expires_at    TEXT NOT NULL CHECK (expires_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    released_at   TEXT CHECK (released_at IS NULL OR released_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (work_order_id, generation),
    CHECK ((execution_id IS NULL) = (attempt IS NULL)),
    CHECK ((state = 'active') = (released_at IS NULL)),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, execution_id, attempt) REFERENCES execution_attempts (project_id, execution_id, attempt),
    FOREIGN KEY (project_id, agent_id) REFERENCES agents (project_id, agent_id)
) STRICT;
CREATE UNIQUE INDEX leases_one_active_per_order ON leases (work_order_id) WHERE state = 'active';
CREATE UNIQUE INDEX leases_one_active_per_agent ON leases (project_id, agent_id) WHERE state = 'active';
CREATE UNIQUE INDEX leases_one_active_per_worktree ON leases (worktree_path) WHERE state = 'active';

-- ---- Planung und Entscheidungen ----------------------------------------------------------------

-- Ein Planungsergebnis (DecisionBatch) mit Urteil des Validators. state_revision ist die Revision, auf der
-- der Plan beruht (nicht die aktuelle). Ein Bezug auf das Ziel besteht bewusst nicht als Fremdschlüssel:
-- auch ein abgelehnter Plan mit unbekanntem Ziel (unknown_reference) wird festgehalten.
CREATE TABLE plans (
    project_id               TEXT NOT NULL REFERENCES projects (project_id),
    batch_id                 TEXT NOT NULL CHECK (length(batch_id) BETWEEN 1 AND 128),
    state_revision           INTEGER NOT NULL CHECK (state_revision >= 0),
    goal_kind                TEXT NOT NULL CHECK (goal_kind IN ('ticket','feature','milestone')),
    goal_id                  TEXT NOT NULL CHECK (length(goal_id) BETWEEN 1 AND 128),
    batch_json               TEXT NOT NULL CHECK (json_valid(batch_json) AND json_extract(batch_json, '$.batch_id') = batch_id),
    verdict                  TEXT NOT NULL CHECK (verdict IN ('accepted','rejected')),
    rejections_json          TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(rejections_json) AND json_type(rejections_json) = 'array'),
    planning_snapshot_sha256 TEXT NOT NULL CHECK (length(planning_snapshot_sha256) = 64 AND planning_snapshot_sha256 NOT GLOB '*[^0-9a-f]*'),
    current_snapshot_sha256  TEXT NOT NULL DEFAULT '' CHECK (current_snapshot_sha256 = '' OR (length(current_snapshot_sha256) = 64 AND current_snapshot_sha256 NOT GLOB '*[^0-9a-f]*')),
    attempt_json             TEXT CHECK (attempt_json IS NULL OR (json_valid(attempt_json) AND json_type(attempt_json) = 'object')),
    created_at               TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (project_id, batch_id),
    CHECK ((verdict = 'accepted') = (json_array_length(rejections_json) = 0))
) STRICT;
CREATE INDEX plans_by_goal ON plans (project_id, goal_kind, goal_id, created_at);

-- decision_id stammt vom Modell und ist nur innerhalb eines Batches eindeutig.
CREATE TABLE decisions (
    project_id              TEXT NOT NULL,
    batch_id                TEXT NOT NULL,
    decision_id             TEXT NOT NULL CHECK (length(decision_id) BETWEEN 1 AND 128),
    position                INTEGER NOT NULL CHECK (position >= 0),
    action                  TEXT NOT NULL CHECK (action IN ('plan_extend','concept_work','implementation','review','documentation_work','ask_question','request_integration','wait','request_completion')),
    target_kind             TEXT NOT NULL CHECK (target_kind IN ('ticket','feature','milestone','work_order')),
    target_id               TEXT NOT NULL CHECK (length(target_id) BETWEEN 1 AND 128),
    decision_json           TEXT NOT NULL CHECK (json_valid(decision_json) AND json_extract(decision_json, '$.decision_id') = decision_id),
    validation              TEXT NOT NULL CHECK (validation IN ('accepted','rejected')),
    -- Wirksames Profil der Delegation (domain.AgentProfile); NULL bei Aktionen ohne Auswahl.
    delegation_profile_json TEXT CHECK (delegation_profile_json IS NULL OR (json_valid(delegation_profile_json) AND json_type(delegation_profile_json) = 'object')),
    PRIMARY KEY (project_id, batch_id, decision_id),
    UNIQUE (project_id, batch_id, position),
    FOREIGN KEY (project_id, batch_id) REFERENCES plans (project_id, batch_id)
) STRICT;

-- ---- Fragen und Antworten ----------------------------------------------------------------------

CREATE TABLE questions (
    question_id           TEXT PRIMARY KEY CHECK (length(question_id) BETWEEN 1 AND 128),
    project_id            TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id         TEXT NOT NULL,
    execution_id          TEXT NOT NULL,
    attempt               INTEGER NOT NULL,
    text                  TEXT NOT NULL,
    context               TEXT NOT NULL,
    recommendation        TEXT NOT NULL DEFAULT '',
    state                 TEXT NOT NULL CHECK (state IN ('open','answered','delivered','superseded','cancelled')),
    created_at            TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    answer_id             TEXT,
    -- Zustellbestätigung an der Frage (erst nach bestätigter Zustellung gesetzt).
    delivery_confirmed_at TEXT CHECK (delivery_confirmed_at IS NULL OR delivery_confirmed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, question_id),
    CHECK ((state = 'delivered') = (delivery_confirmed_at IS NOT NULL)),
    CHECK (state NOT IN ('answered','delivered') OR answer_id IS NOT NULL),
    CHECK (state <> 'open' OR answer_id IS NULL),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, execution_id, attempt) REFERENCES execution_attempts (project_id, execution_id, attempt)
) STRICT;
CREATE INDEX questions_by_state ON questions (project_id, state);

CREATE TABLE answers (
    answer_id   TEXT PRIMARY KEY CHECK (length(answer_id) BETWEEN 1 AND 128),
    project_id  TEXT NOT NULL,
    question_id TEXT NOT NULL UNIQUE,
    text        TEXT NOT NULL,
    created_at  TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    actor       TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 128),
    FOREIGN KEY (project_id, question_id) REFERENCES questions (project_id, question_id)
) STRICT;

CREATE TRIGGER answers_no_update BEFORE UPDATE ON answers
BEGIN SELECT RAISE(ABORT, 'answers sind unveraenderlich'); END;
CREATE TRIGGER answers_no_delete BEFORE DELETE ON answers
BEGIN SELECT RAISE(ABORT, 'answers sind unveraenderlich'); END;

-- ---- Review ------------------------------------------------------------------------------------

-- Die Rolle der Reviewer-Ausführung steht nur in executions.role (Join), nicht doppelt.
CREATE TABLE review_reports (
    report_id                 TEXT PRIMARY KEY CHECK (length(report_id) BETWEEN 1 AND 128),
    project_id                TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id             TEXT NOT NULL,
    reviewer_execution_id     TEXT NOT NULL,
    implementer_execution_id  TEXT,
    commit_sha                TEXT NOT NULL CHECK (length(commit_sha) = 40 AND commit_sha NOT GLOB '*[^0-9a-f]*'),
    concept_version           TEXT NOT NULL CHECK (length(concept_version) BETWEEN 1 AND 128),
    verdict                   TEXT NOT NULL CHECK (verdict IN ('FREIGEGEBEN','BEFUNDE')),
    findings_json             TEXT NOT NULL CHECK (json_valid(findings_json) AND json_type(findings_json) = 'array'),
    report_json               TEXT NOT NULL CHECK (json_valid(report_json) AND json_extract(report_json, '$.report_id') = report_id),
    created_at                TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, reviewer_execution_id) REFERENCES executions (project_id, execution_id),
    FOREIGN KEY (project_id, implementer_execution_id) REFERENCES executions (project_id, execution_id)
) STRICT;
CREATE INDEX review_reports_by_order ON review_reports (project_id, work_order_id, created_at);

-- ---- Nachrichten und externe Aktionen (Logik folgt in CC-45) ------------------------------------

CREATE TABLE messages (
    message_id     TEXT PRIMARY KEY CHECK (length(message_id) BETWEEN 1 AND 128),
    project_id     TEXT NOT NULL REFERENCES projects (project_id),
    work_order_id  TEXT,
    execution_id   TEXT,
    attempt        INTEGER,
    sender         TEXT NOT NULL CHECK (length(sender) BETWEEN 1 AND 128),
    recipient      TEXT NOT NULL CHECK (length(recipient) BETWEEN 1 AND 128),
    type           TEXT NOT NULL CHECK (length(type) BETWEEN 1 AND 64),
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json   TEXT NOT NULL CHECK (json_valid(payload_json)),
    created_at     TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (project_id, message_id),
    CHECK ((execution_id IS NULL) = (attempt IS NULL)),
    FOREIGN KEY (project_id, work_order_id) REFERENCES work_orders (project_id, work_order_id),
    FOREIGN KEY (project_id, execution_id, attempt) REFERENCES execution_attempts (project_id, execution_id, attempt)
) STRICT;

CREATE TABLE message_deliveries (
    message_id      TEXT NOT NULL REFERENCES messages (message_id),
    recipient       TEXT NOT NULL CHECK (length(recipient) BETWEEN 1 AND 128),
    state           TEXT NOT NULL CHECK (state IN ('pending','delivered','acknowledged')),
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    delivered_at    TEXT CHECK (delivered_at IS NULL OR delivered_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    acknowledged_at TEXT CHECK (acknowledged_at IS NULL OR acknowledged_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    PRIMARY KEY (message_id, recipient)
) STRICT;

CREATE TABLE outbox (
    action_id        TEXT PRIMARY KEY CHECK (length(action_id) BETWEEN 1 AND 128),
    project_id       TEXT NOT NULL REFERENCES projects (project_id),
    operation_key    TEXT NOT NULL UNIQUE CHECK (length(operation_key) BETWEEN 1 AND 256),
    kind             TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    target           TEXT NOT NULL DEFAULT '',
    payload_json     TEXT NOT NULL CHECK (json_valid(payload_json)),
    state            TEXT NOT NULL CHECK (state IN ('pending','claimed','retry_wait','reconciling','succeeded','needs_attention','cancelled')),
    attempts         INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  TEXT CHECK (next_attempt_at IS NULL OR next_attempt_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    claim_expires_at TEXT CHECK (claim_expires_at IS NULL OR claim_expires_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    last_error       TEXT NOT NULL DEFAULT '' CHECK (length(CAST(last_error AS BLOB)) <= 1024),
    external_ref     TEXT,
    created_at       TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at       TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;
CREATE INDEX outbox_by_state ON outbox (project_id, state, next_attempt_at);

-- ---- Auditverlauf ------------------------------------------------------------------------------

-- Nur anhängbar: Ändern und Löschen sind per Trigger gesperrt.
CREATE TABLE events (
    event_id       INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id     TEXT NOT NULL REFERENCES projects (project_id),
    occurred_at    TEXT NOT NULL CHECK (occurred_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    actor          TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 128),
    kind           TEXT NOT NULL CHECK (length(kind) BETWEEN 3 AND 64),
    entity_type    TEXT NOT NULL CHECK (length(entity_type) BETWEEN 1 AND 64),
    entity_id      TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 300),
    level          TEXT NOT NULL CHECK (level IN ('config','goal','work_order','execution','process','agent','question','decision','review','task','lease','message','outbox')),
    from_state     TEXT NOT NULL DEFAULT '',
    to_state       TEXT NOT NULL DEFAULT '',
    state_revision INTEGER NOT NULL CHECK (state_revision >= 0),
    details_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(details_json) AND json_type(details_json) = 'object' AND length(CAST(details_json AS BLOB)) <= 4096)
) STRICT;
CREATE INDEX events_by_project ON events (project_id, event_id);
CREATE INDEX events_by_entity ON events (project_id, entity_type, entity_id, event_id);

CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events sind nur anhaengbar'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events sind nur anhaengbar'); END;
