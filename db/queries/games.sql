-- name: CreateGame :one
INSERT INTO games (id, key, name, ruleset, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetGame :one
SELECT * FROM games
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetGameByKey :one
SELECT * FROM games
WHERE key = $1 AND deleted_at IS NULL;

-- name: ListGames :many
SELECT * FROM games
WHERE deleted_at IS NULL
ORDER BY created_at, id;

-- name: UpdateGame :one
UPDATE games SET
    key = COALESCE(sqlc.narg(key), key),
    name = COALESCE(sqlc.narg(name), name),
    ruleset = COALESCE(sqlc.narg(ruleset), ruleset),
    updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- Its content stays, but nothing lists it any more.
-- name: DeleteGame :execrows
UPDATE games SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
