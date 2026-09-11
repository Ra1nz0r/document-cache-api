package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"

	"document-cache-api/internal/database/sqlc"
	"document-cache-api/internal/storage"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

var (
	ErrInvalidDocument       = errors.New("invalid document")        // Ошибка при создании документа: некорректные данные.
	ErrInvalidDocumentFilter = errors.New("invalid document filter") // Ошибка при фильтрации документов: некорректные key/value.
	ErrInvalidDocumentID     = errors.New("invalid document id")     // Ошибка при получении/удалении документа: некорректный UUID.
	ErrDocumentNotFound      = errors.New("document not found")      // Ошибка при получении/удалении документа: документа нет в БД или у пользователя нет доступа.
	ErrDocumentForbidden     = errors.New("document access denied")  // Ошибка при получении/удалении документа: у пользователя нет доступа к документу.
)

const (
	DefaultDocumentsLimit = 100
	MaxDocumentsLimit     = 1000
)

// UploadDocumentInput содержит данные для создания документа.
type UploadDocumentInput struct {
	Name   string
	MIME   string
	IsFile bool
	Public bool
	Grant  []string
	JSON   json.RawMessage
	File   io.Reader
}

type DocumentService struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
	files   *storage.FileStorage
}

func NewDocumentService(
	pool *pgxpool.Pool,
	queries *sqlc.Queries,
	files *storage.FileStorage,
) *DocumentService {
	return &DocumentService{
		pool:    pool,
		queries: queries,
		files:   files,
	}
}

// Upload проверяет данные и сохраняет новый документ.
func (s *DocumentService) Upload(
	ctx context.Context,
	owner SessionUser,
	input UploadDocumentInput,
) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDocument)
	}

	// Проверяем, что передан корректный MIME type.
	mediaType, _, err := mime.ParseMediaType(input.MIME)
	if err != nil || !strings.Contains(mediaType, "/") {
		return fmt.Errorf("%w: invalid MIME type", ErrInvalidDocument)
	}

	if len(input.JSON) > 0 && !json.Valid(input.JSON) {
		return fmt.Errorf("%w: invalid JSON", ErrInvalidDocument)
	}

	// Для файлового документа нужен файл.
	// Для обычного документа нужен JSON и не должно быть файла.
	if input.IsFile {
		if input.File == nil {
			return fmt.Errorf("%w: file is required", ErrInvalidDocument)
		}
	} else {
		if input.File != nil {
			return fmt.Errorf(
				"%w: file must be absent when file=false",
				ErrInvalidDocument,
			)
		}

		if len(input.JSON) == 0 {
			return fmt.Errorf("%w: JSON is required", ErrInvalidDocument)
		}
	}

	// Убираем повторяющиеся логины из grant.
	logins := make([]string, 0, len(input.Grant))
	seen := make(map[string]struct{}, len(input.Grant))

	for _, login := range input.Grant {
		if _, exists := seen[login]; exists {
			continue
		}

		seen[login] = struct{}{}
		logins = append(logins, login)
	}

	// Нельзя выдать доступ пользователю, которого нет в БД.
	grantUsers, err := s.queries.GetUsersByLogins(ctx, logins)
	if err != nil {
		return fmt.Errorf("get grant users: %w", err)
	}

	if len(grantUsers) != len(logins) {
		return fmt.Errorf(
			"%w: grant contains unknown users",
			ErrInvalidDocument,
		)
	}

	var storageKey pgtype.Text
	size := int64(len(input.JSON))

	// Файл сохраняем отдельно, в БД будет лежать только ключ до него.
	if input.IsFile {
		key, fileSize, err := s.files.Save(input.File)
		if err != nil {
			return err
		}

		storageKey = pgtype.Text{
			String: key,
			Valid:  true,
		}
		size = fileSize
	}

	// Если ошибка произошла до Commit, удаляем уже сохранённый файл.
	// После начала Commit его результат может быть неоднозначным, поэтому файл здесь уже не трогаем.
	commitStarted := false

	defer func() {
		if storageKey.Valid && !commitStarted {
			if err := s.files.Delete(storageKey.String); err != nil {
				log.Error().
					Err(err).
					Msg("failed to clean up uploaded file")
			}
		}
	}()

	// Документ и его grants сохраняем в одной транзакции.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin document transaction: %w", err)
	}

	defer func() {
		// HTTP-контекст к этому моменту уже может быть отменён,
		// поэтому для rollback используем отдельный контекст.
		rollbackCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_ = tx.Rollback(rollbackCtx)
	}()

	qtx := s.queries.WithTx(tx)

	documentID, err := qtx.CreateDocument(ctx, sqlc.CreateDocumentParams{
		OwnerID:    owner.ID,
		Name:       input.Name,
		Mime:       input.MIME,
		IsFile:     input.IsFile,
		IsPublic:   input.Public,
		JsonData:   input.JSON,
		StorageKey: storageKey,
		SizeBytes:  size,
	})
	if err != nil {
		return fmt.Errorf("create document: %w", err)
	}

	for _, user := range grantUsers {
		if err := qtx.CreateDocumentGrant(
			ctx,
			sqlc.CreateDocumentGrantParams{
				DocumentID: documentID,
				UserID:     user.ID,
			},
		); err != nil {
			return fmt.Errorf("create document grant: %w", err)
		}
	}

	commitStarted = true

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit document transaction: %w", err)
	}

	return nil
}

