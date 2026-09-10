package server

import (
	"context"
	"document-cache-api/internal/cache"
	"document-cache-api/internal/config"
	"document-cache-api/internal/database"
	"document-cache-api/internal/database/sqlc"
	"document-cache-api/internal/handlers"
	"document-cache-api/internal/logs"
	"document-cache-api/internal/middleware"
	"document-cache-api/internal/service"
	"document-cache-api/internal/storage"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
)

const serviceName = "astral-docs"

// Run запускает HTTP-сервер и завершает его по SIGINT/SIGTERM.
func Run() error {
	cfg := config.Get()

	// Настраиваем глобальный логгер приложения.
	if err := logs.Setup(cfg.LoggerConfig(serviceName)); err != nil {
		return fmt.Errorf("setup logger: %w", err)
	}

	// Создаём контекст, который отменится при получении SIGINT или SIGTERM.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// Инициализируем подключение к базе PostgreSQL.
	log.Info().Msg("connecting to PostgreSQL")

	pgxPool, err := database.ConnectToDatabase(ctx, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pgxPool.Close()

	log.Info().Msg("connected to PostgreSQL")

	queries := sqlc.New(pgxPool)

	fileStorage, err := storage.NewFileStorage(cfg.Storage.Dir)
	if err != nil {
		return fmt.Errorf("initialize file storage: %w", err)
	}

	authService := service.NewAuthService(queries, cfg.Auth)
	documentService := service.NewDocumentService(
		pgxPool,
		queries,
		fileStorage,
	)

	responseCache := cache.New(
		cfg.Cache.MaxEntries,
		cfg.Cache.MaxBytes,
	)

	h := handlers.New(
		authService,
		documentService,
		cfg.Storage.MaxUploadSize,
		responseCache,
	)

	// Создаём mux и регистрируем HTTP-маршруты приложения.
	mux := http.NewServeMux()
	registerRoutes(mux, h)

	// Подключаем логирование ко всем маршрутам.
	srv := newHTTPServer(cfg, middleware.Logging(mux))

	// Делаем канал для получения ошибки из ListenAndServe.
	serverErrCh := make(chan error, 1)

	go func() {
		serverErrCh <- srv.ListenAndServe()
	}()

	log.Info().
		Str("address", srv.Addr).
		Msg("starting HTTP server")

	// Ждём либо ошибку сервера, либо сигнал завершения процесса.
	select {
	case err := <-serverErrCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return fmt.Errorf("serve HTTP: %w", err)

	case <-ctx.Done():
		// Повторный сигнал сможет завершить процесс немедленно.
		stop()
		log.Info().Msg("shutting down HTTP server")
	}

	// Даём серверу ограниченное время на graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		cfg.Server.ShutdownTimeout,
	)
	defer cancel()

	// Shutdown закрывает listener и ждёт завершения активных запросов.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Если запросы не завершились вовремя, закрываем соединения.
		if closeErr := srv.Close(); closeErr != nil {
			log.Error().
				Err(closeErr).
				Msg("failed to close HTTP server")
		}

		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	// После Shutdown ListenAndServe должен завершиться с http.ErrServerClosed.
	if err := <-serverErrCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}

	log.Info().Msg("HTTP server stopped")
	return nil
}

// newHTTPServer создаёт HTTP-сервер с обработчиком и таймаутами из конфигурации.
func newHTTPServer(cfg *config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         cfg.HTTPAddress(),
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
}

// registerRoutes регистрирует маршруты HTTP API.
func registerRoutes(mux *http.ServeMux, h *handlers.Handler) {
	mux.HandleFunc("POST /api/register", h.Register)
	mux.HandleFunc("POST /api/auth", h.Auth)
	mux.HandleFunc("DELETE /api/auth/{token}", h.Logout)

	mux.HandleFunc("POST /api/docs", h.UploadDocument)
	mux.HandleFunc("GET /api/docs", h.ListDocuments)
	mux.HandleFunc("GET /api/docs/{id}", h.GetDocument)
	mux.HandleFunc("DELETE /api/docs/{id}", h.DeleteDocument)

	// Обработчики для остальных методов на известных путях.
	mux.HandleFunc(
		"/api/register",
		handlers.MethodNotAllowed("POST"),
	)
	mux.HandleFunc(
		"/api/auth",
		handlers.MethodNotAllowed("POST"),
	)
	mux.HandleFunc(
		"/api/auth/{token}",
		handlers.MethodNotAllowed("DELETE"),
	)
	mux.HandleFunc(
		"/api/docs",
		handlers.MethodNotAllowed("GET, HEAD, POST"),
	)
	mux.HandleFunc(
		"/api/docs/{id}",
		handlers.MethodNotAllowed("GET, HEAD, DELETE"),
	)
}
