package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"document-cache-api/internal/cache"

	"github.com/rs/zerolog/log"
)

// makeJSONResponse сериализует JSON один раз перед сохранением в кеш.
func makeJSONResponse(value APIResponse) (cache.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return cache.Response{}, err
	}

	body = append(body, '\n')

	return cache.Response{
		ContentType: "application/json; charset=utf-8",
		Body:        string(body),
	}, nil
}

// writeDocumentResponse отправляет готовый успешный ответ.
// Для HEAD тело не отправляется, но Content-Length сохраняется.
func writeDocumentResponse(
	w http.ResponseWriter,
	r *http.Request,
	response cache.Response,
	cacheHit bool,
) {
	w.Header().Set("Content-Type", response.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(response.Body)))
	w.Header().Set("Cache-Control", "no-store")

	if response.NoSniff {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}

	if cacheHit {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}

	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodHead {
		return
	}

	if _, err := io.WriteString(w, response.Body); err != nil {
		log.Error().Err(err).Msg("failed to write document response")
	}
}
