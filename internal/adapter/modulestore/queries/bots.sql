-- name: BotReceivers :many
SELECT module FROM bot_receivers;

-- name: ReceiveBots :exec
INSERT INTO bot_receivers (module, changed_at, changed_by)
VALUES (sqlc.arg(module), sqlc.arg(changed_at), sqlc.arg(changed_by))
ON CONFLICT DO NOTHING;

-- name: IgnoreBots :exec
DELETE FROM bot_receivers WHERE module = sqlc.arg(module);
