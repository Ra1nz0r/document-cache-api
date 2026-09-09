package handlers

type RegisterRequest struct {
	Token string
	Login string
	Pswd  string
}

type RegisterResponse struct {
	Login string `json:"login"`
}

// APIResponse соответствует общей модели ответа из ТЗ.
type APIResponse struct {
	Error    *APIError `json:"error,omitempty"`
	Response any       `json:"response,omitempty"`
	Data     any       `json:"data,omitempty"`
}

type APIError struct {
	Code int    `json:"code"`
	Text string `json:"text"`
}
