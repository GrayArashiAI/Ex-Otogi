-- +goose Up
-- What agents remember of each chat, beyond the transcript: one book per
-- agent and chat, kept past the retention. people maps the actor IDs an
-- entry is about to their names when it was noted, as JSON.
CREATE TABLE memories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    profile      TEXT    NOT NULL,
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    text         TEXT    NOT NULL,
    people       TEXT    NOT NULL,
    created_at   INTEGER NOT NULL, -- unix ms
    updated_at   INTEGER NOT NULL  -- unix ms
);
CREATE INDEX memories_book ON memories (profile, platform, account, conversation);

-- How far each agent has gone through each chat's transcript: the last
-- message it took into its memory, and how many times in a row the model
-- answered the batch after it with nothing usable.
CREATE TABLE memory_marks (
    profile      TEXT    NOT NULL,
    platform     TEXT    NOT NULL,
    account      TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    at           INTEGER NOT NULL, -- unix ms of the last message taken
    source_id    TEXT    NOT NULL, -- its source ID
    unusable     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (profile, platform, account, conversation)
);
