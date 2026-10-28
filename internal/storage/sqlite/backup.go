package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/unxed/oddyness/internal/metadata"
)

// ErrCorrupt — копия повреждена: проверка целостности SQLite не прошла.
var ErrCorrupt = errors.New("sqlite: the database file is corrupt")

// Backup пишет консистентную копию открытой базы в новый файл path: `VACUUM INTO` берёт согласованный снимок
// средствами SQLite, файл открытой базы никто не копирует вслепую. Существующий файл не перезаписывается (ErrExists);
// при ошибке недописанная копия удаляется. Это чтение: доступно и роли чтения.
func (s *Store) Backup(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := dsn(path); err != nil { // те же ограничения на путь, что и у базы
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return fmt.Errorf("sqlite: backup: %w", err)
	}
	return nil
}

// Restore создаёт новую базу newPath из копии backupPath. Копия проверяется до любой записи: открытие сверяет версию
// схемы и отпечаток конфигурации (ErrVersion, ErrConfig, «not an oddyness database»), затем PRAGMA integrity_check
// (ErrCorrupt). Существующая база newPath не перезаписывается (ErrExists), файл копии не меняется; при ошибке недописанная
// база удаляется. Автоматических миграций нет: копия другой конфигурации или версии отклоняется.
func Restore(ctx context.Context, backupPath, newPath string, m *metadata.Model) error {
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, newPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	src, err := Open(backupPath, m)
	if err != nil {
		return fmt.Errorf("sqlite: restore: %w", err)
	}
	defer src.Close()
	var check string
	if err := src.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if check != "ok" {
		return fmt.Errorf("%w: %s", ErrCorrupt, check)
	}
	if err := src.Backup(ctx, newPath); err != nil {
		return err
	}
	// копия должна открываться как рабочая база той же конфигурации
	dst, err := Open(newPath, m)
	if err != nil {
		os.Remove(newPath)
		return fmt.Errorf("sqlite: restore: the restored database does not open: %w", err)
	}
	return dst.Close()
}
