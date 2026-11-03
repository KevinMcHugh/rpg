-- migrate:up

-- The game's authored content, and the notes that comment on it.
--
-- Every string a player can see (a character's name, a line of dialogue, a
-- choice) is stored twice: on its row, for reading, and as a row in
-- text_revisions that records which api key wrote it. Only a human key may
-- add a revision (the trigger below), and the build refuses a string whose
-- row and latest revision disagree or whose revision an agent wrote. See
-- docs/authorship.md.

CREATE TABLE text_revisions (
    id          CHAR(20)    PRIMARY KEY,
    target_kind TEXT        NOT NULL CHECK (target_kind IN ('character', 'node', 'choice')),
    target_id   CHAR(20)    NOT NULL,
    field       TEXT        NOT NULL,
    body        TEXT        NOT NULL,
    author_key  CHAR(20)    NOT NULL REFERENCES api_keys(id),
    -- Append-only: a revision is never changed or deleted, so it has no
    -- updated_at or deleted_at.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX text_revisions_target ON text_revisions (target_kind, target_id, created_at DESC);

-- The last lock: whatever the application does, the database will not
-- record player-facing text written by anything but a human key.
CREATE FUNCTION text_revisions_human_only() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM api_keys WHERE id = NEW.author_key AND kind = 'human') THEN
        RAISE EXCEPTION 'text revision by key % refused: only a human key may write player-facing text', NEW.author_key
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER text_revisions_human_only BEFORE INSERT OR UPDATE ON text_revisions
    FOR EACH ROW EXECUTE FUNCTION text_revisions_human_only();

CREATE TABLE characters (
    id               CHAR(20)    PRIMARY KEY,
    name             TEXT        NOT NULL,
    name_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    -- The character bible: voice, wants, secrets, sample lines. Authoring
    -- only; it never ships.
    bible            TEXT        NOT NULL DEFAULT '',
    created_by       CHAR(20)    REFERENCES api_keys(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX characters_active ON characters (name) WHERE deleted_at IS NULL;

CREATE TABLE conversations (
    id            CHAR(20)    PRIMARY KEY,
    -- Title and summary are authoring labels; they never ship.
    title         TEXT        NOT NULL,
    summary       TEXT        NOT NULL DEFAULT '',
    start_node_id CHAR(20),
    created_by    CHAR(20)    REFERENCES api_keys(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX conversations_active ON conversations (created_at DESC) WHERE deleted_at IS NULL;

-- A line of dialogue. A node with no speaker is the narrator.
CREATE TABLE nodes (
    id               CHAR(20)    PRIMARY KEY,
    conversation_id  CHAR(20)    NOT NULL REFERENCES conversations(id),
    speaker_id       CHAR(20)    REFERENCES characters(id),
    text             TEXT        NOT NULL,
    text_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    created_by       CHAR(20)    REFERENCES api_keys(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX nodes_conversation ON nodes (conversation_id, created_at) WHERE deleted_at IS NULL;

ALTER TABLE conversations ADD FOREIGN KEY (start_node_id) REFERENCES nodes(id);

-- What the player can say at a node. A choice with no to_node_id ends the
-- conversation.
CREATE TABLE choices (
    id               CHAR(20)    PRIMARY KEY,
    from_node_id     CHAR(20)    NOT NULL REFERENCES nodes(id),
    to_node_id       CHAR(20)    REFERENCES nodes(id),
    text             TEXT        NOT NULL,
    text_revision_id CHAR(20)    NOT NULL REFERENCES text_revisions(id),
    sort_order       INT         NOT NULL DEFAULT 0,
    created_by       CHAR(20)    REFERENCES api_keys(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX choices_from ON choices (from_node_id, sort_order) WHERE deleted_at IS NULL;

-- Feedback on the content: gaps, continuity problems, plot ideas,
-- questions. The one table an agent key may write. Notes never ship.
CREATE TABLE notes (
    id          CHAR(20)    PRIMARY KEY,
    target_kind TEXT        NOT NULL CHECK (target_kind IN ('game', 'character', 'conversation', 'node', 'choice')),
    -- NULL when target_kind is 'game'.
    target_id   CHAR(20),
    kind        TEXT        NOT NULL CHECK (kind IN ('gap', 'continuity', 'plot_idea', 'pacing', 'mechanics', 'question', 'other')),
    body        TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    -- A reply shares its parent's target.
    parent_id   CHAR(20)    REFERENCES notes(id),
    -- Whether a person wrote it. Recorded from the request, not looked up
    -- from created_by: a connector token acts as the human key that
    -- approved it, but what wrote the note was a model.
    by_human    BOOLEAN     NOT NULL,
    created_by  CHAR(20)    REFERENCES api_keys(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ,
    CHECK ((target_kind = 'game') = (target_id IS NULL))
);

CREATE INDEX notes_target ON notes (target_kind, target_id, created_at) WHERE deleted_at IS NULL;

-- migrate:down

DROP TABLE notes;
DROP TABLE choices;
ALTER TABLE conversations DROP CONSTRAINT conversations_start_node_id_fkey;
DROP TABLE nodes;
DROP TABLE conversations;
DROP TABLE characters;
DROP TABLE text_revisions;
DROP FUNCTION text_revisions_human_only();
