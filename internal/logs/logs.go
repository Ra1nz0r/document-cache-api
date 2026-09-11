package logs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LoggerConfig struct {
	Service             string // Необязательное логическое имя сервиса.
	IncludeServiceField bool   // Добавлять ли Service в запись.
	FileName            string // Имя текущего файла лога, например api-service.log.
	Level               string // Уровень логирования: trace, debug, info, warn, error.
	Pretty              bool   // Включает человекочитаемый вывод в терминал.
	ToFile              bool   // Включает запись логов в файл.
	Dir                 string // Директория для файлов логов.
	Caller              bool   // Добавляет file:line места вызова.
}

// Setup настраивает глобальный логгер zerolog на основе переданной конфигурации.
func Setup(cfg LoggerConfig) error {
	// Если уровень логирования не задан, используется info.
	if cfg.Level == "" {
		cfg.Level = "info"
	}

	// Если директория для логов не задана, используется log.
	if cfg.ToFile && cfg.Dir == "" {
		cfg.Dir = "log"
	}

	// Разбираем и устанавливаем глобальный уровень логирования.
	level, err := zerolog.ParseLevel(strings.ToLower(cfg.Level))
	if err != nil {
		return fmt.Errorf("parse log level: %w", err)
	}
	zerolog.SetGlobalLevel(level)

	// Устанавливаем формат времени для всех записей лога.
	zerolog.TimeFieldFormat = time.RFC3339Nano

	// Собираем writer для вывода логов в консоль и, при необходимости, в файл.
	writer, err := buildWriter(cfg)
	if err != nil {
		return fmt.Errorf("cannot build writer: %w", err)
	}

	// Создаём логгер и добавляем общие поля.
	base := zerolog.New(writer).With().Timestamp()

	// Если нужно добавлять имя сервиса в запись и оно указано, добавляем поле service.
	if cfg.IncludeServiceField && cfg.Service != "" {
		base = base.Str("service", cfg.Service)
	}

	logger := base.Logger()

	// При необходимости добавляем информацию о месте вызова.
	if cfg.Caller {
		zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
			return fmt.Sprintf("%s:%d", filepath.Base(file), line)
		}
		logger = logger.With().Caller().Logger()
	}

	// Назначаем настроенный логгер глобальным
	log.Logger = logger
	return nil
}

// buildWriter собирает writer для вывода логов в консоль и, при необходимости, в файл.
func buildWriter(cfg LoggerConfig) (io.Writer, error) {
	var console io.Writer

	// Если включён Pretty-режим, используем человекочитаемый вывод в терминал.
	// Иначе пишем стандартный JSON-лог в stdout.
	if cfg.Pretty {
		console = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05.000",
		}
	} else {
		console = os.Stdout
	}

	// Если запись в файл отключена, возвращаем только консольный writer.
	if !cfg.ToFile {
		return console, nil
	}

	// Создаём директорию для логов, если она отсутствует.
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed create log dir: %w", err)
	}

	// Формируем путь к файлу лога по имени сервиса.
	filePath := filepath.Join(cfg.Dir, logFileName(cfg))

	fileWriter := &lumberjack.Logger{
		Filename:   filePath,
		MaxSize:    100,
		MaxBackups: 5,
		MaxAge:     10,
		Compress:   true,
	}

	// Возвращаем writer, который пишет и в консоль, и в файл.
	return zerolog.MultiLevelWriter(console, fileWriter), nil
}

// logFileName возвращает имя файла для записи логов.
// Если задан FileName, используется он.
// Если FileName не задан, но указан Service, используется <Service>.log.
// В остальных случаях используется app.log.
func logFileName(cfg LoggerConfig) string {
	if cfg.FileName != "" {
		return cfg.FileName
	}
	if cfg.Service != "" {
		return cfg.Service + ".log"
	}
	return "app.log"
}

// LogFunction вспомогательная функция для логирования, выводит сообщение
// о начале работы функции и при её завершении, включая время выполнения.
func LogFunction(name string) (string, func()) {
	start := time.Now()

	log.Trace().
		CallerSkipFrame(1).
		Str("func", name).
		Msg("===== START =====")

	return name, func() {
		log.Trace().
			CallerSkipFrame(1).
			Str("func", name).
			Dur("duration", time.Since(start)).
			Msg("===== FINISH =====")
	}
}
