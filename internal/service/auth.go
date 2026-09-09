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

// Login проверяет логин и пароль, создаёт сессию и возвращает token.
func (s *AuthService) Login(
	ctx context.Context,
	login string,
	password string,
) (string, error) {
	// Пустые данные сразу считаем неверными учётными данными.
	// Ограничение в 72 байта связано с максимальной длиной пароля для bcrypt.
	if login == "" || password == "" || len(password) > 72 {
		return "", ErrInvalidCredentials
	}

	// Ищем пользователя по логину, чтобы получить сохранённый bcrypt-хеш пароля.
	user, err := s.queries.GetUserByLogin(ctx, login)
	if err != nil {
		// Не сообщаем клиенту отдельно о несуществующем логине.
		// Неверный логин, и неверный пароль дают одну ошибку авторизации.
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("get user by login: %w", err)
	}

	// bcrypt сам извлекает параметры из сохранённого хеша
	// и сравнивает его с переданным пользователем паролем.
	if err := bcrypt.CompareHashAndPassword(
		[]byte(user.PasswordHash),
		[]byte(password),
	); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("compare password hash: %w", err)
	}

	// Генерируем 32 криптографически случайных байта.
	// В hex-представлении клиент получит токен длиной 64 символа.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}

	token := hex.EncodeToString(tokenBytes)

	// В БД не сохраняем исходный токен.
	// Хешируем именно строку, которую отдаём клиенту, и сохраняем её SHA-256-хеш.
	tokenHash := sha256.Sum256([]byte(token))

	// Создаём сессию для найденного пользователя.
	// Время окончания рассчитываем от текущего UTC-времени и SessionTTL из конфига.
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

	// Исходный токен существует только у клиента.
	// При последующих запросах сервер снова вычислит его SHA-256-хеш.
	return token, nil
}

// SessionUser содержит минимальные данные пользователя,
// полученные после успешной проверки авторизованной сессии.
type SessionUser struct {
	ID    pgtype.UUID // идентификатор пользователя в БД
	Login string      // логин пользователя, которому принадлежит сессия
}

// Authenticate проверяет session token и возвращает пользователя,
// которому принадлежит соответствующая действующая сессия.
func (s *AuthService) Authenticate(
	ctx context.Context,
	token string,
) (SessionUser, error) {
	// Проверяем формат полученного токена и получаем тот же SHA-256-хеш,
	// который сохранялся в БД при создании сессии.
	tokenHash, err := hashSessionToken(token)
	if err != nil {
		return SessionUser{}, err
	}

	// По хешу токена ищем пользователя, связанного с сессией.
	user, err := s.queries.GetSessionUser(ctx, tokenHash)
	if err != nil {
		// Отсутствующая сессия для API означает невалидную авторизацию.
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionUser{}, ErrInvalidSession
		}

		return SessionUser{}, fmt.Errorf("get session user: %w", err)
	}

	// Не отдаём наружу sqlc-модель целиком,
	// а возвращаем только необходимые service-слою данные пользователя.
	return SessionUser{
		ID:    user.ID,
		Login: user.Login,
	}, nil
}

// Logout завершает сессию по переданному token.
// Повторное удаление уже отсутствующей сессии также считается успешным.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	// В БД хранится SHA-256-хеш токена, поэтому сначала
	// проверяем формат и вычисляем хеш полученного значения.
	tokenHash, err := hashSessionToken(token)
	if err != nil {
		return err
	}

	// Удаляем сессию по хешу токена.
	// Если подходящей записи уже нет, DELETE остаётся идемпотентным.
	if err := s.queries.DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// hashSessionToken проверяет формат session token
// и вычисляет SHA-256-хеш для поиска сессии в БД.
func hashSessionToken(token string) ([]byte, error) {
	// Login создаёт токен из 32 случайных байт.
	// После hex-кодирования такой токен всегда состоит из 64 символов.
	if len(token) != 64 {
		return nil, ErrInvalidSession
	}

	// Проверяем не только длину, но и что строка действительно
	// является корректным hex-представлением.
	if _, err := hex.DecodeString(token); err != nil {
		return nil, ErrInvalidSession
	}

	// Повторяем то же преобразование, которое используется в Login:
	// SHA-256 считается от исходной 64-символьной строки токена.
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
