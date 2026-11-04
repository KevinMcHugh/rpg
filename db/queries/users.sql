-- name: CreateUser :one
INSERT INTO users (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT * FROM users
WHERE deleted_at IS NULL
ORDER BY created_at, id;

-- name: GetUserIdentity :one
SELECT * FROM user_identities
WHERE provider = $1 AND subject = $2 AND deleted_at IS NULL;

-- name: CreateUserIdentity :one
INSERT INTO user_identities (id, user_id, provider, subject, email, display_name, last_login_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
RETURNING *;

-- name: TouchUserIdentity :exec
UPDATE user_identities SET
    last_login_at = NOW(),
    email = $2,
    display_name = COALESCE(sqlc.narg(display_name), display_name),
    updated_at = NOW()
WHERE id = $1;

-- name: ListUserIdentities :many
SELECT * FROM user_identities
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at;

-- A user's own human key, which their web sessions act as. Signing in with
-- Google mints one the first time; nobody ever sees it.
-- name: GetHumanKeyForUser :one
SELECT * FROM api_keys
WHERE user_id = $1 AND kind = 'human' AND deleted_at IS NULL
ORDER BY created_at, id
LIMIT 1;

-- ---- membership -------------------------------------------------------------

-- name: AddGameMember :exec
INSERT INTO game_members (game_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (game_id, user_id) WHERE deleted_at IS NULL DO NOTHING;

-- name: ListGameMembers :many
SELECT game_members.user_id, game_members.role, game_members.created_at, users.name FROM game_members
JOIN users ON users.id = game_members.user_id
WHERE game_members.game_id = $1 AND game_members.deleted_at IS NULL
ORDER BY (game_members.role = 'owner') DESC, game_members.created_at, users.name;

-- The owner cannot be removed: a game always has one.
-- name: RemoveGameMember :execrows
UPDATE game_members SET deleted_at = NOW(), updated_at = NOW()
WHERE game_id = $1 AND user_id = $2 AND role <> 'owner' AND deleted_at IS NULL;

-- ---- game invites -----------------------------------------------------------

-- name: CreateGameInvite :one
INSERT INTO game_invites (id, game_id, code_hash, note, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListGameInvites :many
SELECT * FROM game_invites
WHERE game_id = $1 AND used_at IS NULL AND deleted_at IS NULL AND expires_at > NOW()
ORDER BY created_at DESC;

-- An invite that can still be redeemed, with its game, for the join page.
-- name: GetGameInviteByHash :one
SELECT game_invites.*, games.name AS game_name, users.name AS inviter FROM game_invites
JOIN games ON games.id = game_invites.game_id AND games.deleted_at IS NULL
JOIN users ON users.id = game_invites.created_by
WHERE game_invites.code_hash = $1
  AND game_invites.used_at IS NULL AND game_invites.deleted_at IS NULL
  AND game_invites.expires_at > NOW();

-- Spends an invite in one UPDATE, so two people racing one link cannot
-- both join on it.
-- name: ClaimGameInvite :one
UPDATE game_invites SET used_at = NOW(), used_by = $2, updated_at = NOW()
WHERE code_hash = $1
  AND used_at IS NULL AND deleted_at IS NULL AND expires_at > NOW()
  AND game_id IN (SELECT id FROM games WHERE deleted_at IS NULL)
RETURNING *;

-- name: DeleteGameInvite :execrows
UPDATE game_invites SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND used_at IS NULL AND deleted_at IS NULL;

-- ---- Google sign-in ---------------------------------------------------------

-- name: CreateSSOLoginIntent :exec
INSERT INTO sso_login_intents (
    state, provider, return_to,
    client_id, redirect_uri, code_challenge, code_challenge_method, downstream_state, scope,
    invite_code, link_user_id, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: ConsumeSSOLoginIntent :one
DELETE FROM sso_login_intents
WHERE state = $1 AND expires_at > NOW()
RETURNING *;

-- name: PurgeExpiredSSOLoginIntents :exec
DELETE FROM sso_login_intents WHERE expires_at < NOW();
