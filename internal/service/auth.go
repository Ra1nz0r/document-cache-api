package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"

	"document-cache-api/internal/config"
	"document-cache-api/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidAdminToken = errors.New("invalid admin token")
	ErrInvalidLogin      = errors.New(
		"login must contain at least 8 characters: Latin letters and digits only",
	)
	ErrInvalidPassword = errors.New(
		"password must contain at least 8 characters, uppercase and lowercase letters, a digit and a special character; maximum 72 bytes",
	)
	ErrLoginTaken = errors.New("login is already taken")
)

// loginPattern разрешает только латинские буквы и цифры.
// Минимальная длина логина 8 символов.
var loginPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,}$`)

type AuthService struct {
	queries *sqlc.Queries     // запросы к БД, сгенерированные sqlc
	cfg     config.AuthConfig // настройки авторизации из конфига
}

func NewAuthService(
	queries *sqlc.Queries,
	cfg config.AuthConfig,
) *AuthService {
	return &AuthService{
		queries: queries,
		cfg:     cfg,
	}
}

// Register проверяет входные данные, создаёт пользователя и возвращает его логин.
func (s *AuthService) Register(
	ctx context.Context,
	adminToken string,
	login string,
	password string,
) (string, error) {
	// Регистрация доступна только с AdminToken из конфига.
	// ConstantTimeCompare используется, чтобы сравнение не зависело
	// от позиции первого несовпавшего байта.
	if s.cfg.AdminToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(adminToken),
			[]byte(s.cfg.AdminToken),
		) != 1 {
		return "", ErrInvalidAdminToken
	}

	// Логин должен быть не короче 8 символов и состоять
	// только из латинских букв и цифр.
	if !loginPattern.MatchString(login) {
		return "", ErrInvalidLogin
	}

	// Проверяем минимальные требования к сложности пароля.
	if !validPassword(password) {
		return "", ErrInvalidPassword
	}

	// В БД храним не сам пароль, а его bcrypt-хеш.
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	// Создаём пользователя. Уникальность логина дополнительно
	// гарантируется в PostgreSQL через constraint.
	user, err := s.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Login:        login,
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError

		// PostgreSQL code 23505 означает нарушение UNIQUE constraint.
		// Здесь отдельно обрабатываем занятый логин, чтобы не отдавать
		// наружу внутреннюю ошибку базы.
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "users_login_key" {
			return "", ErrLoginTaken
		}

		return "", fmt.Errorf("create user: %w", err)
	}

	return user.Login, nil
}

// validPassword проверяет пароль по требованиям к длине и составу символов.
func validPassword(password string) bool {
	// bcrypt принимает пароль длиной максимум 72 байта.
	// Минимальную длину считаем в Unicode-символах, а не в байтах.
	if !utf8.ValidString(password) ||
		utf8.RuneCountInString(password) < 8 ||
		len(password) > 72 {
		return false
	}

	var upper, lower, digit, special bool

	// За один проход проверяем наличие символов каждого требуемого типа.
	for _, ch := range password {
		switch {
		case unicode.IsUpper(ch):
			upper = true
		case unicode.IsLower(ch):
			lower = true
		case unicode.IsDigit(ch):
			digit = true
		case !unicode.IsLetter(ch) && !unicode.IsDigit(ch):
			special = true
		}
	}

	return upper && lower && digit && special
}
