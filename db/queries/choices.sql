-- name: CreateChoice :one
INSERT INTO choices (id, from_node_id, to_node_id, text, text_revision_id, sort_order, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetChoice :one
SELECT * FROM choices
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListChoicesByConversation :many
SELECT choices.* FROM choices
JOIN nodes ON nodes.id = choices.from_node_id AND nodes.deleted_at IS NULL
WHERE nodes.conversation_id = $1 AND choices.deleted_at IS NULL
ORDER BY choices.from_node_id, choices.sort_order, choices.created_at, choices.id;

-- Every live choice from a live node of a live conversation, for the
-- build. A choice whose target was deleted is still listed, so the build
-- can report it.
-- name: ListAllChoices :many
SELECT choices.* FROM choices
JOIN nodes ON nodes.id = choices.from_node_id AND nodes.deleted_at IS NULL
JOIN conversations ON conversations.id = nodes.conversation_id AND conversations.deleted_at IS NULL
WHERE choices.deleted_at IS NULL
ORDER BY choices.from_node_id, choices.sort_order, choices.created_at, choices.id;

-- set_target says whether to change to_node_id, so a PATCH can make a
-- choice end the conversation. A new text comes with its new revision.
-- name: UpdateChoice :one
UPDATE choices SET
    to_node_id = CASE WHEN sqlc.arg(set_target)::bool THEN sqlc.narg(to_node_id) ELSE to_node_id END,
    text = COALESCE(sqlc.narg(text), text),
    text_revision_id = COALESCE(sqlc.narg(text_revision_id), text_revision_id),
    sort_order = COALESCE(sqlc.narg(sort_order), sort_order),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteChoice :execrows
UPDATE choices SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- A deleted node takes the choices it offered with it.
-- name: DeleteChoicesFromNode :exec
UPDATE choices SET deleted_at = NOW(), updated_at = NOW()
WHERE from_node_id = $1 AND deleted_at IS NULL;
