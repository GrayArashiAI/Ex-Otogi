-- +goose Up
-- Modules that receive messages from bot accounts: their "ignore bots"
-- switch is off. Every other module ignores bots.
CREATE TABLE bot_receivers (
    module     TEXT    PRIMARY KEY,
    changed_at INTEGER NOT NULL, -- unix ms
    changed_by TEXT    NOT NULL
);
