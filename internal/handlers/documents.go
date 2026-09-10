package handlers

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"document-cache-api/internal/cache"
	"document-cache-api/internal/service"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
)

// UploadDocument загружает новый документ.
func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	// Ограничиваем размер всего запроса, включая meta, JSON и файл.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize)

	// До 1 МиБ файловых данных держим в памяти, остальное уйдёт во временные файлы.
	err := r.ParseMultipartForm(1 << 20)

	// Удаляем временные файлы после обработки запроса.
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid multipart form or upload size limit exceeded",
		)
		return
	}

	form := r.MultipartForm

	// meta содержит параметры загружаемого документа.
	metaValues := form.Value["meta"]
	if len(metaValues) != 1 {
		writeError(w, http.StatusBadRequest, "exactly one meta field is required")
		return
	}

	var meta UploadDocumentMeta
	if err := json.Unmarshal([]byte(metaValues[0]), &meta); err != nil {
		writeError(w, http.StatusBadRequest, "invalid meta JSON")
		return
	}

	// Операции с документами доступны только с действующей сессией.
	user, err := h.auth.Authenticate(r.Context(), meta.Token)
	if err != nil {
		if errors.Is(err, service.ErrInvalidSession) {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}

		log.Error().Err(err).Msg("failed to authenticate upload")
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	input := service.UploadDocumentInput{
		Name:   meta.Name,
		MIME:   meta.MIME,
		IsFile: meta.File,
		Public: meta.Public,
		Grant:  meta.Grant,
	}

	// json необязателен, но если передан, проверяем его формат.
	jsonValues := form.Value["json"]
	if len(jsonValues) > 1 {
		writeError(w, http.StatusBadRequest, "only one json field is allowed")
		return
	}

	if len(jsonValues) == 1 {
		input.JSON = json.RawMessage(jsonValues[0])

		if !json.Valid(input.JSON) {
			writeError(w, http.StatusBadRequest, "invalid document JSON")
			return
		}
	}

	fileHeaders := form.File["file"]

	// Наличие файла должно соответствовать флагу meta.file.
	if meta.File {
		if len(fileHeaders) != 1 {
			writeError(w, http.StatusBadRequest, "exactly one file is required")
			return
		}

		file, err := fileHeaders[0].Open()
		if err != nil {
			log.Error().Err(err).Msg("failed to open multipart file")
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		defer file.Close()

		input.File = file
	} else if len(fileHeaders) > 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"file must be absent when file=false",
		)
		return
	}

	err = h.documents.Upload(r.Context(), user, input)

	// После изменения документов сбрасываем кеш списков.
	h.cache.Invalidate("")

	if err != nil {
		if errors.Is(err, service.ErrInvalidDocument) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		log.Error().Err(err).Msg("failed to upload document")
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	response := UploadDocumentResponse{
		JSON: input.JSON,
	}

	if meta.File {
		response.File = meta.Name
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Data: response,
	})
}

