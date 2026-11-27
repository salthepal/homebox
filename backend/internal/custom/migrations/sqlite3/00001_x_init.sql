-- +goose Up
-- Fork bookkeeping table; proves the custom migration pipeline end to end.
CREATE TABLE IF NOT EXISTS x_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
INSERT INTO x_meta (key, value) VALUES ('schema', 'custom') ON CONFLICT (key) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS x_meta;
