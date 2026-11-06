-- name: Operator :one
SELECT user_handle, cookie_key FROM operator;

-- name: CreateOperator :exec
INSERT INTO operator (singleton, user_handle, cookie_key) VALUES (1, sqlc.arg(user_handle), sqlc.arg(cookie_key))
ON CONFLICT DO NOTHING;

-- name: Passkeys :many
SELECT id, name, credential, created_at, used_at FROM passkeys ORDER BY created_at;

-- name: AddPasskey :exec
INSERT INTO passkeys (id, name, credential, created_at) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(credential), sqlc.arg(created_at));

-- name: UsePasskey :exec
UPDATE passkeys SET credential = sqlc.arg(credential), used_at = sqlc.arg(used_at) WHERE id = sqlc.arg(id);

-- name: RemovePasskey :execrows
DELETE FROM passkeys WHERE id = sqlc.arg(id);

-- name: AddEnrollment :exec
INSERT INTO enrollments (token_hash, expires_at) VALUES (sqlc.arg(token_hash), sqlc.arg(expires_at));

-- name: Enrollment :one
SELECT expires_at FROM enrollments WHERE token_hash = sqlc.arg(token_hash);

-- name: RemoveEnrollment :execrows
DELETE FROM enrollments WHERE token_hash = sqlc.arg(token_hash) OR expires_at < sqlc.arg(now);
