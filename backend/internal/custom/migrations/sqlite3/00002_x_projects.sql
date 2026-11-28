-- +goose Up
-- Projects (multi-step upgrades). Upstream IDs (group_id, entity_id, maintenance_entry_id) are
-- plain UUID columns with no foreign keys (.fork/PLAN.md R4); FKs only between x_ tables.
CREATE TABLE IF NOT EXISTS x_projects (
    id           TEXT PRIMARY KEY,
    group_id     TEXT         NOT NULL,
    name         VARCHAR(255) NOT NULL,
    description  TEXT         NOT NULL DEFAULT '',
    status       VARCHAR(20)  NOT NULL DEFAULT 'idea',
    priority     INTEGER      NOT NULL DEFAULT 0,
    target_date  DATE,
    budget       REAL,
    started_at   DATETIME,
    completed_at DATETIME,
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT x_projects_status_chk CHECK (status IN ('idea', 'planning', 'in_progress', 'on_hold', 'done', 'cancelled')),
    CONSTRAINT x_projects_priority_chk CHECK (priority BETWEEN 0 AND 3)
);
CREATE INDEX IF NOT EXISTS x_projects_group_status_idx ON x_projects (group_id, status);

CREATE TABLE IF NOT EXISTS x_project_steps (
    id                   TEXT PRIMARY KEY,
    project_id           TEXT         NOT NULL REFERENCES x_projects (id) ON DELETE CASCADE,
    position             INTEGER      NOT NULL DEFAULT 0,
    title                VARCHAR(255) NOT NULL,
    notes                TEXT         NOT NULL DEFAULT '',
    status               VARCHAR(20)  NOT NULL DEFAULT 'todo',
    due_date             DATE,
    est_cost             REAL,
    actual_cost          REAL,
    url                  TEXT,
    entity_id            TEXT,
    log_as_maintenance   BOOLEAN      NOT NULL DEFAULT 0,
    maintenance_entry_id TEXT,
    completed_at         DATETIME,
    created_at           DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT x_project_steps_status_chk CHECK (status IN ('todo', 'doing', 'blocked', 'done', 'skipped'))
);
CREATE INDEX IF NOT EXISTS x_project_steps_project_pos_idx ON x_project_steps (project_id, position);
CREATE INDEX IF NOT EXISTS x_project_steps_entity_idx ON x_project_steps (entity_id);

CREATE TABLE IF NOT EXISTS x_project_entities (
    project_id TEXT NOT NULL REFERENCES x_projects (id) ON DELETE CASCADE,
    entity_id  TEXT NOT NULL,
    PRIMARY KEY (project_id, entity_id)
);
CREATE INDEX IF NOT EXISTS x_project_entities_entity_idx ON x_project_entities (entity_id);

-- +goose Down
DROP TABLE IF EXISTS x_project_entities;
DROP TABLE IF EXISTS x_project_steps;
DROP TABLE IF EXISTS x_projects;
