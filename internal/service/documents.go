package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"document-cache-api/internal/database/sqlc"
	"document-cache-api/internal/storage"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

var ErrInvalidDocument = errors.New("invalid document")

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

func (s *DocumentService) Upload(
	ctx context.Context,
	owner SessionUser,
	input UploadDocumentInput,
) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDocument)
	}

	mediaType, _, err := mime.ParseMediaType(input.MIME)
	if err != nil || !strings.Contains(mediaType, "/") {
		return fmt.Errorf("%w: invalid MIME type", ErrInvalidDocument)
	}

	if len(input.JSON) > 0 && !json.Valid(input.JSON) {
		return fmt.Errorf("%w: invalid JSON", ErrInvalidDocument)
	}

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

	// Убираем повторяющиеся логины.
	logins := make([]string, 0, len(input.Grant))
	seen := make(map[string]struct{}, len(input.Grant))

	for _, login := range input.Grant {
		if _, exists := seen[login]; exists {
			continue
		}

		seen[login] = struct{}{}
		logins = append(logins, login)
	}

	// Все указанные получатели должны существовать.
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

	// До попытки коммита при ошибке можно удалить записанный файл.
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin document transaction: %w", err)
	}

	defer func() {
		// Для отката используем отдельный контекст:
		// контекст HTTP-запроса к этому моменту мог быть отменён.
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
