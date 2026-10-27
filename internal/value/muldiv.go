package value

import (
	"encoding/binary"
	"errors"
	"math/big"
)

var (
	// ErrDivideByZero — деление или остаток от деления на нуль.
	ErrDivideByZero = errors.New("value: division by zero")
	// ErrInexact — частное не выражается точно: после масштаба 32 остаётся
	// остаток. Молчаливого округления нет; округляйте явно (Round) до деления
	// или выбирайте другой порядок вычислений.
	ErrInexact = errors.New("value: result is not exactly representable")
)

var bigLimit = new(big.Int).Exp(big.NewInt(10), big.NewInt(MaxDigits), nil)

func (n Number) coefficient() *big.Int {
	c := new(big.Int).SetUint64(n.hi)
	c.Lsh(c, 64)
	c.Or(c, new(big.Int).SetUint64(n.lo))
	if n.neg {
		c.Neg(c)
	}
	return c
}

func pow10Big(k int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)
}

// fromBig превращает коэффициент и масштаб в Number. Лишние завершающие нули
// отбрасываются только пока результат не помещается (больше 32 цифр или масштаб
// больше 32); иначе масштаб сохраняется как есть. Не помещается — ErrOverflow.
func fromBig(c *big.Int, scale int) (Number, error) {
	ten := big.NewInt(10)
	r := new(big.Int)
	for scale > 0 {
		abs := new(big.Int).Abs(c)
		if abs.Cmp(bigLimit) < 0 && scale <= MaxScale {
			break
		}
		q, m := new(big.Int).QuoRem(c, ten, r)
		if m.Sign() != 0 {
			break
		}
		c, scale = q, scale-1
	}
	neg := c.Sign() < 0
	abs := new(big.Int).Abs(c)
	if abs.Cmp(bigLimit) >= 0 || scale > MaxScale || scale < 0 {
		return Number{}, ErrOverflow
	}
	var buf [16]byte // модуль меньше 10^32 < 2^128
	abs.FillBytes(buf[:])
	hi := binary.BigEndian.Uint64(buf[:8])
	lo := binary.BigEndian.Uint64(buf[8:])
	return newNumber(neg, hi, lo, scale)
}

// Mul возвращает точное произведение; масштаб — сумма масштабов, уменьшенная
// только если результат иначе не поместился бы. Не помещается — ErrOverflow.
// Работает через math/big: обещание нуля аллокаций на Mul не распространяется.
func (n Number) Mul(m Number) (Number, error) {
	c := new(big.Int).Mul(n.coefficient(), m.coefficient())
	return fromBig(c, int(n.scale)+int(m.scale))
}

// Div возвращает точное частное. Результат выражается без остатка с масштабом не
// больше 32, иначе ErrInexact (1/3 — ошибка, 1/4 = 0.25, 6/3 = 2). Деление на
// нуль — ErrDivideByZero. Правила оригинала для неточных частных не подтверждены,
// поэтому они не угадываются.
func (n Number) Div(m Number) (Number, error) {
	if m.isZero() {
		return Number{}, ErrDivideByZero
	}
	a, b := n.coefficient(), m.coefficient()
	diff := int(n.scale) - int(m.scale)
	k := max(0, -diff)
	for ; diff+k <= MaxScale; k++ {
		num := new(big.Int).Mul(a, pow10Big(k))
		q, r := new(big.Int).QuoRem(num, b, new(big.Int))
		if r.Sign() == 0 {
			return fromBig(q, diff+k)
		}
	}
	return Number{}, ErrInexact
}

// Mod возвращает остаток от деления n на m со знаком делимого (частное
// отсекается к нулю), масштаб — больший из двух. Деление на нуль — ErrDivideByZero.
func (n Number) Mod(m Number) (Number, error) {
	if m.isZero() {
		return Number{}, ErrDivideByZero
	}
	scale := max(n.scale, m.scale)
	a := new(big.Int).Mul(n.coefficient(), pow10Big(int(scale-n.scale)))
	b := new(big.Int).Mul(m.coefficient(), pow10Big(int(scale-m.scale)))
	r := new(big.Int).Rem(a, b)
	return fromBig(r, int(scale))
}
