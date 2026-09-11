package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"document-cache-api/internal/cache"
	"document-cache-api/internal/service"

	"github.com/rs/zerolog/log"
)

type Handler struct {
	auth          *service.AuthService
	documents     *service.DocumentService
	maxUploadSize int64
	cache         *cache.Cache
}

func New(
	auth *service.AuthService,
	documents *service.DocumentService,
	maxUploadSize int64,
	responseCache *cache.Cache,
) *Handler {
	return &Handler{
		auth:          auth,
		documents:     documents,
		maxUploadSize: maxUploadSize,
		cache:         responseCache,
	}
}

// Register обрабатывает HTTP-запрос на регистрацию нового пользователя.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	// Ограничиваем размер тела запроса.
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

	// Проверяем входные данные и регистрируем пользователя, возвращаем логин.
	login, err := h.auth.Register(
		r.Context(),
		req.Token,
		req.Login,
		req.Pswd,
	)
	if err != nil {
		// Ожидаемые ошибки сервиса переводим в подходящие HTTP-статусы.
		switch {
		case errors.Is(err, service.ErrInvalidAdminToken):
			writeError(w, http.StatusUnauthorized, err.Error())

		case errors.Is(err, service.ErrInvalidLogin),
			errors.Is(err, service.ErrInvalidPassword),
			errors.Is(err, service.ErrLoginTaken):
			writeError(w, http.StatusBadRequest, err.Error())

		default:
			// Неизвестную внутреннюю ошибку логируем, не возвращаем её детали клиенту.
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

// Auth обрабатывает аутентификацию пользователя и создаёт новую сессию.
func (h *Handler) Auth(w http.ResponseWriter, r *http.Request) {
	// Для формы с логином и паролем большого тела запроса не требуется.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid authentication form")
		return
	}

	req := AuthRequest{
		Login: r.PostForm.Get("login"),
		Pswd:  r.PostForm.Get("pswd"),
	}

	// Оба параметра обязательны для выполнения аутентификации.
	if req.Login == "" || req.Pswd == "" {
		writeError(w, http.StatusBadRequest, "login and pswd are required")
		return
	}

	// Проверяем логин и пароль, создаём сессию и получаем токен.
	token, err := h.auth.Login(r.Context(), req.Login, req.Pswd)
	if err != nil {
		// Для неправильного логина или пароля, возвращаем ошибку авторизации.
		if errors.Is(err, service.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}

		// Детали внутренней ошибки сохраняем только в логах.
		log.Error().
			Err(err).
			Msg("failed to authenticate user")

		writeError(
			w,
			http.StatusInternalServerError,
			"internal server error",
		)
		return
	}

	// Ответ содержит токен авторизации, запрещаем его кеширование.
	w.Header().Set("Cache-Control", "no-store")

	writeJSON(w, http.StatusOK, APIResponse{
		Response: AuthResponse{
			Token: token,
		},
	})
}

// Logout завершает авторизованную сессию по токену из URL.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// Получаем токен из пути /api/auth/{token}.
	token := r.PathValue("token")

	// Завершаем сессию по переданному токену.
	if err := h.auth.Logout(r.Context(), token); err != nil {
		// Некорректный формат токена считаем ошибкой входного параметра.
		if errors.Is(err, service.ErrInvalidSession) {
			writeError(
				w,
				http.StatusBadRequest,
				"invalid token format",
			)
			return
		}

		log.Error().
			Err(err).
			Msg("failed to logout")

		writeError(
			w,
			http.StatusInternalServerError,
			"internal server error",
		)
		return
	}

	// Ответ связан с авторизационной сессией, не кешируем.
	w.Header().Set("Cache-Control", "no-store")

	writeJSON(w, http.StatusOK, APIResponse{
		Response: map[string]bool{
			token: true,
		},
	})
}

// writeError формирует единый JSON-ответ для HTTP-ошибок.
func writeError(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, APIResponse{
		Error: &APIError{
			Code: status,
			Text: text,
		},
	})
}

// writeJSON записывает APIResponse в HTTP-ответ в формате JSON.
func writeJSON(w http.ResponseWriter, status int, response APIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	// HTTP-статус уже отправлен клиенту, только логируем ошибку.
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().
			Err(err).
			Msg("failed to write HTTP response")
	}
}
