-- name: CreateQuest :one
INSERT INTO quests (id, game_id, key, name, name_revision_id, summary, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetQuest :one
SELECT * FROM quests
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListQuests :many
SELECT * FROM quests
WHERE game_id = $1 AND deleted_at IS NULL
ORDER BY name, id;

-- A new name comes with its new revision.
-- name: UpdateQuest :one
UPDATE quests SET
    key = COALESCE(sqlc.narg(key), key),
    name = COALESCE(sqlc.narg(name), name),
    name_revision_id = COALESCE(sqlc.narg(name_revision_id), name_revision_id),
    summary = COALESCE(sqlc.narg(summary), summary),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- Its stages go with it.
-- name: DeleteQuest :execrows
UPDATE quests SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: DeleteQuestStages :exec
UPDATE quest_stages SET deleted_at = NOW(), updated_at = NOW()
WHERE quest_id = $1 AND deleted_at IS NULL;

-- name: CreateQuestStage :one
INSERT INTO quest_stages (id, quest_id, number, journal, journal_revision_id, completes, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetQuestStage :one
SELECT * FROM quest_stages
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListQuestStages :many
SELECT * FROM quest_stages
WHERE quest_id = $1 AND deleted_at IS NULL
ORDER BY number;

-- Every live stage of every live quest in a game, for the build.
-- name: ListAllQuestStages :many
SELECT quest_stages.* FROM quest_stages
JOIN quests ON quests.id = quest_stages.quest_id AND quests.deleted_at IS NULL
WHERE quests.game_id = $1 AND quest_stages.deleted_at IS NULL
ORDER BY quest_stages.quest_id, quest_stages.number;

-- A new journal entry comes with its new revision.
-- name: UpdateQuestStage :one
UPDATE quest_stages SET
    number = COALESCE(sqlc.narg(number), number),
    journal = COALESCE(sqlc.narg(journal), journal),
    journal_revision_id = COALESCE(sqlc.narg(journal_revision_id), journal_revision_id),
    completes = COALESCE(sqlc.narg(completes), completes),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteQuestStage :execrows
UPDATE quest_stages SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- Up to lim rows after the sort key of the last row on the page before;
-- app.Store.PageQuests pages them.
-- name: QuestsAfter :many
SELECT * FROM quests
WHERE game_id = sqlc.arg(game_id) AND deleted_at IS NULL
  AND (sqlc.narg(after_id)::text IS NULL OR (name, id) > (sqlc.narg(after_name)::text, sqlc.narg(after_id)::text))
ORDER BY name, id
LIMIT sqlc.arg(lim);