// ListDocuments возвращает список документов, доступных пользователю.
func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	// ParseQuery позволяет вернуть ошибку при некорректном query string.
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeErrorForRequest(
			w, r,
			http.StatusBadRequest,
			"invalid query parameters",
		)
		return
	}

	// Токен авторизации передаётся в query-параметрах.
	user, err := h.auth.Authenticate(r.Context(), params.Get("token"))
	if err != nil {
		if errors.Is(err, service.ErrInvalidSession) {
			writeErrorForRequest(
				w, r,
				http.StatusUnauthorized,
				err.Error(),
			)
			return
		}

		log.Error().Err(err).Msg("failed to authenticate document list")
		writeErrorForRequest(
			w, r,
			http.StatusInternalServerError,
			"internal server error",
		)
		return
	}

	// key и value используются только вместе.
	if params.Has("key") != params.Has("value") ||
		(params.Has("key") && params.Get("key") == "") {
		writeErrorForRequest(
			w, r,
			http.StatusBadRequest,
			"key and value must be provided together; key must not be empty",
		)
		return
	}

	// Если limit не указан, используем значение по умолчанию.
	limit := service.DefaultDocumentsLimit

	if params.Has("limit") {
		limit, err = strconv.Atoi(params.Get("limit"))
		if err != nil {
			writeErrorForRequest(
				w, r,
				http.StatusBadRequest,
				"limit must be an integer",
			)
			return
		}
	}

	// Токен в ключ кеша не добавляем:
	// разные сессии одного пользователя могут использовать один результат.
	cacheParams := make(url.Values)

	for _, name := range []string{"login", "key", "value", "limit"} {
		if params.Has(name) {
			cacheParams.Set(name, params.Get(name))
		}
	}

	cacheKey := cache.Key{
		UserID: hex.EncodeToString(user.ID.Bytes[:]),
		Query:  cacheParams.Encode(),
	}

	// При попадании в кеш до БД уже не доходим.
	cached, generation, hit := h.cache.Get(cacheKey)
	if hit {
		writeDocumentResponse(w, r, cached, true)
		return
	}

	docs, err := h.documents.List(
		r.Context(),
		user,
		service.ListDocumentsInput{
			Login: params.Get("login"),
			Key:   params.Get("key"),
			Value: params.Get("value"),
			Limit: limit,
		},
	)
	if err != nil {
		if errors.Is(err, service.ErrInvalidDocumentFilter) {
			writeErrorForRequest(
				w, r,
				http.StatusBadRequest,
				err.Error(),
			)
			return
		}

		log.Error().Err(err).Msg("failed to list documents")
		writeErrorForRequest(
			w, r,
			http.StatusInternalServerError,
			"internal server error",
		)
		return
	}

	// Пустой список должен сериализоваться как [], а не null.
	items := make([]DocumentListItem, 0, len(docs))

	for _, doc := range docs {
		grants := doc.Grants
		if grants == nil {
			grants = []string{}
		}

		items = append(items, DocumentListItem{
			ID:      doc.ID,
			Name:    doc.Name,
			MIME:    doc.Mime,
			File:    doc.IsFile,
			Public:  doc.IsPublic,
			Created: doc.CreatedAt.Time.UTC().Format("2006-01-02 15:04:05"),
			Grant:   grants,
		})
	}

	// Готовим ответ один раз, чтобы его можно было положить в кеш и сразу отправить.
	response, err := makeJSONResponse(APIResponse{
		Data: ListDocumentsResponse{
			Docs: items,
		},
	})
	if err != nil {
		log.Error().Err(err).Msg("failed to encode document list")
		writeErrorForRequest(
			w, r, http.StatusInternalServerError, "internal server error",
		)
		return
	}

	h.cache.Set(cacheKey, response, generation)
	writeDocumentResponse(w, r, response, false)
}

