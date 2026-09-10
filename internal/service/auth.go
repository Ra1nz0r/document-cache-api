package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"document-cache-api/internal/config"
	"document-cache-api/internal/database/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	ErrLoginTaken         = errors.New("login is already taken")
	ErrInvalidCredentials = errors.New("invalid login or password")
	ErrInvalidSession     = errors.New("invalid or expired session")
)

// Логин от 8 символов, только латинские буквы и цифры.
var loginPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,}$`)

type AuthService struct {
	queries *sqlc.Queries
	cfg     config.AuthConfig
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

// Register проверяет данные и создаёт нового пользователя.
func (s *AuthService) Register(
	ctx context.Context,
	adminToken string,
	login string,
	password string,
) (string, error) {
	// Регистрация доступна только с admin token из конфига.
	if s.cfg.AdminToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(adminToken),
			[]byte(s.cfg.AdminToken),
		) != 1 {
		return "", ErrInvalidAdminToken
	}

	if !loginPattern.MatchString(login) {
		return "", ErrInvalidLogin
	}

	if !validPassword(password) {
		return "", ErrInvalidPassword
	}

	// В БД сохраняем только bcrypt-хеш пароля.
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	user, err := s.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Login:        login,
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError

		// 23505 - нарушение UNIQUE constraint.
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "users_login_key" {
			return "", ErrLoginTaken
		}

		return "", fmt.Errorf("create user: %w", err)
	}

	return user.Login, nil
}

// validPassword проверяет требования к паролю.
func validPassword(password string) bool {
	// bcrypt принимает пароль длиной максимум 72 байта.
	if !utf8.ValidString(password) ||
		utf8.RuneCountInString(password) < 8 ||
		len(password) > 72 {
		return false
	}

	var upper, lower, digit, special bool

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

// Login проверяет логин и пароль, создаёт сессию и возвращает token.
func (s *AuthService) Login(
	ctx context.Context,
	login string,
	password string,
) (string, error) {
	if login == "" || password == "" || len(password) > 72 {
		return "", ErrInvalidCredentials
	}

	user, err := s.queries.GetUserByLogin(ctx, login)
	if err != nil {
		// Не разделяем ошибки "нет пользователя" и "неверный пароль".
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("get user by login: %w", err)
	}

	// Проверяем пароль по сохранённому bcrypt-хешу.
	if err := bcrypt.CompareHashAndPassword(
		[]byte(user.PasswordHash),
		[]byte(password),
	); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("compare password hash: %w", err)
	}

	// 32 случайных байта превращаем в token из 64 hex-символов.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}

	token := hex.EncodeToString(tokenBytes)

	// Сам token в БД не храним, только его SHA-256.
	tokenHash := sha256.Sum256([]byte(token))

	err = s.queries.CreateSession(ctx, sqlc.CreateSessionParams{
		TokenHash: tokenHash[:],
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().UTC().Add(s.cfg.SessionTTL),
			Valid: true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	return token, nil
}

// SessionUser содержит данные пользователя из действующей сессии.
type SessionUser struct {
	ID    pgtype.UUID
	Login string
}

// Authenticate проверяет token и возвращает пользователя сессии.
func (s *AuthService) Authenticate(
	ctx context.Context,
	token string,
) (SessionUser, error) {
	tokenHash, err := hashSessionToken(token)
	if err != nil {
		return SessionUser{}, err
	}

	user, err := s.queries.GetSessionUser(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionUser{}, ErrInvalidSession
		}

		return SessionUser{}, fmt.Errorf("get session user: %w", err)
	}

	return SessionUser{
		ID:    user.ID,
		Login: user.Login,
	}, nil
}

// Logout удаляет сессию. Повторное удаление считается успешным.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	tokenHash, err := hashSessionToken(token)
	if err != nil {
		return err
	}

	if err := s.queries.DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// hashSessionToken проверяет формат token и возвращает его SHA-256.
func hashSessionToken(token string) ([]byte, error) {
	// Login всегда создаёт token из 64 hex-символов.
	if len(token) != 64 {
		return nil, ErrInvalidSession
	}

	if _, err := hex.DecodeString(token); err != nil {
		return nil, ErrInvalidSession
	}

	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
