// Package value содержит типизированные значения платформы.
//
// Number — точное десятичное число без float64 и без выделений памяти:
// знаковый коэффициент до 32 десятичных цифр (помещается в 128 бит) и
// масштаб (число цифр после точки) до 32. Выбор представления и
// ожидания описаны в docs/implementation/numbers.md.
package value

import (
	"errors"
	"fmt"
	"math/bits"
)

const (
	// MaxDigits — наибольшее число значащих цифр коэффициента.
	MaxDigits = 32
	// MaxScale — наибольшее число цифр после десятичной точки.
	MaxScale = 32
)

var (
	// ErrSyntax — текст не является десятичным литералом.
	ErrSyntax = errors.New("value: invalid number syntax")
	// ErrOverflow — результат не помещается в 32 значащие цифры и масштаб 32.
	// Значение никогда не округляется молча.
	ErrOverflow = errors.New("value: number overflow")
	// ErrRange — недопустимый аргумент округления.
	ErrRange = errors.New("value: argument out of range")
)

// Number — точное десятичное число: (-1)^neg * (hi:lo) / 10^scale.
//
// Нулевое значение — число 0. Инварианты: коэффициент меньше 10^32,
// scale не больше MaxScale, у нуля нет знака (отрицательного нуля нет).
// Масштаб сохраняется: 0.30 и 0.3 равны по Cmp, но печатаются по-разному.
type Number struct {
	hi, lo uint64
	scale  uint8
	neg    bool
}

// RoundMode — политика округления Round.
type RoundMode int

const (
	// RoundTruncate отбрасывает лишние цифры (к нулю).
	RoundTruncate RoundMode = iota
	// RoundHalfAwayFromZero округляет половину от нуля: 0.5 -> 1, -0.5 -> -1.
	RoundHalfAwayFromZero
	// RoundHalfEven округляет половину к чётному: 0.5 -> 0, 1.5 -> 2.
	RoundHalfEven
)

var pow10 = [20]uint64{
	1, 10, 100, 1000, 10000, 100000, 1000000, 10000000, 100000000,
	1000000000, 10000000000, 100000000000, 1000000000000, 10000000000000,
	100000000000000, 1000000000000000, 10000000000000000,
	100000000000000000, 1000000000000000000, 10000000000000000000,
}

// limit — 10^32 как 128-битное число: hi = 10^32 >> 64, lo = 10^32 mod 2^64.
var limitHi, limitLo = func() (uint64, uint64) {
	// 10^32 = 10^19 * 10^13
	return bits.Mul64(pow10[19], pow10[13])
}()

func overLimit(hi, lo uint64) bool {
	return hi > limitHi || (hi == limitHi && lo >= limitLo)
}

// mulSmall умножает (hi,lo) на m; ok=false при выходе за 128 бит.
func mulSmall(hi, lo, m uint64) (uint64, uint64, bool) {
	h1, l1 := bits.Mul64(lo, m)
	h2, l2 := bits.Mul64(hi, m)
	nh, c := bits.Add64(l2, h1, 0)
	return nh, l1, h2 == 0 && c == 0
}

// mulPow10 умножает на 10^k, k >= 0.
func mulPow10(hi, lo uint64, k int) (uint64, uint64, bool) {
	for k > 0 {
		step := min(k, 19)
		var ok bool
		hi, lo, ok = mulSmall(hi, lo, pow10[step])
		if !ok {
			return 0, 0, false
		}
		k -= step
	}
	return hi, lo, true
}

// divSmall делит (hi,lo) на d, возвращает частное и остаток.
func divSmall(hi, lo, d uint64) (uint64, uint64, uint64) {
	qh := hi / d
	r := hi % d
	ql, r2 := bits.Div64(r, lo, d)
	return qh, ql, r2
}

// divPow10 делит на 10^k, k >= 0; sticky — остаток от деления не нулевой.
func divPow10(hi, lo uint64, k int) (qh, ql uint64, sticky bool) {
	for k > 0 {
		step := min(k, 19)
		var r uint64
		hi, lo, r = divSmall(hi, lo, pow10[step])
		sticky = sticky || r != 0
		k -= step
	}
	return hi, lo, sticky
}

