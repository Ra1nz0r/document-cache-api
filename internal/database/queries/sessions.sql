-- name: CreateSession :exec
INSERT INTO sessions (
    token_hash,
    user_id,
    expires_at
)
VALUES (
    sqlc.arg(token_hash),
    sqlc.arg(user_id),
    sqlc.arg(expires_at)
);