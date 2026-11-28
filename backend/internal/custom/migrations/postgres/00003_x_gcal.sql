-- +goose Up
-- Google Calendar sync state. group_id / connected_by / source_id are plain UUIDs with no
-- foreign keys to upstream tables (.fork/PLAN.md R4).
CREATE TABLE IF NOT EXISTS x_gcal_connections (
    group_id              UUID PRIMARY KEY,
    connected_by          UUID         NOT NULL,
    google_email          TEXT         NOT NULL DEFAULT '',
    calendar_id           TEXT         NOT NULL DEFAULT '',
    calendar_summary      TEXT         NOT NULL DEFAULT '',
    enc_refresh_token     BYTEA        NOT NULL,
    include_completed     BOOLEAN      NOT NULL DEFAULT TRUE,
    include_project_steps BOOLEAN      NOT NULL DEFAULT TRUE,
    reminder_minutes      INTEGER      NOT NULL DEFAULT 1440,
    broken                BOOLEAN      NOT NULL DEFAULT FALSE,
    last_sync_at          TIMESTAMPTZ,
    last_error            TEXT         NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS x_gcal_links (
    id           UUID PRIMARY KEY,
    group_id     UUID        NOT NULL,
    source_type  VARCHAR(20) NOT NULL,
    source_id    UUID        NOT NULL,
    event_id     TEXT        NOT NULL,
    content_hash TEXT        NOT NULL DEFAULT '',
    synced_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT x_gcal_links_type_chk CHECK (source_type IN ('maintenance', 'project_step'))
);
CREATE UNIQUE INDEX IF NOT EXISTS x_gcal_links_source_idx ON x_gcal_links (group_id, source_type, source_id);
CREATE INDEX IF NOT EXISTS x_gcal_links_event_idx ON x_gcal_links (group_id, event_id);

-- +goose Down
DROP TABLE IF EXISTS x_gcal_links;
DROP TABLE IF EXISTS x_gcal_connections;
