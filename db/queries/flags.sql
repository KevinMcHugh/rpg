-- name: CreateFlag :one
INSERT INTO flags (id, key, description, initial_value, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetFlag :one
SELECT * FROM flags
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListFlags :many
SELECT * FROM flags
WHERE deleted_at IS NULL
ORDER BY key;

-- name: UpdateFlag :one
UPDATE flags SET
    key = COALESCE(sqlc.narg(key), key),
    description = COALESCE(sqlc.narg(description), description),
    initial_value = COALESCE(sqlc.narg(initial_value), initial_value),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteFlag :execrows
UPDATE flags SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
