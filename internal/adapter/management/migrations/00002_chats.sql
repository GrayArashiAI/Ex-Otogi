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
-- The chats in the records kept so far.
INSERT INTO chats (platform, conversation, account, kind, title, first_seen, last_seen, last_message)
SELECT r.platform, r.conversation,
       (SELECT account FROM records x WHERE x.kind = 'event.received' AND x.platform = r.platform AND x.conversation = r.conversation ORDER BY x.id DESC LIMIT 1),
       COALESCE((SELECT json_extract(x.payload, '$.conversation.kind') FROM records x WHERE x.kind = 'event.received' AND x.platform = r.platform AND x.conversation = r.conversation ORDER BY x.id DESC LIMIT 1), ''),
       COALESCE((SELECT json_extract(x.payload, '$.conversation.title') FROM records x WHERE x.kind = 'event.received' AND x.platform = r.platform AND x.conversation = r.conversation AND json_extract(x.payload, '$.conversation.title') <> '' ORDER BY x.id DESC LIMIT 1), ''),
       MIN(r.at), MAX(r.at),
       COALESCE(MAX(CASE WHEN json_extract(r.payload, '$.action') = 'message.created' THEN r.id END), 0)
FROM records r
WHERE r.kind = 'event.received' AND r.platform <> '' AND r.conversation <> ''
GROUP BY r.platform, r.conversation;
INSERT INTO chat_days (platform, conversation, day, messages)
SELECT platform, conversation, date(at / 1000, 'unixepoch'), COUNT(*)
FROM records
WHERE kind = 'event.received' AND platform <> '' AND conversation <> '' AND json_extract(payload, '$.action') = 'message.created'
GROUP BY platform, conversation, date(at / 1000, 'unixepoch');
