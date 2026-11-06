-- +goose Up
-- Display names of users, chats, and channels, from the entities updates
-- carry: a conversation's title and a sender's name.
CREATE TABLE names (
    peer TEXT PRIMARY KEY, -- user:<id>, chat:<id>, or channel:<id>
    name TEXT NOT NULL
);
