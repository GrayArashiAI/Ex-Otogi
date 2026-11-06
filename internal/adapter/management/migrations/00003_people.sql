-- +goose Up
ALTER TABLE chats ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN members INTEGER NOT NULL DEFAULT 0;

-- The people seen in each chat: who sent its new messages, by the latest
-- name they were seen with. Rows outlive the records' retention.
CREATE TABLE chat_people (
    platform     TEXT    NOT NULL,
    conversation TEXT    NOT NULL,
    actor        TEXT    NOT NULL,
    name         TEXT    NOT NULL DEFAULT '',
    first_seen   INTEGER NOT NULL, -- unix ms
    last_seen    INTEGER NOT NULL, -- unix ms
    messages     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (platform, conversation, actor)
);
CREATE INDEX chat_people_actor ON chat_people (platform, actor, last_seen);

-- The people in the records kept so far.
INSERT INTO chat_people (platform, conversation, actor, name, first_seen, last_seen, messages)
SELECT r.platform, r.conversation, json_extract(r.payload, '$.actor.id'),
       COALESCE((SELECT json_extract(x.payload, '$.actor.name') FROM records x
                 WHERE x.kind = 'event.received' AND x.platform = r.platform AND x.conversation = r.conversation
                   AND json_extract(x.payload, '$.actor.id') = json_extract(r.payload, '$.actor.id')
                   AND IFNULL(json_extract(x.payload, '$.actor.name'), '') <> ''
                 ORDER BY x.id DESC LIMIT 1), ''),
       MIN(r.at), MAX(r.at), COUNT(*)
FROM records r
WHERE r.kind = 'event.received' AND r.platform <> '' AND r.conversation <> ''
  AND json_extract(r.payload, '$.action') = 'message.created'
  AND IFNULL(json_extract(r.payload, '$.direction'), '') = 'incoming'
  AND IFNULL(json_extract(r.payload, '$.actor.id'), '') <> ''
GROUP BY r.platform, r.conversation, json_extract(r.payload, '$.actor.id');
