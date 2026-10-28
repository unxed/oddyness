package sqlite

import (
	"errors"
	"fmt"
	"strings"
)

// Role — локальная роль сессии. Выбор роли не является аутентификацией и не защищает от владельца файла базы
// (он может открыть SQLite чем угодно): роль — правило ядра для приложения, которое открыло базу. Сеть и пользователи
// вне границ первой поставки.
type Role int32

const (
	// RoleOperator — чтение и запись (роль по умолчанию, как у прежних вызовов Open).
	RoleOperator Role = iota
	// RoleReader — только чтение: каталоги, документы, движения и остатки читаются, любая запись отклоняется.
	RoleReader
)

func (r Role) String() string {
	switch r {
	case RoleOperator:
		return "operator"
	case RoleReader:
		return "read"
	}
	return fmt.Sprintf("Role(%d)", int32(r))
}

// ParseRole разбирает имя роли: «operator» или «read» (допустимо «reader»), без учёта регистра.
func ParseRole(name string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "operator":
		return RoleOperator, nil
	case "read", "reader":
		return RoleReader, nil
	}
	return 0, fmt.Errorf("sqlite: unknown role %q (supported: read, operator)", name)
}

// ErrDenied — операция не разрешена роли сессии; ничего не записано.
var ErrDenied = errors.New("sqlite: the operation is not permitted for the session role")

// SetRole задаёт роль сессии; действует на все последующие операции этого Store. Прикладной код ставит роль сразу
// после Open.
func (s *Store) SetRole(r Role) { s.role.Store(int32(r)) }

// Role возвращает роль сессии.
func (s *Store) Role() Role { return Role(s.role.Load()) }

// CanWrite сообщает, разрешена ли роли запись; окна используют его, чтобы показать недоступные команды (защитой служит
// проверка в самих операциях, а не окно).
func (s *Store) CanWrite() bool { return s.Role() == RoleOperator }

// requireWrite — проверка роли в начале каждой публичной операции записи; ErrDenied называет операцию.
func (s *Store) requireWrite(op string) error {
	if r := s.Role(); r != RoleOperator {
		return fmt.Errorf("%w: %s (role %s)", ErrDenied, op, r)
	}
	return nil
}
