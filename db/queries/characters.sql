-- name: CreateCharacter :one
INSERT INTO characters (id, game_id, key, name, name_revision_id, bible, companion, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetCharacter :one
SELECT * FROM characters
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCharacters :many
SELECT * FROM characters
WHERE game_id = $1 AND deleted_at IS NULL
ORDER BY name, id;

-- A new name comes with its new revision.
-- name: UpdateCharacter :one
UPDATE characters SET
    name = COALESCE(sqlc.narg(name), name),
    name_revision_id = COALESCE(sqlc.narg(name_revision_id), name_revision_id),
    bible = COALESCE(sqlc.narg(bible), bible),
    key = COALESCE(sqlc.narg(key), key),
    companion = COALESCE(sqlc.narg(companion), companion),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteCharacter :execrows
UPDATE characters SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- Up to lim rows after the sort key of the last row on the page before;
-- app.Store.PageCharacters pages them.
-- name: CharactersAfter :many
SELECT * FROM characters
WHERE game_id = sqlc.arg(game_id) AND deleted_at IS NULL
  AND (sqlc.narg(after_id)::text IS NULL OR (name, id) > (sqlc.narg(after_name)::text, sqlc.narg(after_id)::text))
ORDER BY name, id
LIMIT sqlc.arg(lim);
