-- +goose Up
-- What a sticker an agent sent shows, so the recent messages read it as
-- [sticker: ...]; empty for the agents' other messages.
ALTER TABLE sent ADD COLUMN sticker TEXT NOT NULL DEFAULT '';