func cmpMag(ah, al, bh, bl uint64) int {
	switch {
	case ah != bh:
		if ah < bh {
			return -1
		}
		return 1
	case al != bl:
		if al < bl {
			return -1
		}
		return 1
	}
	return 0
}

func (n Number) isZero() bool { return n.hi == 0 && n.lo == 0 }

func newNumber(neg bool, hi, lo uint64, scale int) (Number, error) {
	if scale > MaxScale || overLimit(hi, lo) {
		return Number{}, ErrOverflow
	}
	if hi == 0 && lo == 0 {
		neg = false
	}
	return Number{hi: hi, lo: lo, scale: uint8(scale), neg: neg}, nil
}

// FromInt64 возвращает целое число со scale 0.
func FromInt64(v int64) Number {
	neg := v < 0
	mag := uint64(v)
	if neg {
		mag = -mag
	}
	return Number{lo: mag, neg: neg && mag != 0}
}

// Parse разбирает литерал вида [+-]ddd[.ddd] без пробелов и экспоненты.
// Точка требует цифр с обеих сторон. Больше 32 значащих цифр или больше
// 32 цифр после точки — ErrOverflow; любой иной вид — ErrSyntax.
// Ведущие нули значащими не считаются; "-0" и "-0.0" дают нуль без знака.
func Parse(s string) (Number, error) {
	i := 0
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var hi, lo uint64
	intDigits, fracDigits, sig := 0, 0, 0
	seenDot := false
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if seenDot || intDigits == 0 {
				return Number{}, fmt.Errorf("%w: %q", ErrSyntax, s)
			}
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			return Number{}, fmt.Errorf("%w: %q", ErrSyntax, s)
		}
		if seenDot {
			fracDigits++
		} else {
			intDigits++
		}
		if sig > 0 || c != '0' {
			sig++
			if sig > MaxDigits {
				return Number{}, fmt.Errorf("%w: %q has more than %d significant digits", ErrOverflow, s, MaxDigits)
			}
		}
		var ok bool
		hi, lo, ok = mulSmall(hi, lo, 10)
		if !ok {
			return Number{}, fmt.Errorf("%w: %q", ErrOverflow, s)
		}
		var carry uint64
		lo, carry = bits.Add64(lo, uint64(c-'0'), 0)
		hi += carry
	}
	if intDigits == 0 || (seenDot && fracDigits == 0) {
		return Number{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	if fracDigits > MaxScale {
		return Number{}, fmt.Errorf("%w: %q has more than %d fraction digits", ErrOverflow, s, MaxScale)
	}
	return newNumber(neg, hi, lo, fracDigits)
}

// String печатает число обычной записью (без экспоненты) с сохранением масштаба.
func (n Number) String() string {
	var buf [MaxDigits + 1]byte // цифры без точки, справа налево
	pos := len(buf)
	hi, lo := n.hi, n.lo
	for hi != 0 || lo != 0 {
		var r uint64
		hi, lo, r = divSmall(hi, lo, pow10[19])
		for j := 0; j < 19; j++ {
			pos--
			buf[pos] = byte('0' + r%10)
			r /= 10
			if hi == 0 && lo == 0 && r == 0 {
				break
			}
		}
	}
	if pos == len(buf) {
		pos--
		buf[pos] = '0'
	}
	digits := buf[pos:]
	out := make([]byte, 0, MaxDigits+4)
	if n.neg {
		out = append(out, '-')
	}
	scale := int(n.scale)
	if len(digits) <= scale {
		out = append(out, '0', '.')
		for k := len(digits); k < scale; k++ {
			out = append(out, '0')
		}
		out = append(out, digits...)
		return string(out)
	}
	intLen := len(digits) - scale
	out = append(out, digits[:intLen]...)
	if scale > 0 {
		out = append(out, '.')
		out = append(out, digits[intLen:]...)
	}
	return string(out)
}

// Scale возвращает число цифр после точки.
func (n Number) Scale() int { return int(n.scale) }

// Sign возвращает -1, 0 или 1.
func (n Number) Sign() int {
	switch {
	case n.isZero():
		return 0
	case n.neg:
		return -1
	}
	return 1
}

// Neg возвращает число с обратным знаком.
func (n Number) Neg() Number {
	if !n.isZero() {
		n.neg = !n.neg
	}
	return n
}

// Cmp сравнивает числа по значению (масштаб не важен): -1, 0, 1.
func (n Number) Cmp(m Number) int {
	if sn, sm := n.Sign(), m.Sign(); sn != sm {
		if sn < sm {
			return -1
		}
		return 1
	}
	if n.isZero() {
		return 0
	}
	c := magCmpScaled(n, m)
	if n.neg {
		return -c
	}
	return c
}

// magCmpScaled сравнивает модули чисел разного масштаба без потери точности.
func magCmpScaled(n, m Number) int {
	switch {
	case n.scale < m.scale:
		h, l, ok := mulPow10(n.hi, n.lo, int(m.scale-n.scale))
		if !ok {
			return 1 // 128 бит больше любого коэффициента (< 10^32)
		}
		return cmpMag(h, l, m.hi, m.lo)
	case n.scale > m.scale:
		h, l, ok := mulPow10(m.hi, m.lo, int(n.scale-m.scale))
		if !ok {
			return -1
		}
		return cmpMag(n.hi, n.lo, h, l)
	}
	return cmpMag(n.hi, n.lo, m.hi, m.lo)
}

// Add возвращает точную сумму; масштаб результата — больший из двух.
// Не помещающийся результат — ErrOverflow, без округления.
func (n Number) Add(m Number) (Number, error) {
	scale := max(n.scale, m.scale)
	ah, al, ok1 := mulPow10(n.hi, n.lo, int(scale-n.scale))
	bh, bl, ok2 := mulPow10(m.hi, m.lo, int(scale-m.scale))
	// Если выравнивание вышло за 128 бит, точный результат не мог бы
	// поместиться в 10^32: другой слагаемый меньше 10^32 < 2^128 - 10^32.
	if !ok1 || !ok2 {
		return Number{}, ErrOverflow
	}
	if n.neg == m.neg {
		lo, c := bits.Add64(al, bl, 0)
		hi, c2 := bits.Add64(ah, bh, c)
		if c2 != 0 {
			return Number{}, ErrOverflow
		}
		return newNumber(n.neg, hi, lo, int(scale))
	}
	neg := n.neg
	if cmpMag(ah, al, bh, bl) < 0 {
		ah, al, bh, bl = bh, bl, ah, al
		neg = m.neg
	}
	lo, b := bits.Sub64(al, bl, 0)
	hi, _ := bits.Sub64(ah, bh, b)
	return newNumber(neg, hi, lo, int(scale))
}

// Sub возвращает точную разность n - m.
func (n Number) Sub(m Number) (Number, error) { return n.Add(m.Neg()) }

// Round округляет до digits знаков после точки (digits < 0 — до десятков,
// сотен и т. д.) по политике mode. Если у числа уже не больше digits знаков,
// оно возвращается без изменений (дополнение нулями не выполняется).
// digits вне [-MaxDigits+1, MaxScale] — ErrRange; результат 10^32 — ErrOverflow.
// Соответствие режимов функции «Окр» оригинала не подтверждено.
func (n Number) Round(digits int, mode RoundMode) (Number, error) {
	if digits < -(MaxDigits-1) || digits > MaxScale {
		return Number{}, fmt.Errorf("%w: digits %d", ErrRange, digits)
	}
	if mode < RoundTruncate || mode > RoundHalfEven {
		return Number{}, fmt.Errorf("%w: mode %d", ErrRange, int(mode))
	}
	drop := int(n.scale) - digits
	if drop <= 0 {
		return n, nil
	}
	qh, ql, sticky := divPow10(n.hi, n.lo, drop-1)
	qh, ql, r := divSmall(qh, ql, 10)
	up := false
	switch mode {
	case RoundHalfAwayFromZero:
		up = r >= 5
	case RoundHalfEven:
		up = r > 5 || (r == 5 && (sticky || ql&1 == 1))
	}
	if up {
		var c uint64
		ql, c = bits.Add64(ql, 1, 0)
		qh += c
	}
	newScale := max(digits, 0)
	if digits < 0 {
		var ok bool
		qh, ql, ok = mulPow10(qh, ql, -digits)
		if !ok {
			return Number{}, ErrOverflow
		}
	}
	return newNumber(n.neg, qh, ql, newScale)
}
