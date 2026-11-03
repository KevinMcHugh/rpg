-- name: CreateTextRevision :one
INSERT INTO text_revisions (id, target_kind, target_id, field, body, author_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- Revisions with the kind and name of the key that wrote them, for the
-- build's provenance check.
-- name: ListTextRevisionsByIDs :many
SELECT text_revisions.id, text_revisions.target_kind, text_revisions.target_id,
       text_revisions.field, text_revisions.body,
       api_keys.kind AS author_kind, api_keys.name AS author_name
FROM text_revisions
JOIN api_keys ON api_keys.id = text_revisions.author_key
WHERE text_revisions.id = ANY(sqlc.arg(ids)::text[]);
