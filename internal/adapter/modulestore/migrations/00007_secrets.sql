-- +goose Up
-- Secrets set in the control panel, by the name settings refer to them
-- with, ${NAME}. The values are stored as given: whoever can read this file
-- can read a key kept next to it, so the data directory's permissions are
-- what protects them.
CREATE TABLE secrets (
    name  TEXT    NOT NULL PRIMARY KEY,
    value TEXT    NOT NULL,
    by    TEXT    NOT NULL,
    at    INTEGER NOT NULL -- unix ms
);
