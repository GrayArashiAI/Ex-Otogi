-- +goose Up
-- The pictures and animations described, kept while the module's pictures
-- is set, for agents to look at one again after the platform's link to it
-- expired: one row per content, and the chats it was seen in, where alone
-- it can be looked at. Pruned hourly: with its description, and past
-- pictures.max_mb, the longest unseen first.
CREATE TABLE originals (
    algorithm  TEXT    NOT NULL, -- as its description's key
    digest     BLOB    NOT NULL,
    media_type TEXT    NOT NULL, -- as detected from the bytes
    bytes      BLOB    NOT NULL,
    size       INTEGER NOT NULL,
    seen_at    INTEGER NOT NULL, -- unix ms, last seen in any chat
    PRIMARY KEY (algorithm, digest)
);
CREATE INDEX originals_seen ON originals (seen_at);

CREATE TABLE original_chats (
    algorithm TEXT    NOT NULL,
    digest    BLOB    NOT NULL,
    chat      TEXT    NOT NULL, -- platform:conversation, as allowlists name it
    PRIMARY KEY (algorithm, digest, chat)
);
