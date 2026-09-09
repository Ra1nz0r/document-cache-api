package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"document-cache-api/internal/service"

	"github.com/rs/zerolog/log"
)

type Handler struct {
	auth *service.AuthService
}

func New(auth *service.AuthService) *Handler {
	return &Handler{
		auth: auth,
	}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	// Для формы регистрации достаточно 16 КиБ.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration form")
		return
	}

	req := RegisterRequest{
		Token: r.PostForm.Get("token"),
		Login: r.PostForm.Get("login"),
		Pswd:  r.PostForm.Get("pswd"),
	}

	login, err := h.auth.Register(
		r.Context(),
		req.Token,
		req.Login,
		req.Pswd,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidAdminToken):
			writeError(w, http.StatusUnauthorized, err.Error())

		case errors.Is(err, service.ErrInvalidLogin),
			errors.Is(err, service.ErrInvalidPassword),
			errors.Is(err, service.ErrLoginTaken):
			writeError(w, http.StatusBadRequest, err.Error())

		default:
			log.Error().
				Err(err).
				Msg("failed to register user")

			writeError(
				w,
				http.StatusInternalServerError,
				"internal server error",
			)
		}

		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Response: RegisterResponse{
			Login: login,
		},
	})
}

func writeError(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, APIResponse{
		Error: &APIError{
			Code: status,
			Text: text,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, response APIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().
			Err(err).
			Msg("failed to write HTTP response")
	}
}
