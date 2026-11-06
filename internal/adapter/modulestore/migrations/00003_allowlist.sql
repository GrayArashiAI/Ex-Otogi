-- +goose Up
CREATE TABLE allowlist (
    module   TEXT    NOT NULL,
    list     TEXT    NOT NULL,
    chat     TEXT    NOT NULL, -- <platform>:<conversation id>
    added_at INTEGER NOT NULL, -- unix ms
    added_by TEXT    NOT NULL,
    PRIMARY KEY (module, list, chat)
);
