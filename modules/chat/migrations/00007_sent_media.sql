-- +goose Up
-- The media of a picture an agent sent again, as its original message has
-- it, so the recent messages read it as that picture; empty for the agents'
-- other messages.
ALTER TABLE sent ADD COLUMN media TEXT NOT NULL DEFAULT '';