// ListDocumentsInput содержит параметры получения списка документов.
type ListDocumentsInput struct {
	Login string
	Key   string
	Value string
	Limit int
}

// List возвращает доступные пользователю документы выбранного владельца.
func (s *DocumentService) List(
	ctx context.Context,
	viewer SessionUser,
	input ListDocumentsInput,
) ([]sqlc.ListDocumentsRow, error) {
	// Если login не передан, показываем документы самого пользователя.
	if input.Login == "" {
		input.Login = viewer.Login
	}

	if input.Limit < 1 || input.Limit > MaxDocumentsLimit {
		return nil, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			ErrInvalidDocumentFilter,
			MaxDocumentsLimit,
		)
	}

	// Проверяем значение фильтра в зависимости от выбранного key.
	switch input.Key {
	case "":
		if input.Value != "" {
			return nil, fmt.Errorf(
				"%w: value requires key",
				ErrInvalidDocumentFilter,
			)
		}

	case "id":
		var id pgtype.UUID
		if err := id.Scan(input.Value); err != nil || !id.Valid {
			return nil, fmt.Errorf(
				"%w: invalid document ID",
				ErrInvalidDocumentFilter,
			)
		}

		// Приводим UUID к строковому формату PostgreSQL.
		value, err := id.Value()
		if err != nil {
			return nil, fmt.Errorf("format document ID: %w", err)
		}
		input.Value = value.(string)

	case "name", "mime":
		// Для строковых полей дополнительных преобразований не нужно.

	case "file", "public":
		// file и public принимают только boolean значения.
		value, err := strconv.ParseBool(input.Value)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s must be a boolean",
				ErrInvalidDocumentFilter,
				input.Key,
			)
		}

		input.Value = strconv.FormatBool(value)

	default:
		return nil, fmt.Errorf(
			"%w: unsupported key",
			ErrInvalidDocumentFilter,
		)
	}

	docs, err := s.queries.ListDocuments(ctx, sqlc.ListDocumentsParams{
		OwnerLogin:  input.Login,
		ViewerID:    viewer.ID,
		FilterKey:   input.Key,
		FilterValue: input.Value,
		ResultLimit: int32(input.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	return docs, nil
}

// DocumentContent содержит данные документа для выдачи клиенту.
type DocumentContent struct {
	MIME   string
	IsFile bool
	Data   []byte
}

// Get получает документ и проверяет доступ текущего пользователя.
func (s *DocumentService) Get(
	ctx context.Context,
	viewer SessionUser,
	documentID string,
) (DocumentContent, error) {
	var id pgtype.UUID

	// Проверяем, что ID документа имеет корректный UUID-формат.
	if err := id.Scan(documentID); err != nil || !id.Valid {
		return DocumentContent{}, ErrInvalidDocumentID
	}

	document, err := s.queries.GetDocument(ctx, sqlc.GetDocumentParams{
		DocumentID: id,
		ViewerID:   viewer.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DocumentContent{}, ErrDocumentNotFound
		}

		return DocumentContent{}, fmt.Errorf("get document: %w", err)
	}

	// CanRead учитывает владельца, public и выданные grant.
	if !document.CanRead {
		return DocumentContent{}, ErrDocumentForbidden
	}

	content := DocumentContent{
		MIME:   document.Mime,
		IsFile: document.IsFile,
	}

	// Для JSON-документа содержимое уже хранится в БД.
	if !document.IsFile {
		content.Data = document.JsonData
		return content, nil
	}

	// Для файла в БД должен быть ключ до файла в storage.
	if !document.StorageKey.Valid || document.StorageKey.String == "" {
		return DocumentContent{}, fmt.Errorf(
			"file document has no storage key",
		)
	}

	data, err := s.files.Read(document.StorageKey.String)
	if err != nil {
		return DocumentContent{}, fmt.Errorf("read document content: %w", err)
	}

	content.Data = data
	return content, nil
}

// Delete удаляет документ владельца и связанный с ним файл.
func (s *DocumentService) Delete(
	ctx context.Context,
	viewer SessionUser,
	documentID string,
) error {
	var id pgtype.UUID

	// Проверяем формат ID до обращения в БД.
	if err := id.Scan(documentID); err != nil || !id.Valid {
		return ErrInvalidDocumentID
	}

	storageKey, err := s.queries.DeleteDocument(
		ctx,
		sqlc.DeleteDocumentParams{
			DocumentID: id,
			OwnerID:    viewer.ID,
		},
	)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("delete document: %w", err)
		}

		// Если удалить не получилось, проверяем причину, документа нет или текущий пользователь не владелец.
		_, lookupErr := s.queries.GetDocument(
			ctx,
			sqlc.GetDocumentParams{
				DocumentID: id,
				ViewerID:   viewer.ID,
			},
		)
		if lookupErr != nil {
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return ErrDocumentNotFound
			}

			return fmt.Errorf(
				"check document after unsuccessful delete: %w",
				lookupErr,
			)
		}

		return ErrDocumentForbidden
	}

	// Для JSON-документа удалять из файлового хранилища нечего.
	if !storageKey.Valid {
		return nil
	}

	if err := s.files.Delete(storageKey.String); err != nil {
		// Запись в БД уже удалена, поэтому ошибку очистки файла только логируем.
		log.Error().
			Err(err).
			Str("document_id", documentID).
			Str("storage_key", storageKey.String).
			Msg("failed to remove file after document deletion")
	}

	return nil
}
