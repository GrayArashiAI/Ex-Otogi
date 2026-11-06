-- name: SaveName :exec
INSERT INTO names (peer, name) VALUES (sqlc.arg(peer), sqlc.arg(name))
ON CONFLICT (peer) DO UPDATE SET name = excluded.name;

-- name: Name :one
SELECT name FROM names WHERE peer = sqlc.arg(peer);