// GetDocument возвращает один документ по ID.
// Для HEAD отправляет только заголовки.
func (h *Handler) GetDocument(w http.ResponseWriter, r *http.Request) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeErrorForRequest(
			w, r, http.StatusBadRequest, "invalid query parameters",
		)
		return
	}

	// Для получения документа нужна действующая сессия.
	viewer, err := h.auth.Authenticate(r.Context(), params.Get("token"))
	if err != nil {
		if errors.Is(err, service.ErrInvalidSession) {
			writeErrorForRequest(
				w, r, http.StatusUnauthorized, err.Error(),
			)
			return
		}

		log.Error().Err(err).Msg("failed to authenticate session")
		writeErrorForRequest(
			w, r, http.StatusInternalServerError, "internal server error",
		)
		return
	}

	var id pgtype.UUID

	// Проверяем ID до формирования ключа кеша.
	if err := id.Scan(r.PathValue("id")); err != nil || !id.Valid {
		writeErrorForRequest(
			w, r, http.StatusBadRequest, service.ErrInvalidDocumentID.Error(),
		)
		return
	}

	// Кеш разделён по пользователю, так как доступ к документу может отличаться.
	cacheKey := cache.Key{
		UserID:     hex.EncodeToString(viewer.ID.Bytes[:]),
		DocumentID: hex.EncodeToString(id.Bytes[:]),
	}

	cached, generation, hit := h.cache.Get(cacheKey)
	if hit {
		writeDocumentResponse(w, r, cached, true)
		return
	}

	// Проверку доступа и получение содержимого оставляем service-слою.
	document, err := h.documents.Get(
		r.Context(),
		viewer,
		r.PathValue("id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidDocumentID):
			writeErrorForRequest(
				w, r, http.StatusBadRequest, err.Error(),
			)

		case errors.Is(err, service.ErrDocumentNotFound):
			writeErrorForRequest(
				w, r, http.StatusNotFound, err.Error(),
			)

		case errors.Is(err, service.ErrDocumentForbidden):
			writeErrorForRequest(
				w, r, http.StatusForbidden, err.Error(),
			)

		default:
			log.Error().Err(err).Msg("failed to get document")
			writeErrorForRequest(
				w, r, http.StatusInternalServerError, "internal server error",
			)
		}

		return
	}

	var response cache.Response

	// Файл отдаём как есть, JSON заворачиваем в общую модель ответа.
	if document.IsFile {
		response = cache.Response{
			ContentType: document.MIME,
			Body:        string(document.Data),
			NoSniff:     true,
		}
	} else {
		response, err = makeJSONResponse(APIResponse{
			Data: json.RawMessage(document.Data),
		})
		if err != nil {
			log.Error().Err(err).Msg("failed to encode document")
			writeErrorForRequest(
				w, r, http.StatusInternalServerError, "internal server error",
			)
			return
		}
	}

	h.cache.Set(cacheKey, response, generation)
	writeDocumentResponse(w, r, response, false)
}

// DeleteDocument удаляет документ текущего пользователя.
func (h *Handler) DeleteDocument(
	w http.ResponseWriter,
	r *http.Request,
) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeErrorForRequest(
			w, r, http.StatusBadRequest, "invalid query parameters",
		)
		return
	}

	// Для удаления документа нужна действующая сессия.
	viewer, err := h.auth.Authenticate(
		r.Context(),
		params.Get("token"),
	)
	if err != nil {
		if errors.Is(err, service.ErrInvalidSession) {
			writeErrorForRequest(
				w, r, http.StatusUnauthorized, err.Error(),
			)
			return
		}

		log.Error().Err(err).Msg("failed to authenticate session")
		writeErrorForRequest(
			w, r, http.StatusInternalServerError, "internal server error",
		)
		return
	}

	documentID := r.PathValue("id")

	var id pgtype.UUID

	// UUID нужен и для проверки ID, и для ключа кеша.
	if err := id.Scan(documentID); err != nil || !id.Valid {
		writeErrorForRequest(
			w, r, http.StatusBadRequest, service.ErrInvalidDocumentID.Error(),
		)
		return
	}

	err = h.documents.Delete(r.Context(), viewer, documentID)

	// Сбрасываем списки и кеш этого документа для всех пользователей.
	h.cache.Invalidate(hex.EncodeToString(id.Bytes[:]))

	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidDocumentID):
			writeErrorForRequest(
				w, r, http.StatusBadRequest, err.Error(),
			)

		case errors.Is(err, service.ErrDocumentNotFound):
			writeErrorForRequest(
				w, r, http.StatusNotFound, err.Error(),
			)

		case errors.Is(err, service.ErrDocumentForbidden):
			writeErrorForRequest(
				w, r, http.StatusForbidden, err.Error(),
			)

		default:
			log.Error().Err(err).Msg("failed to delete document")
			writeErrorForRequest(
				w, r, http.StatusInternalServerError, "internal server error",
			)
		}

		return
	}

	writeJSONForRequest(w, r, http.StatusOK, APIResponse{
		Response: map[string]bool{
			documentID: true,
		},
	})
}
