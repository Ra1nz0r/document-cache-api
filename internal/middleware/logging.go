package middleware

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// responseWriter запоминает HTTP-статус, отправленный обработчиком.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}

	// Информационные ответы не являются окончательным статусом.
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}

	w.ResponseWriter.WriteHeader(status)
	w.status = status
}

func (w *responseWriter) Write(body []byte) (int, error) {
	// Запись тела без явного WriteHeader означает ответ 200.
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(body)
}

// Unwrap предоставляет доступ к исходному ResponseWriter
// при использовании http.ResponseController.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Logging записывает результат обработки HTTP-запроса.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		writer := &responseWriter{
			ResponseWriter: w,
		}

		next.ServeHTTP(writer, r)

		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}

		// ServeMux заполняет Pattern при выборе обработчика.
		// Полный URL не используем: он может содержать токен.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}

		event := log.Info()

		switch {
		case status >= http.StatusInternalServerError:
			event = log.Error()
		case status >= http.StatusBadRequest:
			event = log.Warn()
		}

		event.
			Str("method", r.Method).
			Str("route", route).
			Int("status", status).
			Float64(
				"duration_ms",
				float64(time.Since(start))/float64(time.Millisecond),
			)

		if cacheStatus := writer.Header().Get("X-Cache"); cacheStatus != "" {
			event.Str("cache", cacheStatus)
		}

		event.Msg("HTTP request completed")
	})
}
