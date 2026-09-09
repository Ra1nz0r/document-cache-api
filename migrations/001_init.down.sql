-- Откат удаляет все документы, пользователей, сессии и grants из БД.
-- Файлы на диске эта миграция не удаляет.
BEGIN;

DROP TABLE document_grants;
DROP TABLE documents;
DROP TABLE sessions;
DROP TABLE users;

COMMIT;
