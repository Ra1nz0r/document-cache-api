package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"document-cache-api/internal/service"

	"github.com/rs/zerolog/log"
)

type Handler struct {
	auth *service.AuthService // сервис с логикой регистрации и проверки пользователя
}

func New(auth *service.AuthService) *Handler {
	return &Handler{
		auth: auth,
	}
}

// Register обрабатывает HTTP-запрос на регистрацию нового пользователя.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	// Ограничиваем размер тела запроса, чтобы не принимать
	// слишком большие данные для небольшой формы регистрации.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	// ParseForm разбирает application/x-www-form-urlencoded
	// и сохраняет значения формы в r.PostForm.
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration form")
		return
	}

	req := RegisterRequest{
		Token: r.PostForm.Get("token"),
		Login: r.PostForm.Get("login"),
		Pswd:  r.PostForm.Get("pswd"),
	}

	// Вся основная логика регистрации находится в service-слое.
	// Handler отвечает только за HTTP-ввод, вызов сервиса и формирование ответа.
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
			// Неизвестную внутреннюю ошибку логируем,
			// но не возвращаем её детали клиенту.
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

	// При успешной регистрации возвращаем логин созданного пользователя.
	writeJSON(w, http.StatusOK, APIResponse{
		Response: RegisterResponse{
			Login: login,
		},
	})
}

// Auth обрабатывает аутентификацию пользователя и создаёт новую сессию.
func (h *Handler) Auth(w http.ResponseWriter, r *http.Request) {
	// Для формы с логином и паролем большого request body не требуется.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	// По ТЗ данные авторизации передаются как form-параметры.
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

	// Проверку учётных данных и создание сессии оставляем service-слою.
	// Handler работает только с HTTP-запросом и преобразует результат в ответ API.
	token, err := h.auth.Login(r.Context(), req.Login, req.Pswd)
	if err != nil {
		// Неверную пару login/password не отличаем для клиента,
		// в обоих случаях возвращается одна ошибка авторизации.
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

	// Ответ содержит токен авторизации, поэтому запрещаем его кеширование.
	w.Header().Set("Cache-Control", "no-store")

	// По ТЗ успешная аутентификация возвращает токен в поле response.
	writeJSON(w, http.StatusOK, APIResponse{
		Response: AuthResponse{
			Token: token,
		},
	})
}

// Logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	// По ТЗ токен передаётся в пути /api/auth/{token}.
	token := r.PathValue("token")

	if err := h.auth.Logout(r.Context(), token); err != nil {
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

	w.Header().Set("Cache-Control", "no-store")

	// По ТЗ ключом в response служит переданный токен.
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

	// На этом этапе HTTP-статус уже отправлен клиенту,
	// поэтому ошибку сериализации остаётся только залогировать.
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().
			Err(err).
			Msg("failed to write HTTP response")
	}
}
