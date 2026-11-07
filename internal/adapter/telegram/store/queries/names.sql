-- name: SavePeer :exec
INSERT INTO names (peer, name, bot) VALUES (sqlc.arg(peer), sqlc.arg(name), sqlc.arg(bot))
ON CONFLICT (peer) DO UPDATE SET name = excluded.name, bot = excluded.bot;

-- name: PeerInfo :one
SELECT name, bot FROM names WHERE peer = sqlc.arg(peer);
