-- +goose Up
-- The sender's standing in the conversation as the platform gave it with the
-- message (owner, admin), for the marks after their name. Messages from
-- before are left empty and are not marked.
ALTER TABLE transcript ADD COLUMN actor_role TEXT NOT NULL DEFAULT '';
