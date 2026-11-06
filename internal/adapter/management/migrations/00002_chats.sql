-- +goose Up
-- Every chat the bot has seen. Rows outlive the records' retention.
CREATE TABLE chats (
    platform     TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    account      TEXT    NOT NULL,           -- the account that saw it last
    kind         TEXT    NOT NULL DEFAULT '',
    title        TEXT    NOT NULL DEFAULT '', -- as the platform names it
    name         TEXT    NOT NULL DEFAULT '', -- as the operator names it; wins over title
    first_seen   INTEGER NOT NULL,            -- unix ms
    last_seen    INTEGER NOT NULL,            -- unix ms
    last_message INTEGER NOT NULL DEFAULT 0,  -- records.id of its latest new message
    PRIMARY KEY (platform, conversation)
);
-- New messages per chat per day (UTC), kept as long as the records.
CREATE TABLE chat_days (
    platform     TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    day          TEXT    NOT NULL, -- YYYY-MM-DD
    messages     INTEGER NOT NULL,
    PRIMARY KEY (platform, conversation, day)
);
