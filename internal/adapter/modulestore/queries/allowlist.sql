-- name: Allowlists :many
SELECT module, list, chat, added_at, added_by FROM allowlist;

-- name: Allow :exec
INSERT INTO allowlist (module, list, chat, added_at, added_by)
VALUES (sqlc.arg(module), sqlc.arg(list), sqlc.arg(chat), sqlc.arg(added_at), sqlc.arg(added_by))
ON CONFLICT DO NOTHING;

-- name: Disallow :exec
DELETE FROM allowlist WHERE module = sqlc.arg(module) AND list = sqlc.arg(list) AND chat = sqlc.arg(chat);
