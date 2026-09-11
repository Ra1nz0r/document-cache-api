package handlers

import "encoding/json"

// RegisterRequest содержит входные параметры регистрации.
type RegisterRequest struct {
	Token string // Токен приглашения
	Login string // Логин нового пользователя
	Pswd  string // Пароль нового пользователя
}

// RegisterResponse содержит результат успешной регистрации.
type RegisterResponse struct {
	Login string `json:"login"` // Логин нового пользователя
}

// APIResponse общая модель ответа.
// Пустые error, response и data не попадают в JSON.
type APIResponse struct {
	Error    *APIError `json:"error,omitempty"`    // nil, если нет ошибки
	Response any       `json:"response,omitempty"` // nil, если нет успешного ответа
	Data     any       `json:"data,omitempty"`     // nil, если нет дополнительных данных
}

// APIError содержит код и текст ошибки.
type APIError struct {
	Code int    `json:"code"` // HTTP-код ошибки
	Text string `json:"text"` // Читаемый текст ошибки
}

// AuthRequest содержит логин и пароль из формы авторизации.
type AuthRequest struct {
	Login string // Логин пользователя
	Pswd  string // Пароль пользователя
}

// AuthResponse содержит токен новой сессии.
type AuthResponse struct {
	Token string `json:"token"` // Токен авторизационной сессии
}

// UploadDocumentMeta описывает JSON из multipart-поля meta.
type UploadDocumentMeta struct {
	Name   string   `json:"name"`   // Читаемое имя документа
	File   bool     `json:"file"`   // true, если документ содержит файл
	Public bool     `json:"public"` // true, если документ публичный
	Token  string   `json:"token"`  // Токен авторизационной сессии
	MIME   string   `json:"mime"`   // MIME-тип документа, если file=true
	Grant  []string `json:"grant"`  // Список логинов, которым разрешён доступ к документу
}

// UploadDocumentResponse содержит данные, возвращаемые после загрузки документа.
type UploadDocumentResponse struct {
	JSON json.RawMessage `json:"json,omitempty"` // JSON-ответ, если документ содержит JSON
	File string          `json:"file,omitempty"` // URL для скачивания файла, если документ содержит файл
}

// DocumentListItem содержит метаданные документа для выдачи списка.
type DocumentListItem struct {
	ID      string   `json:"id"`      // Уникальный идентификатор документа
	Name    string   `json:"name"`    // Читаемое имя документа
	MIME    string   `json:"mime"`    // MIME-тип документа, если
	File    bool     `json:"file"`    // true, если документ содержит файл
	Public  bool     `json:"public"`  // true, если документ публичный
	Created string   `json:"created"` // Дата и время создания документа в формате RFC3339
	Grant   []string `json:"grant"`   // Список логинов, которым разрешён доступ к документу
}

// ListDocumentsResponse содержит список доступных документов.
type ListDocumentsResponse struct {
	Docs []DocumentListItem `json:"docs"` // Список документов, доступных пользователю
}
