-- migrate:up

-- World state and the rules that read and change it: flags, skills,
-- quests, conditions and effects on dialogue, skill checks, and saved
-- games. See docs/mechanics.md.
--
-- Anything a script refers to has a key: a short slug, unique among live
-- rows of its kind, that authors type in conditions and effects. Keys are
-- authoring-only. Scripts are stored with ids, so renaming a key never
-- breaks one.

ALTER TABLE text_revisions DROP CONSTRAINT text_revisions_target_kind_check;
ALTER TABLE text_revisions ADD CONSTRAINT text_revisions_target_kind_check
    CHECK (target_kind IN ('character', 'node', 'choice', 'conversation', 'skill', 'quest', 'quest_stage'));

ALTER TABLE notes DROP CONSTRAINT notes_target_kind_check;
ALTER TABLE notes ADD CONSTRAINT notes_target_kind_check
    CHECK (target_kind IN ('game', 'character', 'conversation', 'node', 'choice', 'flag', 'skill', 'quest'));

-- Characters: a key for scripts, and whether they can join the party
-- (companions have approval).
ALTER TABLE characters ADD COLUMN key TEXT;
UPDATE characters SET key = 'c' || id;
ALTER TABLE characters ALTER COLUMN key SET NOT NULL;
ALTER TABLE characters ADD COLUMN companion BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX characters_key ON characters (key) WHERE deleted_at IS NULL;

-- Conversations: a key, the label a player sees in the list of
-- conversations they can start (player-facing, so it has a revision; a
-- conversation with no label is only reached from another), when it is
-- available, whether it can be had again, and which one opens the game.
ALTER TABLE conversations ADD COLUMN key TEXT;
UPDATE conversations SET key = 'v' || id;
ALTER TABLE conversations ALTER COLUMN key SET NOT NULL;
ALTER TABLE conversations ADD COLUMN label TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN label_revision_id CHAR(20) REFERENCES text_revisions(id);
ALTER TABLE conversations ADD COLUMN condition JSONB;
ALTER TABLE conversations ADD COLUMN repeatable BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE conversations ADD COLUMN opening BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX conversations_key ON conversations (key) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX conversations_one_opening ON conversations (opening) WHERE opening AND deleted_at IS NULL;

-- A flag is a named number in the world state; 0 is false. Authoring-only.
CREATE TABLE flags (
    id            CHAR(20)    PRIMARY KEY,
    key           TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    initial_value INT         NOT NULL DEFAULT 0,
    created_by    CHAR(20)    REFERENCES api_keys(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX flags_key ON flags (key) WHERE deleted_at IS NULL;

-- A skill the player has, at a level. Its name shows on skill checks.
CREATE TABLE skills (
    id               CHAR(20)    PRIMARY KEY,
    key              TEXT        NOT NULL,
    name             TEXT        NOT NULL,
    name_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    initial_level    INT         NOT NULL DEFAULT 0,
    created_by       CHAR(20)    REFERENCES api_keys(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE UNIQUE INDEX skills_key ON skills (key) WHERE deleted_at IS NULL;

-- A quest's state is the number of the stage it is at; 0 is not started.
-- Its name and each stage's journal entry are player-facing.
CREATE TABLE quests (
    id               CHAR(20)    PRIMARY KEY,
    key              TEXT        NOT NULL,
    name             TEXT        NOT NULL,
    name_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    summary          TEXT        NOT NULL DEFAULT '',
    created_by       CHAR(20)    REFERENCES api_keys(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE UNIQUE INDEX quests_key ON quests (key) WHERE deleted_at IS NULL;

CREATE TABLE quest_stages (
    id                  CHAR(20)    PRIMARY KEY,
    quest_id            CHAR(20)    NOT NULL REFERENCES quests(id),
    number              INT         NOT NULL CHECK (number > 0),
    journal             TEXT        NOT NULL,
    journal_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    -- Reaching this stage finishes the quest.
    completes           BOOLEAN     NOT NULL DEFAULT FALSE,
    created_by          CHAR(20)    REFERENCES api_keys(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX quest_stages_number ON quest_stages (quest_id, number) WHERE deleted_at IS NULL;

-- Effects run when the player reaches a node.
ALTER TABLE nodes ADD COLUMN effects JSONB NOT NULL DEFAULT '[]';

-- A choice shows only when its condition holds, runs its effects when
-- taken, and may be a skill check: 2d6 plus the skill against the
-- difficulty, going to to_node_id on success and fail_node_id on failure.
ALTER TABLE choices ADD COLUMN condition JSONB;
ALTER TABLE choices ADD COLUMN effects JSONB NOT NULL DEFAULT '[]';
ALTER TABLE choices ADD COLUMN check_skill_id CHAR(20) REFERENCES skills(id);
ALTER TABLE choices ADD COLUMN check_difficulty INT NOT NULL DEFAULT 0;
ALTER TABLE choices ADD COLUMN fail_node_id CHAR(20) REFERENCES nodes(id);

-- Saved games. The authoring tool's playtests and the game binary both
-- keep them here (the game in a database of its own). created_by is the
-- playtester's key, and NULL in the game.
CREATE TABLE saves (
    id          CHAR(20)    PRIMARY KEY,
    name        TEXT        NOT NULL DEFAULT '',
    state       JSONB       NOT NULL,
    created_by  CHAR(20)    REFERENCES api_keys(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX saves_active ON saves (updated_at DESC) WHERE deleted_at IS NULL;

-- migrate:down

DROP TABLE saves;
ALTER TABLE choices DROP COLUMN fail_node_id, DROP COLUMN check_difficulty, DROP COLUMN check_skill_id,
    DROP COLUMN effects, DROP COLUMN condition;
ALTER TABLE nodes DROP COLUMN effects;
DROP TABLE quest_stages;
DROP TABLE quests;
DROP TABLE skills;
DROP TABLE flags;
DROP INDEX conversations_one_opening;
DROP INDEX conversations_key;
ALTER TABLE conversations DROP COLUMN opening, DROP COLUMN repeatable, DROP COLUMN condition,
    DROP COLUMN label_revision_id, DROP COLUMN label, DROP COLUMN key;
DROP INDEX characters_key;
ALTER TABLE characters DROP COLUMN companion, DROP COLUMN key;
DELETE FROM notes WHERE target_kind IN ('flag', 'skill', 'quest');
ALTER TABLE notes DROP CONSTRAINT notes_target_kind_check;
ALTER TABLE notes ADD CONSTRAINT notes_target_kind_check
    CHECK (target_kind IN ('game', 'character', 'conversation', 'node', 'choice'));
DELETE FROM text_revisions WHERE target_kind IN ('conversation', 'skill', 'quest', 'quest_stage');
ALTER TABLE text_revisions DROP CONSTRAINT text_revisions_target_kind_check;
ALTER TABLE text_revisions ADD CONSTRAINT text_revisions_target_kind_check
    CHECK (target_kind IN ('character', 'node', 'choice'));
