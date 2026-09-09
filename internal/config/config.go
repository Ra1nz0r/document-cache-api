package config

import (
	"document-cache-api/internal/logs"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joeshaw/envdecode"
)

const (
	defaultConfigDir  = "."
	defaultConfigFile = "document-cache-api_config.cfg"
)

var (
	ConfigPath = defaultConfigFile
)

var (
	once     sync.Once
	instance *Config
)

type Config struct {
	Server   ServerConfig   `toml:"server"`
	Log      LogConfig      `toml:"log"`
	Database DatabaseConfig `toml:"database"`
	Auth     AuthConfig     `toml:"auth"`
	Storage  StorageConfig  `toml:"storage"`
	Cache    CacheConfig    `toml:"cache"`
}

type ServerConfig struct {
	Host            string        `toml:"host" env:"DOC_CACHE_SERVER_HOST"`                         // Адрес, на котором запускается HTTP-сервер
	Port            int           `toml:"port" env:"DOC_CACHE_SERVER_PORT"`                         // Порт HTTP-сервера
	ReadTimeout     time.Duration `toml:"read_timeout" env:"DOC_CACHE_SERVER_READ_TIMEOUT"`         // Максимальное время чтения входящего запроса
	WriteTimeout    time.Duration `toml:"write_timeout" env:"DOC_CACHE_SERVER_WRITE_TIMEOUT"`       // Максимальное время записи ответа клиенту
	IdleTimeout     time.Duration `toml:"idle_timeout" env:"DOC_CACHE_SERVER_IDLE_TIMEOUT"`         // Время жизни keep-alive соединения без активных запросов
	ShutdownTimeout time.Duration `toml:"shutdown_timeout" env:"DOC_CACHE_SERVER_SHUTDOWN_TIMEOUT"` // Максимальное время graceful shutdown
}

type LogConfig struct {
	Level  string `toml:"level" env:"DOC_CACHE_LOG_LEVEL"`     // Уровень логирования: trace, debug, info, warn, error
	Dir    string `toml:"dir" env:"DOC_CACHE_LOG_DIR"`         // Каталог для файлов логов
	Pretty bool   `toml:"pretty" env:"DOC_CACHE_LOG_PRETTY"`   // Человекочитаемый вывод в консоль
	ToFile bool   `toml:"to_file" env:"DOC_CACHE_LOG_TO_FILE"` // Писать ли логи в файл
	Caller bool   `toml:"caller" env:"DOC_CACHE_LOG_CALLER"`   // Добавлять file:line места вызова
}

type DatabaseConfig struct {
	// Строка подключения к PostgreSQL.
	DSN string `toml:"dsn" env:"DOC_CACHE_DATABASE_DSN"`
}

type AuthConfig struct {
	// Фиксированный токен для регистрации пользователей.
	AdminToken string `toml:"admin_token" env:"DOC_CACHE_AUTH_ADMIN_TOKEN"`

	// Срок действия пользовательской сессии.
	SessionTTL time.Duration `toml:"session_ttl" env:"DOC_CACHE_AUTH_SESSION_TTL"`
}

type StorageConfig struct {
	// Каталог хранения загруженных файлов.
	Dir string `toml:"dir" env:"DOC_CACHE_STORAGE_DIR"`

	// Максимальный размер всего multipart-запроса в байтах.
	MaxUploadSize int64 `toml:"max_upload_size" env:"DOC_CACHE_STORAGE_MAX_UPLOAD_SIZE"`
}

type CacheConfig struct {
	// Максимальное количество записей.
	MaxEntries int `toml:"max_entries" env:"DOC_CACHE_CACHE_MAX_ENTRIES"`

	// Максимальный суммарный размер закешированных тел ответов в байтах.
	MaxBytes int64 `toml:"max_bytes" env:"DOC_CACHE_CACHE_MAX_BYTES"`
}

// Get возвращает единственный экземпляр конфигурации.
func Get() *Config {
	once.Do(func() {
		instance = new(Config)

		if err := load(instance); err != nil {
			log.Fatalf("config.Get(): failed to load configuration: %v", err)
		}

		setDefaults(instance)
	})

	return instance
}

// LoggerConfig возвращает конфигурацию логгера в формате,
// который ожидает пакет logs.
func (c *Config) LoggerConfig(serviceName string) logs.LoggerConfig {
	return logs.LoggerConfig{
		Service:             serviceName,
		IncludeServiceField: true,
		FileName:            serviceName + ".log",
		Level:               c.Log.Level,
		Pretty:              c.Log.Pretty,
		ToFile:              c.Log.ToFile,
		Dir:                 c.Log.Dir,
		Caller:              c.Log.Caller,
	}
}

// HTTPAddress возвращает адрес сервера в формате host:port.
func (c *Config) HTTPAddress() string {
	return net.JoinHostPort(c.Server.Host, strconv.Itoa(c.Server.Port))
}

// load загружает конфигурацию из TOML-файла или, если файл не найден, из env.
func load(cfg *Config) error {
	f := ConfigPath

	if _, err := os.Stat(f); errors.Is(err, os.ErrNotExist) {
		if !strings.Contains(f, string(os.PathSeparator)) {
			f = defaultConfigDir + string(os.PathSeparator) + f
		}

		if _, err := os.Stat(f); errors.Is(err, os.ErrNotExist) {
			if err := envdecode.Decode(cfg); err != nil {
				return fmt.Errorf("load config from env: %w", err)
			}
			return nil
		}
	}

	if _, err := toml.DecodeFile(f, cfg); err != nil {
		return fmt.Errorf("load config from file: %w", err)
	}

	return nil
}

// setDefaults задаёт значения по умолчанию для необязательных полей.
func setDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Second
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 120 * time.Second
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = 10 * time.Second
	}

	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Dir == "" {
		cfg.Log.Dir = "log"
	}

	if cfg.Auth.SessionTTL == 0 {
		cfg.Auth.SessionTTL = 24 * time.Hour
	}

	if cfg.Storage.Dir == "" {
		cfg.Storage.Dir = "data/files"
	}
	if cfg.Storage.MaxUploadSize == 0 {
		cfg.Storage.MaxUploadSize = 10 << 20 // 10 МиБ
	}

	if cfg.Cache.MaxEntries == 0 {
		cfg.Cache.MaxEntries = 1000
	}
	if cfg.Cache.MaxBytes == 0 {
		cfg.Cache.MaxBytes = 64 << 20 // 64 МиБ
	}
}

// validate валидирует необходимые значения.
func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		return errors.New("database.dsn is required")
	}
	if strings.TrimSpace(cfg.Auth.AdminToken) == "" {
		return errors.New("auth.admin_token is required")
	}

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return errors.New("server.port must be between 1 and 65535")
	}

	if cfg.Server.ReadTimeout <= 0 ||
		cfg.Server.WriteTimeout <= 0 ||
		cfg.Server.IdleTimeout <= 0 ||
		cfg.Server.ShutdownTimeout <= 0 {
		return errors.New("server timeouts must be positive")
	}

	if cfg.Auth.SessionTTL <= 0 {
		return errors.New("auth.session_ttl must be positive")
	}
	if cfg.Storage.MaxUploadSize <= 0 {
		return errors.New("storage.max_upload_size must be positive")
	}
	if cfg.Cache.MaxEntries <= 0 {
		return errors.New("cache.max_entries must be positive")
	}
	if cfg.Cache.MaxBytes <= 0 {
		return errors.New("cache.max_bytes must be positive")
	}

	return nil
}
