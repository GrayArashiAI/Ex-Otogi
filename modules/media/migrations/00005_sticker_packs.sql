-- +goose Up
-- Sticker packs (EXO-18): named lists of kept stickers, to give agents
-- together. A sticker may be in several packs, or in none; its picture is
-- kept once. A pack's ID is how chat's settings name it, and never changes.
CREATE TABLE sticker_packs (
    id          TEXT    PRIMARY KEY,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    made_by     TEXT    NOT NULL,
    made_at     INTEGER NOT NULL  -- unix ms
);

CREATE TABLE sticker_pack_members (
    pack    TEXT    NOT NULL REFERENCES sticker_packs (id) ON DELETE CASCADE,
    sticker INTEGER NOT NULL REFERENCES stickers (id) ON DELETE CASCADE,
    PRIMARY KEY (pack, sticker)
);
CREATE INDEX sticker_pack_members_sticker ON sticker_pack_members (sticker);
