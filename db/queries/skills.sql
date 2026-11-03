-- name: CreateSkill :one
INSERT INTO skills (id, key, name, name_revision_id, initial_level, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSkill :one
SELECT * FROM skills
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListSkills :many
SELECT * FROM skills
WHERE deleted_at IS NULL
ORDER BY name, id;

-- A new name comes with its new revision.
-- name: UpdateSkill :one
UPDATE skills SET
    key = COALESCE(sqlc.narg(key), key),
    name = COALESCE(sqlc.narg(name), name),
    name_revision_id = COALESCE(sqlc.narg(name_revision_id), name_revision_id),
    initial_level = COALESCE(sqlc.narg(initial_level), initial_level),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteSkill :execrows
UPDATE skills SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
