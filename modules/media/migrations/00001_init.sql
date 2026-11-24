-- +goose Up
-- What each piece of media shows, keyed by a digest of its content.
CREATE TABLE descriptions (
    algorithm    TEXT    NOT NULL, -- md5 from the driver, or sha256 computed here
    digest       BLOB    NOT NULL,
    kind         TEXT    NOT NULL,
    summary      TEXT    NOT NULL,
    text         TEXT    NOT NULL,
    model        TEXT    NOT NULL, -- provider/model that described it
    described_at INTEGER NOT NULL, -- unix ms
    seen_at      INTEGER NOT NULL, -- unix ms, last time it was seen
    PRIMARY KEY (algorithm, digest)
);
CREATE INDEX descriptions_seen ON descriptions (seen_at);

-- Where media without a driver's digest came from (a URL, or a driver's
-- locator), so the same message's media is found again without fetching it.
CREATE TABLE sources (
    source    TEXT    PRIMARY KEY,
    algorithm TEXT    NOT NULL,
    digest    BLOB    NOT NULL,
    seen_at   INTEGER NOT NULL
);
CREATE INDEX sources_seen ON sources (seen_at);
