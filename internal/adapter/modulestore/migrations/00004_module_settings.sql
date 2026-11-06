-- +goose Up
-- Every saved change to a module's settings, append-only.
CREATE TABLE module_settings (
    module   TEXT    NOT NULL,
    revision INTEGER NOT NULL, -- 1, 2, ... per module
    enabled  BOOLEAN NOT NULL,
    settings TEXT    NOT NULL, -- JSON
    by       TEXT    NOT NULL,
    at       INTEGER NOT NULL, -- unix ms
    PRIMARY KEY (module, revision)
);
