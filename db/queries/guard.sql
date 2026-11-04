-- LockForWrite locks the live row kind/id names until the transaction ends
-- and returns when it last changed, for app.Guard to compare with what the
-- caller last saw. FOR UPDATE is not allowed beside UNION, so each kind
-- locks in its own CTE, and only the one whose kind matches reads a row.
-- A new kind that takes a guard gets a CTE here and an arm below.

-- name: LockForWrite :one
WITH
g AS (SELECT games.updated_at FROM games WHERE sqlc.arg(kind)::text = 'game' AND games.id = sqlc.arg(target_id) AND games.deleted_at IS NULL FOR UPDATE),
ch AS (SELECT characters.updated_at FROM characters WHERE sqlc.arg(kind)::text = 'character' AND characters.id = sqlc.arg(target_id) AND characters.deleted_at IS NULL FOR UPDATE),
cv AS (SELECT conversations.updated_at FROM conversations WHERE sqlc.arg(kind)::text = 'conversation' AND conversations.id = sqlc.arg(target_id) AND conversations.deleted_at IS NULL FOR UPDATE),
n AS (SELECT nodes.updated_at FROM nodes WHERE sqlc.arg(kind)::text = 'node' AND nodes.id = sqlc.arg(target_id) AND nodes.deleted_at IS NULL FOR UPDATE),
c AS (SELECT choices.updated_at FROM choices WHERE sqlc.arg(kind)::text = 'choice' AND choices.id = sqlc.arg(target_id) AND choices.deleted_at IS NULL FOR UPDATE),
f AS (SELECT flags.updated_at FROM flags WHERE sqlc.arg(kind)::text = 'flag' AND flags.id = sqlc.arg(target_id) AND flags.deleted_at IS NULL FOR UPDATE),
sk AS (SELECT skills.updated_at FROM skills WHERE sqlc.arg(kind)::text = 'skill' AND skills.id = sqlc.arg(target_id) AND skills.deleted_at IS NULL FOR UPDATE),
q AS (SELECT quests.updated_at FROM quests WHERE sqlc.arg(kind)::text = 'quest' AND quests.id = sqlc.arg(target_id) AND quests.deleted_at IS NULL FOR UPDATE),
qs AS (SELECT quest_stages.updated_at FROM quest_stages WHERE sqlc.arg(kind)::text = 'quest_stage' AND quest_stages.id = sqlc.arg(target_id) AND quest_stages.deleted_at IS NULL FOR UPDATE),
nt AS (SELECT notes.updated_at FROM notes WHERE sqlc.arg(kind)::text = 'note' AND notes.id = sqlc.arg(target_id) AND notes.deleted_at IS NULL FOR UPDATE)
SELECT updated_at::timestamptz AS updated_at FROM (
    SELECT updated_at FROM g UNION ALL SELECT updated_at FROM ch UNION ALL SELECT updated_at FROM cv
    UNION ALL SELECT updated_at FROM n UNION ALL SELECT updated_at FROM c UNION ALL SELECT updated_at FROM f
    UNION ALL SELECT updated_at FROM sk UNION ALL SELECT updated_at FROM q UNION ALL SELECT updated_at FROM qs
    UNION ALL SELECT updated_at FROM nt
) AS target
LIMIT 1;
