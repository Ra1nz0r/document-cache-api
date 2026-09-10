package handlers

import "encoding/json"

// RegisterRequest содержит входные параметры регистрации.
type RegisterRequest struct {
	Token string
	Login string
	Pswd  string
}

// RegisterResponse содержит результат успешной регистрации.
type RegisterResponse struct {
	Login string `json:"login"`
}

// APIResponse общая модель ответа.
// Пустые error, response и data не попадают в JSON.
type APIResponse struct {
	Error    *APIError `json:"error,omitempty"`
	Response any       `json:"response,omitempty"`
	Data     any       `json:"data,omitempty"`
}

// APIError содержит код и текст ошибки.
type APIError struct {
	Code int    `json:"code"`
	Text string `json:"text"`
}

// AuthRequest содержит логин и пароль из формы авторизации.
type AuthRequest struct {
	Login string
	Pswd  string
}

// AuthResponse содержит токен новой сессии.
type AuthResponse struct {
	Token string `json:"token"`
}

// UploadDocumentMeta описывает JSON из multipart-поля meta.
type UploadDocumentMeta struct {
	Name   string   `json:"name"`
	File   bool     `json:"file"`
	Public bool     `json:"public"`
	Token  string   `json:"token"`
	MIME   string   `json:"mime"`
	Grant  []string `json:"grant"`
}

// UploadDocumentResponse содержит данные, возвращаемые после загрузки документа.
type UploadDocumentResponse struct {
	JSON json.RawMessage `json:"json,omitempty"`
	File string          `json:"file,omitempty"`
}

// DocumentListItem содержит метаданные документа для выдачи списка.
type DocumentListItem struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	MIME    string   `json:"mime"`
	File    bool     `json:"file"`
	Public  bool     `json:"public"`
	Created string   `json:"created"`
	Grant   []string `json:"grant"`
}

// ListDocumentsResponse содержит список доступных документов.
type ListDocumentsResponse struct {
	Docs []DocumentListItem `json:"docs"`
}
