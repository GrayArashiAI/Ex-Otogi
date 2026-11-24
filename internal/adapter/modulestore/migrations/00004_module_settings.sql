-- +goose Up
-- Every saved change to a module's settings, append-only. The settings
-- outside modules, edited in the panel in sections, are kept here too, under
-- "system:<section>" (system:general, system:llm) in the module column: a
-- module's ID has no colon, so the two never meet. Their enabled is false.
-- The table keeps its name.
CREATE TABLE module_settings (
    module   TEXT    NOT NULL,
    revision INTEGER NOT NULL, -- 1, 2, ... per module
    enabled  BOOLEAN NOT NULL,
    settings TEXT    NOT NULL, -- JSON
    by       TEXT    NOT NULL,
    at       INTEGER NOT NULL, -- unix ms
    PRIMARY KEY (module, revision)
);
