package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"document-cache-api/internal/service"

	"github.com/rs/zerolog/log"
)

func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	// Ограничиваем весь запрос, включая meta, JSON и файл.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize)

	// Файловые данные свыше 1 МиБ могут размещаться во временных файлах.
	err := r.ParseMultipartForm(1 << 20)

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

	// Здесь впервые используем проверку сессии перед операцией с документом.
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
