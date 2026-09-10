package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"document-cache-api/internal/service"

	"github.com/rs/zerolog/log"
)

// UploadDocument загружает новый документ.
func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	// Ограничиваем размер всего запроса, включая meta, JSON и файл.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize)

	// До 1 МиБ файловых данных ParseMultipartForm хранит в памяти,
	// остальное при необходимости записывает во временные файлы.
	err := r.ParseMultipartForm(1 << 20)

	// Удаляем временные файлы, созданные при разборе multipart.
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

	// meta должен содержать JSON с параметрами документа.
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

	// Все операции с документами требуют действующей сессии.
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

	// Поле json опциональное, но если передано, должно содержать корректный JSON.
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

	// Наличие multipart-поля file должно совпадать с meta.file.
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

	// Проверку данных и сохранение документа выполняет service-слой.
	if err := h.documents.Upload(r.Context(), user, input); err != nil {
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
	// ParseQuery позволяет отдельно обработать ошибку в query string.
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeErrorForRequest(
			w, r,
			http.StatusBadRequest,
			"invalid query parameters",
		)
		return
	}

	// Токен для GET/HEAD передаётся через query-параметры.
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

	// Фильтр работает только при одновременно переданных key и value.
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

	writeJSONForRequest(w, r, http.StatusOK, APIResponse{
		Data: ListDocumentsResponse{
			Docs: items,
		},
	})
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

	// Service проверяет ID документа и доступ текущего пользователя.
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

	// JSON-документ возвращаем через общую модель API.
	if !document.IsFile {
		writeJSONForRequest(w, r, http.StatusOK, APIResponse{
			Data: json.RawMessage(document.Data),
		})
		return
	}

	// Для файла отдаём сохранённый MIME и само содержимое без JSON-обёртки.
	w.Header().Set("Content-Type", document.MIME)
	w.Header().Set("Content-Length", strconv.Itoa(len(document.Data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	w.WriteHeader(http.StatusOK)

	// HEAD должен вернуть те же заголовки, но без содержимого файла.
	if r.Method == http.MethodHead {
		return
	}

	if _, err := w.Write(document.Data); err != nil {
		log.Error().Err(err).Msg("failed to write document response")
	}
}
