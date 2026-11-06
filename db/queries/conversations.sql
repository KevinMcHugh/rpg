-- The first conversation opens the game, as the first line of a
-- conversation becomes its start.
-- name: CreateConversation :one
INSERT INTO conversations (id, game_id, key, title, summary, opening, created_by)
VALUES (sqlc.arg(id), sqlc.arg(game_id), sqlc.arg(key), sqlc.arg(title), sqlc.arg(summary),
    NOT EXISTS (SELECT 1 FROM conversations c WHERE c.game_id = sqlc.arg(game_id) AND c.opening AND c.deleted_at IS NULL),
    sqlc.arg(created_by))
RETURNING *;

-- name: GetConversation :one
SELECT * FROM conversations
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListConversations :many
SELECT * FROM conversations
WHERE game_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- set_start and set_condition say whether to change those columns, so a
-- PATCH can clear them. A new label comes with its new revision.
-- name: UpdateConversation :one
UPDATE conversations SET
    title = COALESCE(sqlc.narg(title), title),
    summary = COALESCE(sqlc.narg(summary), summary),
    start_node_id = CASE WHEN sqlc.arg(set_start)::bool THEN sqlc.narg(start_node_id) ELSE start_node_id END,
    key = COALESCE(sqlc.narg(key), key),
    label = COALESCE(sqlc.narg(label), label),
    label_revision_id = CASE WHEN sqlc.narg(label)::text IS NULL THEN label_revision_id ELSE sqlc.narg(label_revision_id) END,
    condition = CASE WHEN sqlc.arg(set_condition)::bool THEN sqlc.narg(condition)::jsonb ELSE condition END,
    repeatable = COALESCE(sqlc.narg(repeatable), repeatable),
    opening = COALESCE(sqlc.narg(opening), opening),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- Its nodes and choices stay behind but no longer ship: the build reads
-- only live conversations.
-- name: DeleteConversation :execrows
UPDATE conversations SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- Only one conversation opens a game.
-- name: ClearOpening :exec
UPDATE conversations SET opening = FALSE, updated_at = NOW()
WHERE game_id = $2 AND opening AND id <> $1 AND deleted_at IS NULL;

-- Up to lim rows after the sort key of the last row on the page before;
-- app.Store.PageConversations pages them.
-- name: ConversationsAfter :many
SELECT * FROM conversations
WHERE game_id = sqlc.arg(game_id) AND deleted_at IS NULL
  AND (sqlc.narg(after_id)::text IS NULL OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::text))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim);
