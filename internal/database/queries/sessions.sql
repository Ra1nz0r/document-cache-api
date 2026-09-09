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

-- name: GetSessionUser :one
SELECT
    u.id,
    u.login
FROM sessions AS s
JOIN users AS u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash)
  AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token_hash = sqlc.arg(token_hash);