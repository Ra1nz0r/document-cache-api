BEGIN;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    login TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Храним SHA-256-хеш session token, а не сам токен.
CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_expiration CHECK (expires_at > created_at)
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    mime TEXT NOT NULL,
    is_file BOOLEAN NOT NULL,
    is_public BOOLEAN NOT NULL DEFAULT false,
    json_data JSONB,
    storage_key TEXT UNIQUE,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT documents_name_not_empty CHECK (length(btrim(name)) > 0),
    CONSTRAINT documents_size_nonnegative CHECK (size_bytes >= 0),
    -- Файл может содержать дополнительные JSON-данные.
    -- JSON-документ не имеет файла на диске.
    CONSTRAINT documents_content CHECK (
        (is_file AND storage_key IS NOT NULL AND length(storage_key) > 0)
        OR
        (NOT is_file AND storage_key IS NULL AND json_data IS NOT NULL)
    )
);

CREATE INDEX documents_owner_name_created_idx
    ON documents (owner_id, name, created_at, id);

-- Один пользователь получает доступ к документу только один раз.
CREATE TABLE document_grants (
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (document_id, user_id)
);

COMMIT;
