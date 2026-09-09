-- name: CreateUser :one
INSERT INTO users (
    login,
    password_hash
)
VALUES (
    sqlc.arg(login),
    sqlc.arg(password_hash)
)
RETURNING id, login, created_at;