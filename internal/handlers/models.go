package handlers

// RegisterRequest содержит данные формы для регистрации нового пользователя.
// Поля соответствуют входным параметрам POST /api/register из ТЗ.
type RegisterRequest struct {
	// Token — фиксированный токен администратора.
	// Его значение задаётся в конфиге приложения и проверяется при регистрации.
	Token string

	// Login — логин создаваемого пользователя.
	// По ТЗ должен содержать минимум 8 символов, только латинские буквы и цифры.
	Login string

	// Pswd — пароль создаваемого пользователя.
	// По ТЗ должен быть не короче 8 символов и содержать буквы
	// разных регистров, цифру и хотя бы один специальный символ.
	Pswd string
}

// RegisterResponse описывает успешный ответ метода регистрации.
// После создания пользователя API возвращает его логин в поле response.
type RegisterResponse struct {
	// Login — логин успешно созданного пользователя.
	Login string `json:"login"`
}

// APIResponse описывает общую модель ответа API из ТЗ.
// В конкретном ответе присутствуют только заполненные поля.
type APIResponse struct {
	// Error содержит описание ошибки.
	// omitempty убирает поле из JSON, если ошибки нет и указатель равен nil.
	Error *APIError `json:"error,omitempty"`

	// Response используется для подтверждения выполненного действия.
	// Тип any нужен, потому что разные методы возвращают здесь разные структуры.
	Response any `json:"response,omitempty"`

	// Data используется для возврата содержимого, например документа
	// или списка документов. Формат данных зависит от конкретного метода.
	Data any `json:"data,omitempty"`
}

// APIError описывает ошибку в общей модели ответа API.
type APIError struct {
	// Code — числовой код ошибки.
	// В текущей реализации сюда передаётся HTTP-статус ответа.
	Code int `json:"code"`

	// Text — текстовое описание ошибки для клиента.
	Text string `json:"text"`
}

// AuthRequest
type AuthRequest struct {
	Login string
	Pswd  string
}

// AuthResponse
type AuthResponse struct {
	Token string `json:"token"`
}
