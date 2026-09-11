package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"
)

// writeJSONForRequest пишет JSON-ответ.
// Для HEAD формирует те же заголовки, но не отправляет body.
func writeJSONForRequest(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	response APIResponse,
) {
	// Сериализуем заранее, чтобы знать размер ответа для Content-Length.
	body, err := json.Marshal(response)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal HTTP response")

		status = http.StatusInternalServerError
		body = []byte(
			`{"error":{"code":500,"text":"internal server error"}}`,
		)
	}

	// Добавляем перевод строки, как это делает json.Encoder.
	body = append(body, '\n')

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))

	// Ответ может зависеть от пользователя, поэтому внешнее кеширование отключаем.
	w.Header().Set("Cache-Control", "no-store")

	w.WriteHeader(status)

	// HEAD должен вернуть те же заголовки, что и GET, но без тела ответа.
	if r.Method == http.MethodHead {
		return
	}

	if _, err := w.Write(body); err != nil {
		log.Error().Err(err).Msg("failed to write HTTP response")
	}
}

// writeErrorForRequest формирует JSON-ошибку с учётом HEAD-запроса.
func writeErrorForRequest(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	text string,
) {
	writeJSONForRequest(w, r, status, APIResponse{
		Error: &APIError{
			Code: status,
			Text: text,
		},
	})
}

// MethodNotAllowed возвращает обработчик неподдерживаемых HTTP-методов.
func MethodNotAllowed(allowed string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowed)

		writeErrorForRequest(
			w,
			r,
			http.StatusMethodNotAllowed,
			"method not allowed",
		)
	}
}
