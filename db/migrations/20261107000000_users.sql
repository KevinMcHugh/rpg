-- migrate:up

-- People, and who may work on which game.
--
-- A user is a person. They sign in with Google (user_identities, as the
-- inventory app has it) or with an api key the CLI minted for them, and
-- their agents' keys belong to them too, so an agent sees what its person
-- sees. A game has members: one owner, who may rename it, delete it and
-- invite people, and writers, who may write its content. Nobody else sees
-- the game at all.
--
-- The previous binary knows nothing of users, so every new column is
-- nullable and every new table is one it never reads: a rolled-back deploy
-- still runs, though games and keys it creates have no owner until someone
-- adds one.

CREATE TABLE users (
    id          CHAR(20)    PRIMARY KEY,
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

-- A sign-in with an identity provider. A (provider, subject) pair belongs
-- to exactly one user; a user may have several.
CREATE TABLE user_identities (
    id            CHAR(20)    PRIMARY KEY,
    user_id       CHAR(20)    NOT NULL REFERENCES users(id),
    provider      TEXT        NOT NULL,
    subject       TEXT        NOT NULL,
    email         TEXT        NOT NULL,
    display_name  TEXT,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX user_identities_provider_subject
    ON user_identities (provider, subject) WHERE deleted_at IS NULL;

ALTER TABLE api_keys ADD COLUMN user_id CHAR(20) REFERENCES users(id);

CREATE TABLE game_members (
    game_id     CHAR(20)    NOT NULL REFERENCES games(id),
    user_id     CHAR(20)    NOT NULL REFERENCES users(id),
    role        TEXT        NOT NULL CHECK (role IN ('owner', 'writer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX game_members_one ON game_members (game_id, user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX game_members_one_owner ON game_members (game_id) WHERE role = 'owner' AND deleted_at IS NULL;
CREATE INDEX game_members_user ON game_members (user_id) WHERE deleted_at IS NULL;

-- Single-use links an owner makes to bring someone into a game as a
-- writer. Redeeming one signs the person in (or up, with Google) and makes
-- them a member; it is not itself a credential.
CREATE TABLE game_invites (
    id          CHAR(20)    PRIMARY KEY,
    game_id     CHAR(20)    NOT NULL REFERENCES games(id),
    code_hash   TEXT        NOT NULL UNIQUE,
    note        TEXT        NOT NULL DEFAULT '',
    created_by  CHAR(20)    NOT NULL REFERENCES users(id),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    used_by     CHAR(20)    REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX game_invites_active ON game_invites (game_id, created_at)
    WHERE used_at IS NULL AND deleted_at IS NULL;

-- State carried across a Google sign-in: where to go after, the OAuth
-- authorize request it interrupted (a claude.ai connector), an invite to
-- redeem, or the signed-in user a new identity links to.
CREATE TABLE sso_login_intents (
    state                 TEXT        PRIMARY KEY,
    provider              TEXT        NOT NULL,
    return_to             TEXT,
    client_id             CHAR(20),
    redirect_uri          TEXT,
    code_challenge        TEXT,
    code_challenge_method TEXT,
    downstream_state      TEXT,
    scope                 TEXT,
    invite_code           TEXT,
    link_user_id          CHAR(20)    REFERENCES users(id),
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX sso_login_intents_expiry ON sso_login_intents (expires_at);

-- CLI invites now bring in a new user, recorded beside their key.
ALTER TABLE invites ADD COLUMN user_id CHAR(20) REFERENCES users(id);

-- Backfill. Every human key so far was a person, and keys with the same
-- name are the same person (rotating a key keeps its name): each name
-- becomes a user. Agent keys had no person, so they go to the first user,
-- which on a one-person install is the only one. The first user owns every
-- game, and every other user writes in all of them, which is the access
-- everyone had before.
INSERT INTO users (id, name, created_at)
SELECT DISTINCT ON (name) id, name, created_at FROM api_keys
WHERE kind = 'human' AND deleted_at IS NULL
ORDER BY name, created_at, id;

UPDATE api_keys SET user_id = users.id FROM users
WHERE api_keys.kind = 'human' AND api_keys.deleted_at IS NULL AND api_keys.name = users.name;

UPDATE api_keys SET user_id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1)
WHERE user_id IS NULL;

INSERT INTO game_members (game_id, user_id, role)
SELECT g.id, u.id,
       CASE WHEN u.id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1) THEN 'owner' ELSE 'writer' END
FROM games g CROSS JOIN users u
WHERE g.deleted_at IS NULL;

-- migrate:down

ALTER TABLE invites DROP COLUMN user_id;
DROP TABLE sso_login_intents;
DROP TABLE game_invites;
DROP TABLE game_members;
ALTER TABLE api_keys DROP COLUMN user_id;
DROP TABLE user_identities;
DROP TABLE users;
