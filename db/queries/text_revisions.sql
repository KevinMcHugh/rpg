-- name: CreateTextRevision :one
INSERT INTO text_revisions (id, target_kind, target_id, field, body, author_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- Licensed text, imported by `server import`: it names its source and
-- license notice instead of a key.
-- name: CreateLicensedRevision :one
INSERT INTO text_revisions (id, target_kind, target_id, field, body, source, license_notice)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- Revisions with the kind and name of the key that wrote them, or the
-- source and notice of licensed text, for the build's provenance check.
-- name: ListTextRevisionsByIDs :many
SELECT text_revisions.id, text_revisions.target_kind, text_revisions.target_id,
       text_revisions.field, text_revisions.body,
       COALESCE(api_keys.kind, '')::text AS author_kind, COALESCE(api_keys.name, '')::text AS author_name,
       COALESCE(text_revisions.source, '')::text AS source,
       COALESCE(text_revisions.license_notice, '')::text AS license_notice
FROM text_revisions
LEFT JOIN api_keys ON api_keys.id = text_revisions.author_key
WHERE text_revisions.id = ANY(sqlc.arg(ids)::text[]);
