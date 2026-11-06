-- name: CurrentSettings :many
SELECT s.module, s.revision, s.enabled, s.settings, s.by, s.at FROM module_settings s
WHERE s.revision = (SELECT MAX(revision) FROM module_settings WHERE module = s.module);

-- name: SettingsRevision :one
SELECT module, revision, enabled, settings, by, at FROM module_settings
WHERE module = sqlc.arg(module) AND revision = sqlc.arg(revision);

-- name: SettingsRevisions :many
SELECT module, revision, enabled, settings, by, at FROM module_settings
WHERE module = sqlc.arg(module) AND revision < sqlc.arg(before)
ORDER BY revision DESC LIMIT sqlc.arg(limit);

-- name: AddSettings :exec
INSERT INTO module_settings (module, revision, enabled, settings, by, at)
VALUES (sqlc.arg(module), sqlc.arg(revision), sqlc.arg(enabled), sqlc.arg(settings), sqlc.arg(by), sqlc.arg(at));
