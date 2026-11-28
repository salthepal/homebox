-- +goose Up
-- One revocable ICS feed token per group. Only the SHA-256 of the token is stored
-- (.fork/PLAN.md G4); group_id is a plain UUID (R4).
CREATE TABLE IF NOT EXISTS x_ics_feeds (
    group_id   UUID PRIMARY KEY,
    token_hash TEXT        NOT NULL,
    created_by UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS x_ics_feeds_token_hash_idx ON x_ics_feeds (token_hash);

-- +goose Down
DROP TABLE IF EXISTS x_ics_feeds;
