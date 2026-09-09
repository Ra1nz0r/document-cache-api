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