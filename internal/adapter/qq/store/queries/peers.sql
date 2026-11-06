-- name: PeerInfo :one
SELECT peer, name, description, members, looked_up FROM peers WHERE peer = sqlc.arg(peer);

-- name: SetPeerName :exec
INSERT INTO peers (peer, name) VALUES (sqlc.arg(peer), sqlc.arg(name))
ON CONFLICT (peer) DO UPDATE SET name = excluded.name;

-- name: SetGroupInfo :exec
INSERT INTO peers (peer, name, description, members, looked_up)
VALUES (sqlc.arg(peer), sqlc.arg(name), sqlc.arg(description), sqlc.arg(members), sqlc.arg(looked_up))
ON CONFLICT (peer) DO UPDATE SET
    name = excluded.name, description = excluded.description, members = excluded.members, looked_up = excluded.looked_up;
