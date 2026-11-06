-- +goose Up
-- What is known of users and groups: names from the messages they send and
-- mention, and a group's details from the group info API.
CREATE TABLE peers (
    peer        TEXT    PRIMARY KEY, -- user:<openid> or group:<openid>
    name        TEXT    NOT NULL DEFAULT '',
    description TEXT    NOT NULL DEFAULT '',
    members     INTEGER NOT NULL DEFAULT 0,
    looked_up   INTEGER NOT NULL DEFAULT 0 -- unix ms of the last API lookup; 0 for never
);
