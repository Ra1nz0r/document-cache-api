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

-- name: GetUserByLogin :one
SELECT id, login, password_hash
FROM users
WHERE login = sqlc.arg(login);