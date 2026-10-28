package main

import (
	"fmt"
	"os"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// RoleEnv — переменная окружения с локальной ролью сессии: read (только чтение) или operator (по умолчанию).
// Роль применяет ядро (sqlite.Store.SetRole): запись под ролью чтения отклоняется ErrDenied. Это не аутентификация:
// роль выбирает тот, кто запускает приложение.
const RoleEnv = "ODDYNESS_ROLE"

// openSession открывает базу и ставит роль сессии из RoleEnv. Неизвестное значение — ошибка до открытия базы.
func openSession(path string, m *metadata.Model) (*sqlite.Store, error) {
	role := sqlite.RoleOperator
	if v := os.Getenv(RoleEnv); v != "" {
		r, err := sqlite.ParseRole(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", RoleEnv, err)
		}
		role = r
	}
	s, err := sqlite.Open(path, m)
	if err != nil {
		return nil, err
	}
	s.SetRole(role)
	return s, nil
}
