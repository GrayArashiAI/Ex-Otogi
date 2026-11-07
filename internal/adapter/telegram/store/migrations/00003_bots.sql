-- +goose Up
-- Whether a user is a bot, from the same entities as the name.
ALTER TABLE names ADD COLUMN bot BOOLEAN NOT NULL DEFAULT FALSE;
