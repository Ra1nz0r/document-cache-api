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

// Register создаёт пользователя и возвращает его логин.
func (s *AuthService) Register(
	ctx context.Context,
	adminToken string,
	login string,
	password string,
) (string, error) {
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
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "users_login_key" {
			return "", ErrLoginTaken
		}

		return "", fmt.Errorf("create user: %w", err)
	}

	return user.Login, nil
}

func validPassword(password string) bool {
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
