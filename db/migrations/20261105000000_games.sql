-- migrate:up

-- One tool, several games. Each game has its own characters,
-- conversations, world, notes and saves, and its own ruleset (how checks
-- resolve: see docs/mechanics.md). Keys are unique within a game, so two
-- games can both have a flag called met_mayor.
--
-- Everything that existed becomes the first game, whose id is the zero
-- xid. The game_id columns keep that as their default so the previous
-- binary, which knows nothing of games, still runs on this schema (the
-- deploy's rollback swaps only the binary); new code always sets it.

CREATE TABLE games (
    id          CHAR(20)    PRIMARY KEY,
    key         TEXT        NOT NULL,
    -- An authoring label; never ships.
    name        TEXT        NOT NULL,
    ruleset     TEXT        NOT NULL DEFAULT 'classic' CHECK (ruleset IN ('classic', 'srd52', 'srd35', 'pf2e')),
    created_by  CHAR(20)    REFERENCES api_keys(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX games_key ON games (key) WHERE deleted_at IS NULL;

INSERT INTO games (id, key, name) VALUES ('00000000000000000000', 'first', 'First game');

ALTER TABLE characters    ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
ALTER TABLE conversations ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
ALTER TABLE flags         ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
ALTER TABLE skills        ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
ALTER TABLE quests        ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
ALTER TABLE notes         ADD COLUMN game_id CHAR(20) NOT NULL DEFAULT '00000000000000000000' REFERENCES games(id);
-- NULL for the game binary's saves, which play one bundle.
ALTER TABLE saves         ADD COLUMN game_id CHAR(20) REFERENCES games(id);
UPDATE saves SET game_id = '00000000000000000000' WHERE created_by IS NOT NULL;

DROP INDEX characters_key;
DROP INDEX conversations_key;
DROP INDEX conversations_one_opening;
DROP INDEX flags_key;
DROP INDEX skills_key;
DROP INDEX quests_key;
CREATE UNIQUE INDEX characters_key ON characters (game_id, key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX conversations_key ON conversations (game_id, key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX conversations_one_opening ON conversations (game_id) WHERE opening AND deleted_at IS NULL;
CREATE UNIQUE INDEX flags_key ON flags (game_id, key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX skills_key ON skills (game_id, key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX quests_key ON quests (game_id, key) WHERE deleted_at IS NULL;
CREATE INDEX notes_game ON notes (game_id, created_at) WHERE deleted_at IS NULL;

-- migrate:down

DROP INDEX notes_game;
DROP INDEX characters_key;
DROP INDEX conversations_key;
DROP INDEX conversations_one_opening;
DROP INDEX flags_key;
DROP INDEX skills_key;
DROP INDEX quests_key;
-- Only the first game's content fits the single-game schema.
DELETE FROM saves WHERE game_id <> '00000000000000000000';
UPDATE notes SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
UPDATE characters SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
UPDATE conversations SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
UPDATE flags SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
UPDATE skills SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
UPDATE quests SET deleted_at = NOW() WHERE game_id <> '00000000000000000000' AND deleted_at IS NULL;
CREATE UNIQUE INDEX characters_key ON characters (key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX conversations_key ON conversations (key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX conversations_one_opening ON conversations (opening) WHERE opening AND deleted_at IS NULL;
CREATE UNIQUE INDEX flags_key ON flags (key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX skills_key ON skills (key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX quests_key ON quests (key) WHERE deleted_at IS NULL;
ALTER TABLE saves DROP COLUMN game_id;
ALTER TABLE notes DROP COLUMN game_id;
ALTER TABLE quests DROP COLUMN game_id;
ALTER TABLE skills DROP COLUMN game_id;
ALTER TABLE flags DROP COLUMN game_id;
ALTER TABLE conversations DROP COLUMN game_id;
ALTER TABLE characters DROP COLUMN game_id;
DROP TABLE games;
