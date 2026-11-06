-- name: SeeChat :exec
INSERT INTO chats (platform, conversation, account, kind, title, description, members, first_seen, last_seen, last_message)
VALUES (sqlc.arg(platform), sqlc.arg(conversation), sqlc.arg(account), sqlc.arg(kind), sqlc.arg(title), sqlc.arg(description), sqlc.arg(members), sqlc.arg(at), sqlc.arg(at), sqlc.arg(last_message))
ON CONFLICT (platform, conversation) DO UPDATE SET
    account      = excluded.account,
    kind         = CASE WHEN excluded.kind <> '' THEN excluded.kind ELSE chats.kind END,
    title        = CASE WHEN excluded.title <> '' THEN excluded.title ELSE chats.title END,
    description  = CASE WHEN excluded.description <> '' THEN excluded.description ELSE chats.description END,
    members      = CASE WHEN excluded.members <> 0 THEN excluded.members ELSE chats.members END,
    last_seen    = MAX(chats.last_seen, excluded.last_seen),
    last_message = CASE WHEN excluded.last_message <> 0 THEN excluded.last_message ELSE chats.last_message END;

-- name: CountMessage :exec
INSERT INTO chat_days (platform, conversation, day, messages)
VALUES (sqlc.arg(platform), sqlc.arg(conversation), sqlc.arg(day), 1)
ON CONFLICT (platform, conversation, day) DO UPDATE SET messages = chat_days.messages + 1;

-- name: Chats :many
SELECT c.platform, c.conversation, c.account, c.kind, c.title, c.name, c.description, c.members, c.first_seen, c.last_seen,
       (SELECT COUNT(*) FROM chat_people p WHERE p.platform = c.platform AND p.conversation = c.conversation) AS people,
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

-- name: SeePerson :exec
INSERT INTO chat_people (platform, conversation, actor, name, first_seen, last_seen, messages)
VALUES (sqlc.arg(platform), sqlc.arg(conversation), sqlc.arg(actor), sqlc.arg(name), sqlc.arg(at), sqlc.arg(at), 1)
ON CONFLICT (platform, conversation, actor) DO UPDATE SET
    name      = CASE WHEN excluded.name <> '' THEN excluded.name ELSE chat_people.name END,
    last_seen = MAX(chat_people.last_seen, excluded.last_seen),
    messages  = chat_people.messages + 1;

-- name: ChatPeople :many
SELECT actor, name, first_seen, last_seen, messages FROM chat_people
WHERE platform = sqlc.arg(platform) AND conversation = sqlc.arg(conversation)
ORDER BY last_seen DESC LIMIT sqlc.arg(limit);

-- name: PersonName :one
SELECT name FROM chat_people
WHERE platform = sqlc.arg(platform) AND actor = sqlc.arg(actor) AND name <> ''
ORDER BY last_seen DESC LIMIT 1;

-- name: ChatName :one
SELECT name, title FROM chats WHERE platform = sqlc.arg(platform) AND conversation = sqlc.arg(conversation);
