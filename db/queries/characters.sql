-- name: CreateCharacter :one
INSERT INTO characters (id, name, name_revision_id, bible, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCharacter :one
SELECT * FROM characters
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCharacters :many
SELECT * FROM characters
WHERE deleted_at IS NULL
ORDER BY name, id;

-- A new name comes with its new revision.
-- name: UpdateCharacter :one
UPDATE characters SET
    name = COALESCE(sqlc.narg(name), name),
    name_revision_id = COALESCE(sqlc.narg(name_revision_id), name_revision_id),
    bible = COALESCE(sqlc.narg(bible), bible),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteCharacter :execrows
UPDATE characters SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
