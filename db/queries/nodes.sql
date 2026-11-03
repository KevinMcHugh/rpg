-- name: CreateNode :one
INSERT INTO nodes (id, conversation_id, speaker_id, text, text_revision_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListNodesByConversation :many
SELECT * FROM nodes
WHERE conversation_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- Every live node of every live conversation, for the build.
-- name: ListAllNodes :many
SELECT nodes.* FROM nodes
JOIN conversations ON conversations.id = nodes.conversation_id AND conversations.deleted_at IS NULL
WHERE nodes.deleted_at IS NULL
ORDER BY nodes.conversation_id, nodes.created_at, nodes.id;

-- set_speaker says whether to change speaker_id, so a PATCH can make a
-- line the narrator's. A new text comes with its new revision.
-- name: UpdateNode :one
UPDATE nodes SET
    speaker_id = CASE WHEN sqlc.arg(set_speaker)::bool THEN sqlc.narg(speaker_id) ELSE speaker_id END,
    text = COALESCE(sqlc.narg(text), text),
    text_revision_id = COALESCE(sqlc.narg(text_revision_id), text_revision_id),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteNode :execrows
UPDATE nodes SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
