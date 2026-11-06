-- name: SeeChat :exec
INSERT INTO chats (platform, conversation, account, kind, title, first_seen, last_seen, last_message)
VALUES (sqlc.arg(platform), sqlc.arg(conversation), sqlc.arg(account), sqlc.arg(kind), sqlc.arg(title), sqlc.arg(at), sqlc.arg(at), sqlc.arg(last_message))
ON CONFLICT (platform, conversation) DO UPDATE SET
    account      = excluded.account,
    kind         = CASE WHEN excluded.kind <> '' THEN excluded.kind ELSE chats.kind END,
    title        = CASE WHEN excluded.title <> '' THEN excluded.title ELSE chats.title END,
    last_seen    = MAX(chats.last_seen, excluded.last_seen),
    last_message = CASE WHEN excluded.last_message <> 0 THEN excluded.last_message ELSE chats.last_message END;

-- name: CountMessage :exec
INSERT INTO chat_days (platform, conversation, day, messages)
VALUES (sqlc.arg(platform), sqlc.arg(conversation), sqlc.arg(day), 1)
ON CONFLICT (platform, conversation, day) DO UPDATE SET messages = chat_days.messages + 1;

-- name: Chats :many
SELECT c.platform, c.conversation, c.account, c.kind, c.title, c.name, c.first_seen, c.last_seen,
       r.id AS message_id, r.at AS message_at, r.payload AS message
FROM chats c LEFT JOIN records r ON r.id = c.last_message
ORDER BY c.last_seen DESC;

-- name: NameChat :execrows
UPDATE chats SET name = sqlc.arg(name) WHERE platform = sqlc.arg(platform) AND conversation = sqlc.arg(conversation);

-- name: ChatDays :many
SELECT day, messages FROM chat_days
WHERE platform = sqlc.arg(platform) AND conversation = sqlc.arg(conversation) AND day >= sqlc.arg(since)
ORDER BY day;

-- name: PruneChatDays :exec
DELETE FROM chat_days WHERE day < sqlc.arg(before);
