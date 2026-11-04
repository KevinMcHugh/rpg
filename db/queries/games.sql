-- name: CreateGame :one
INSERT INTO games (id, key, name, ruleset, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetGame :one
SELECT * FROM games
WHERE id = $1 AND deleted_at IS NULL;

-- Games a command names, by id or key. A key may name several: keys are
-- labels, not identities.
-- name: FindGames :many
SELECT * FROM games
WHERE (id = $1 OR key = $1) AND deleted_at IS NULL
ORDER BY created_at, id;

-- name: ListGames :many
SELECT * FROM games
WHERE deleted_at IS NULL
ORDER BY created_at, id;

-- name: UpdateGame :one
UPDATE games SET
    key = COALESCE(sqlc.narg(key), key),
    name = COALESCE(sqlc.narg(name), name),
    ruleset = COALESCE(sqlc.narg(ruleset), ruleset),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- Its content stays, but nothing lists it any more.
-- name: DeleteGame :execrows
UPDATE games SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- The games a user belongs to, with their role in each.
-- name: ListGamesForUser :many
SELECT games.*, game_members.role FROM games
JOIN game_members ON game_members.game_id = games.id
    AND game_members.user_id = $1 AND game_members.deleted_at IS NULL
WHERE games.deleted_at IS NULL
ORDER BY games.created_at, games.id;

-- The game something belongs to, and the user's role in it ('' when they
-- are not a member). No row when the thing, or its game, does not exist.
-- name: GameAccess :one
WITH target AS (
    SELECT games.id AS game_id FROM games WHERE sqlc.arg(kind)::text = 'game' AND games.id = sqlc.arg(target_id) AND games.deleted_at IS NULL
    UNION ALL
    SELECT characters.game_id FROM characters WHERE sqlc.arg(kind)::text = 'character' AND characters.id = sqlc.arg(target_id) AND characters.deleted_at IS NULL
    UNION ALL
    SELECT conversations.game_id FROM conversations WHERE sqlc.arg(kind)::text = 'conversation' AND conversations.id = sqlc.arg(target_id) AND conversations.deleted_at IS NULL
    UNION ALL
    SELECT c.game_id FROM nodes n JOIN conversations c ON c.id = n.conversation_id
    WHERE sqlc.arg(kind)::text = 'node' AND n.id = sqlc.arg(target_id) AND n.deleted_at IS NULL
    UNION ALL
    SELECT c.game_id FROM choices ch JOIN nodes n ON n.id = ch.from_node_id JOIN conversations c ON c.id = n.conversation_id
    WHERE sqlc.arg(kind)::text = 'choice' AND ch.id = sqlc.arg(target_id) AND ch.deleted_at IS NULL
    UNION ALL
    SELECT flags.game_id FROM flags WHERE sqlc.arg(kind)::text = 'flag' AND flags.id = sqlc.arg(target_id) AND flags.deleted_at IS NULL
    UNION ALL
    SELECT skills.game_id FROM skills WHERE sqlc.arg(kind)::text = 'skill' AND skills.id = sqlc.arg(target_id) AND skills.deleted_at IS NULL
    UNION ALL
    SELECT quests.game_id FROM quests WHERE sqlc.arg(kind)::text = 'quest' AND quests.id = sqlc.arg(target_id) AND quests.deleted_at IS NULL
    UNION ALL
    SELECT q.game_id FROM quest_stages s JOIN quests q ON q.id = s.quest_id
    WHERE sqlc.arg(kind)::text = 'quest_stage' AND s.id = sqlc.arg(target_id) AND s.deleted_at IS NULL
    UNION ALL
    SELECT notes.game_id FROM notes WHERE sqlc.arg(kind)::text = 'note' AND notes.id = sqlc.arg(target_id) AND notes.deleted_at IS NULL
    UNION ALL
    SELECT game_invites.game_id FROM game_invites WHERE sqlc.arg(kind)::text = 'invite' AND game_invites.id = sqlc.arg(target_id) AND game_invites.deleted_at IS NULL
)
SELECT target.game_id::text AS game_id, COALESCE(game_members.role, '')::text AS role
FROM target
JOIN games ON games.id = target.game_id AND games.deleted_at IS NULL
LEFT JOIN game_members ON game_members.game_id = target.game_id
    AND game_members.user_id = sqlc.arg(user_id) AND game_members.deleted_at IS NULL
LIMIT 1;
