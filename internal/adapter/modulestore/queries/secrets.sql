-- name: Secrets :many
SELECT name, value, by, at FROM secrets ORDER BY name;

-- name: SetSecret :exec
INSERT INTO secrets (name, value, by, at)
VALUES (sqlc.arg(name), sqlc.arg(value), sqlc.arg(by), sqlc.arg(at))
ON CONFLICT (name) DO UPDATE SET value = excluded.value, by = excluded.by, at = excluded.at;

-- name: DeleteSecret :exec
DELETE FROM secrets WHERE name = sqlc.arg(name);
