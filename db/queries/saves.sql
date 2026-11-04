-- name: CreateSave :one
INSERT INTO saves (id, game_id, name, state, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- Saves are private to whoever made them; the game binary has no keys, so
-- its saves all have a NULL created_by and match a NULL argument.
-- name: GetSave :one
SELECT * FROM saves
WHERE id = $1 AND created_by IS NOT DISTINCT FROM sqlc.narg(created_by) AND deleted_at IS NULL;

-- name: ListSaves :many
SELECT * FROM saves
WHERE created_by IS NOT DISTINCT FROM sqlc.narg(created_by)
  AND game_id IS NOT DISTINCT FROM sqlc.narg(game_id)
  AND deleted_at IS NULL
ORDER BY updated_at DESC
LIMIT 50;

-- name: UpdateSave :one
UPDATE saves SET state = $2, updated_at = NOW()
WHERE id = $1 AND created_by IS NOT DISTINCT FROM sqlc.narg(created_by) AND deleted_at IS NULL
RETURNING *;

-- name: DeleteSave :execrows
UPDATE saves SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND created_by IS NOT DISTINCT FROM sqlc.narg(created_by) AND deleted_at IS NULL;
