-- +goose Up
ALTER TABLE timers ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0; -- failed attempts of this run
