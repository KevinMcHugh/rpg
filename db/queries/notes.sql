-- name: CreateNote :one
INSERT INTO notes (id, game_id, target_kind, target_id, kind, body, parent_id, by_human, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- A note with the name of the key that wrote it.
-- name: GetNote :one
SELECT sqlc.embed(notes), api_keys.name AS author_name
FROM notes
LEFT JOIN api_keys ON api_keys.id = notes.created_by
WHERE notes.id = $1 AND notes.deleted_at IS NULL;

-- Filters left NULL match everything. Oldest first, so a thread reads in
-- order.
-- name: ListNotes :many
SELECT sqlc.embed(notes), api_keys.name AS author_name
FROM notes
LEFT JOIN api_keys ON api_keys.id = notes.created_by
WHERE notes.game_id = sqlc.arg(game_id) AND notes.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR notes.status = sqlc.narg(status))
  AND (sqlc.narg(target_kind)::text IS NULL OR notes.target_kind = sqlc.narg(target_kind))
  AND (sqlc.narg(target_id)::text IS NULL OR notes.target_id = sqlc.narg(target_id))
ORDER BY notes.created_at, notes.id;

-- name: CountOpenNotes :one
SELECT COUNT(*) FROM notes
WHERE game_id = $1 AND status = 'open' AND parent_id IS NULL AND deleted_at IS NULL;

-- name: SetNoteStatus :one
UPDATE notes SET status = $2, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteNote :execrows
UPDATE notes SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: PageNotes :many
SELECT sqlc.embed(notes), api_keys.name AS author_name
FROM notes
LEFT JOIN api_keys ON api_keys.id = notes.created_by
WHERE notes.game_id = sqlc.arg(game_id) AND notes.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR notes.status = sqlc.narg(status))
  AND (sqlc.narg(target_kind)::text IS NULL OR notes.target_kind = sqlc.narg(target_kind))
  AND (sqlc.narg(target_id)::text IS NULL OR notes.target_id = sqlc.narg(target_id))
  AND (sqlc.narg(after_id)::text IS NULL OR (notes.created_at, notes.id) > (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::text))
ORDER BY notes.created_at, notes.id
LIMIT sqlc.arg(lim);
