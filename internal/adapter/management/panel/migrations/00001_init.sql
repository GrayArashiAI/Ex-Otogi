-- +goose Up
-- The one operator the passkeys belong to, and the key sealing session cookies.
CREATE TABLE operator (
    singleton   INTEGER PRIMARY KEY CHECK (singleton = 1),
    user_handle BLOB    NOT NULL, -- WebAuthn user handle
    cookie_key  BLOB    NOT NULL  -- 32 bytes to sign, 32 to encrypt
);
CREATE TABLE passkeys (
    id         BLOB    PRIMARY KEY, -- the credential ID
    name       TEXT    NOT NULL,
    credential TEXT    NOT NULL,    -- go-webauthn's Credential, as JSON
    created_at INTEGER NOT NULL,    -- unix ms
    used_at    INTEGER NOT NULL DEFAULT 0
);
-- One-time links to register a first passkey (otogi passkey enroll).
CREATE TABLE enrollments (
    token_hash BLOB    PRIMARY KEY, -- SHA-256 of the token
    expires_at INTEGER NOT NULL     -- unix ms
);
