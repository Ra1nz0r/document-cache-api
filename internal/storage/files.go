package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStorage хранит файлы документов в локальной директории.
type FileStorage struct {
	dir string
}

// NewFileStorage создаёт файловое хранилище.
// Если директории ещё нет, она будет создана.
func NewFileStorage(dir string) (*FileStorage, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}

	return &FileStorage{dir: dir}, nil
}

// Save сохраняет файл под уникальным серверным именем.
// Возвращает ключ файла и его размер.
func (s *FileStorage) Save(src io.Reader) (string, int64, error) {
	// Оригинальное имя не используем, имя файла генерирует сервер.
	file, err := os.CreateTemp(s.dir, "doc-*")
	if err != nil {
		return "", 0, fmt.Errorf("create document file: %w", err)
	}

	path := file.Name()

	size, copyErr := io.Copy(file, src)
	closeErr := file.Close()

	// При ошибке записи удаляем недописанный файл.
	if copyErr != nil {
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("write document file: %w", copyErr)
	}

	if closeErr != nil {
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("close document file: %w", closeErr)
	}

	// В БД сохраняем только имя файла, без полного пути.
	return filepath.Base(path), size, nil
}

// Delete удаляет файл по серверному ключу.
func (s *FileStorage) Delete(key string) error {
	// Ключ должен быть только именем файла, без частей пути.
	if key == "" || key == "." || key == ".." ||
		filepath.Base(key) != key {
		return fmt.Errorf("invalid storage key")
	}

	err := os.Remove(filepath.Join(s.dir, key))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove document file: %w", err)
	}

	return nil
}
