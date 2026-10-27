package value

import (
	"fmt"
	"strings"
)

// Ref — типизированная ссылка на запись справочника.
type Ref struct {
	Catalog string
	ID      int64
}

// integerDigits — число цифр целой части без ведущих нулей (0 для |n| < 1).
func (n Number) integerDigits() int {
	s := strings.TrimPrefix(n.String(), "-")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return len(strings.TrimLeft(s, "0"))
}

// CheckPrecision проверяет, что число помещается в поле с digits значащими цифрами
// и scale цифрами после точки. Лишние знаки — ошибка: значения не округляются.
func (n Number) CheckPrecision(digits, scale int) error {
	if n.Scale() > scale {
		return fmt.Errorf("%s has %d fraction digits, at most %d allowed (values are never rounded)", n, n.Scale(), scale)
	}
	if d := n.integerDigits(); d > digits-scale {
		return fmt.Errorf("%s has %d integer digits, at most %d allowed", n, d, digits-scale)
	}
	return nil
}
