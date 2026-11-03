-- name: CreateConversation :one
INSERT INTO conversations (id, title, summary, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetConversation :one
SELECT * FROM conversations
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListConversations :many
SELECT * FROM conversations
WHERE deleted_at IS NULL
ORDER BY created_at DESC;

-- set_start says whether to change start_node_id, so a PATCH can clear it.
-- name: UpdateConversation :one
UPDATE conversations SET
    title = COALESCE(sqlc.narg(title), title),
    summary = COALESCE(sqlc.narg(summary), summary),
    start_node_id = CASE WHEN sqlc.arg(set_start)::bool THEN sqlc.narg(start_node_id) ELSE start_node_id END,
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- Its nodes and choices stay behind but no longer ship: the build reads
-- only live conversations.
-- name: DeleteConversation :execrows
UPDATE conversations SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
