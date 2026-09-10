-- name: CreateDocument :one
INSERT INTO documents (
    owner_id,
    name,
    mime,
    is_file,
    is_public,
    json_data,
    storage_key,
    size_bytes
)
VALUES (
    sqlc.arg(owner_id),
    sqlc.arg(name),
    sqlc.arg(mime),
    sqlc.arg(is_file),
    sqlc.arg(is_public),
    sqlc.narg(json_data),
    sqlc.narg(storage_key),
    sqlc.arg(size_bytes)
)
RETURNING id;

-- name: CreateDocumentGrant :exec
INSERT INTO document_grants (
    document_id,
    user_id
)
VALUES (
    sqlc.arg(document_id),
    sqlc.arg(user_id)
);

-- name: ListDocuments :many
SELECT
    d.id::text AS id,
    d.name,
    d.mime,
    d.is_file,
    d.is_public,
    d.created_at,
    ARRAY(
        SELECT granted_user.login
        FROM document_grants AS dg
        JOIN users AS granted_user ON granted_user.id = dg.user_id
        WHERE dg.document_id = d.id
        ORDER BY granted_user.login
    )::text[] AS grants
FROM documents AS d
JOIN users AS owner_user ON owner_user.id = d.owner_id
WHERE owner_user.login = sqlc.arg(owner_login)::text
  AND (
      d.owner_id = sqlc.arg(viewer_id)::uuid
      OR d.is_public
      OR EXISTS (
          SELECT 1
          FROM document_grants AS access_grant
          WHERE access_grant.document_id = d.id
            AND access_grant.user_id = sqlc.arg(viewer_id)::uuid
      )
  )
  AND (
      sqlc.arg(filter_key)::text = ''
      OR CASE sqlc.arg(filter_key)::text
          WHEN 'id' THEN d.id::text
          WHEN 'name' THEN d.name
          WHEN 'mime' THEN d.mime
          WHEN 'file' THEN d.is_file::text
          WHEN 'public' THEN d.is_public::text
      END = sqlc.arg(filter_value)::text
  )
ORDER BY d.name ASC, d.created_at ASC, d.id ASC
LIMIT sqlc.arg(result_limit)::integer;

-- name: GetDocument :one
SELECT
    d.id,
    d.mime,
    d.is_file,
    d.json_data,
    d.storage_key,
    (
        d.owner_id = sqlc.arg(viewer_id)::uuid
        OR d.is_public
        OR EXISTS (
            SELECT 1
            FROM document_grants dg
            WHERE dg.document_id = d.id
              AND dg.user_id = sqlc.arg(viewer_id)::uuid
        )
    )::boolean AS can_read
FROM documents d
WHERE d.id = sqlc.arg(document_id)::uuid;