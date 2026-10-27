package value

import (
	"errors"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

const max32 = "99999999999999999999999999999999"

func mustParse(t testing.TB, s string) Number {
	t.Helper()
	n, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return n
}

// NUM-01: литералы с 19, 20, 31, 32 и 33 значащими цифрами.
func TestParseRoundTrip(t *testing.T) {
	// 19, 20, 31 и 32 цифры; границы; масштаб 32; масштаб у нуля.
	for _, s := range []string{
		"0", "0.1", "-0.30", "7", "-7",
		"1234567890123456789",
		"12345678901234567890",
		"1234567890123456789012345678901",
		"12345678901234567890123456789012",
		"-" + max32,
		max32,
		"0.00000000000000000000000000000001",
		"0.12345678901234567890123456789012",
		"1234567890123456789012345678.9012",
		"0.00",
		"0.000000000000000000000000000",
	} {
		n := mustParse(t, s)
		if got := n.String(); got != s {
			t.Errorf("Parse(%q).String() = %q", s, got)
		}
	}
}

func TestParseNormalization(t *testing.T) {
	for _, c := range [][2]string{
		{"007", "7"},
		{"+5", "5"},
		{"-0", "0"},
		{"-0.00", "0.00"},
		{"000.5", "0.5"},
		{"00000" + max32, max32},
	} {
		in, want := c[0], c[1]
		n := mustParse(t, in)
		if got := n.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
	// Отрицательного нуля нет.
	for _, s := range []string{"-0", "-0.0"} {
		n := mustParse(t, s)
		if n.Sign() != 0 || n.neg {
			t.Errorf("Parse(%q): sign=%d neg=%v", s, n.Sign(), n.neg)
		}
	}
	if z := mustParse(t, "0").Neg(); z.neg || z.String() != "0" {
		t.Errorf("Neg(0) = %q neg=%v", z.String(), z.neg)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		"", "-", "+", ".5", "5.", "-.5", "1.2.3", "1e5", "1E5", " 1", "1 ", "0x10",
		"1,5", "--1", "+-1", "1_000", "NaN", "Inf", "١", "1.5\n",
	} {
		if _, err := Parse(s); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) error = %v, want ErrSyntax", s, err)
		}
	}
	for _, s := range []string{
		// 33 значащие цифры
		"123456789012345678901234567890123",
		// 10^32
		"1" + strings.Repeat("0", 32),
		// 33 и 34 цифры после точки
		"0." + strings.Repeat("0", 32) + "1",
		"0.1234567890123456789012345678901234",
		"-123456789012345678901234567890123",
		// заведомо больше 128 бит
		strings.Repeat("9", 40),
		"1." + strings.Repeat("0", 33),
	} {
		if _, err := Parse(s); !errors.Is(err, ErrOverflow) {
			t.Errorf("Parse(%q) error = %v, want ErrOverflow", s, err)
		}
	}
}

// NUM-02: 0.1 + 0.2, перенос разряда, границы, переполнение.
func TestAddSub(t *testing.T) {
	cases := []struct{ a, b, sum, diff string }{
		{"0.1", "0.2", "0.3", "-0.1"},
		{"0.10", "0.2", "0.30", "-0.10"},
		{"9.99", "0.01", "10.00", "9.98"},
		{"0.3", "0.1", "0.4", "0.2"},
		{"1", "3", "4", "-2"},
		{"-1", "3", "2", "-4"},
		{"5", "-5.00", "0.00", "10.00"},
		{"99999999999999999999999999999998", "1", max32, "99999999999999999999999999999997"},
	}
	for _, c := range cases {
		a, b := mustParse(t, c.a), mustParse(t, c.b)
		sum, err := a.Add(b)
		if err != nil || sum.String() != c.sum {
			t.Errorf("%s + %s = %q, %v; want %q", c.a, c.b, sum.String(), err, c.sum)
		}
		diff, err := a.Sub(b)
		if err != nil || diff.String() != c.diff {
			t.Errorf("%s - %s = %q, %v; want %q", c.a, c.b, diff.String(), err, c.diff)
		}
	}
	zero, err := mustParse(t, max32).Add(mustParse(t, "-"+max32))
	if err != nil || zero.String() != "0" || zero.Sign() != 0 || zero.neg {
		t.Errorf("max + (-max) = %q, %v", zero.String(), err)
	}
}

func TestAddOverflow(t *testing.T) {
	one, minOne := mustParse(t, "1"), mustParse(t, "-1")
	tiny := mustParse(t, "0.00000000000000000000000000000001")
	mx, minMx := mustParse(t, max32), mustParse(t, "-"+max32)
	// Для max+tiny точный результат имеет 64 цифры при масштабе 32.
	cases := []struct {
		name string
		f    func() (Number, error)
	}{
		{"max+1", func() (Number, error) { return mx.Add(one) }},
		{"-max-1", func() (Number, error) { return minMx.Add(minOne) }},
		{"max-(-max)", func() (Number, error) { return mx.Sub(minMx) }},
		{"max+max", func() (Number, error) { return mx.Add(mx) }},
		{"max+tiny", func() (Number, error) { return mx.Add(tiny) }},
		{"tiny+max", func() (Number, error) { return tiny.Add(mx) }},
		{"tiny-max", func() (Number, error) { return tiny.Sub(mx) }},
		{"carry to 10^32", func() (Number, error) {
			return mustParse(t, "9999999999999999999999999999999.9").Add(mustParse(t, "0.1"))
		}},
	}
	for _, c := range cases {
		got, err := c.f()
		if !errors.Is(err, ErrOverflow) {
			t.Errorf("%s = %q, %v; want ErrOverflow", c.name, got.String(), err)
		}
	}
}

func TestNeg(t *testing.T) {
	if got := mustParse(t, "1.50").Neg().String(); got != "-1.50" {
		t.Errorf("Neg(1.50) = %q", got)
	}
	if got := mustParse(t, "-1.50").Neg().String(); got != "1.50" {
		t.Errorf("Neg(-1.50) = %q", got)
	}
}

func TestCmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1", 0},
		{"1", "1.000", 0},
		{"-0", "0", 0},
		{"-1", "1", -1},
		{"1", "-1", 1},
		{"-5", "-6", 1},
		{"-6", "-5", -1},
		{"0.1", "0.10000000000000000000000000000001", -1},
		{"0.10000000000000000000000000000001", "0.1", 1},
		// выравнивание масштаба не помещается в 128 бит
		{max32, "0.00000000000000000000000000000001", 1},
		{"0.00000000000000000000000000000001", max32, -1},
		{"-" + max32, "-0.00000000000000000000000000000001", -1},
		{"0", "0.00000000000000000000000000000001", -1},
		{"0.2", "0.19999999999999999999999999999999", 1},
	}
	for _, c := range cases {
		a, b := mustParse(t, c.a), mustParse(t, c.b)
		if got := a.Cmp(b); got != c.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := b.Cmp(a); got != -c.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

// NUM-03: положительные и отрицательные половины, разные разряды округления.
// Ожидания выведены из определения режимов; соответствие «Окр» оригинала
// не проверено (docs/implementation/numbers.md).
func TestRound(t *testing.T) {
	const (
		T = RoundTruncate
		A = RoundHalfAwayFromZero
		E = RoundHalfEven
	)
	cases := []struct {
		in     string
		digits int
		mode   RoundMode
		want   string
	}{
		{"0.5", 0, A, "1"}, {"-0.5", 0, A, "-1"}, {"1.5", 0, A, "2"}, {"2.5", 0, A, "3"},
		{"-2.5", 0, A, "-3"}, {"0.4", 0, A, "0"}, {"-0.4", 0, A, "0"},
		{"0.05", 1, A, "0.1"}, {"0.04", 1, A, "0.0"}, {"1.005", 2, A, "1.01"}, {"-1.005", 2, A, "-1.01"},
		{"0.5", 0, E, "0"}, {"1.5", 0, E, "2"}, {"2.5", 0, E, "2"}, {"-2.5", 0, E, "-2"},
		{"-0.5", 0, E, "0"}, {"2.50000001", 0, E, "3"}, {"2.49999999", 0, E, "2"},
		{"0.15", 1, E, "0.2"}, {"0.25", 1, E, "0.2"}, {"0.35", 1, E, "0.4"},
		{"1.99", 0, T, "1"}, {"-1.99", 0, T, "-1"}, {"1.99", 1, T, "1.9"}, {"-0.99", 0, T, "0"},
		{"1250", -2, A, "1300"}, {"1250", -2, E, "1200"}, {"1251", -2, E, "1300"},
		{"1350", -2, E, "1400"}, {"149", -2, A, "100"}, {"-150", -2, A, "-200"},
		{"1250.5", -2, E, "1300"}, {"1249.99", -2, A, "1200"}, {"1999", -3, T, "1000"},
		{"1.5", 3, A, "1.5"}, {"1.50", 1, A, "1.5"}, {"1.50", 1, E, "1.5"}, {"7", 0, A, "7"},
		{"0.99999999999999999999999999999999", 31, A, "1.0000000000000000000000000000000"},
		{"0.99999999999999999999999999999999", 0, A, "1"},
		{"0.99999999999999999999999999999999", 0, T, "0"},
		{"0.12345678901234567890123456789012", 31, A, "0.1234567890123456789012345678901"},
		{"0.12345678901234567890123456789012", 30, A, "0.123456789012345678901234567890"},
		{"0.12345678901234567890123456789015", 31, E, "0.1234567890123456789012345678902"},
		{"0.12345678901234567890123456789025", 31, E, "0.1234567890123456789012345678902"},
	}
	for _, c := range cases {
		got, err := mustParse(t, c.in).Round(c.digits, c.mode)
		if err != nil || got.String() != c.want {
			t.Errorf("Round(%s, %d, %d) = %q, %v; want %q", c.in, c.digits, c.mode, got.String(), err, c.want)
		}
		if got.Sign() == 0 && got.neg {
			t.Errorf("Round(%s, %d, %d): отрицательный ноль", c.in, c.digits, c.mode)
		}
	}
	// Результат округления до целого имеет масштаб 0.
	if r, _ := mustParse(t, "0.5").Round(0, A); r.Scale() != 0 {
		t.Errorf("scale = %d, want 0", r.Scale())
	}
}

func TestRoundErrors(t *testing.T) {
	n := mustParse(t, "1.5")
	for _, d := range []int{MaxScale + 1, -MaxDigits, math.MinInt, math.MaxInt} {
		if _, err := n.Round(d, RoundHalfEven); !errors.Is(err, ErrRange) {
			t.Errorf("Round(digits=%d) error = %v, want ErrRange", d, err)
		}
	}
	for _, m := range []RoundMode{-1, 3, 99} {
		if _, err := n.Round(0, m); !errors.Is(err, ErrRange) {
			t.Errorf("Round(mode=%d) error = %v, want ErrRange", int(m), err)
		}
	}
	// Округление вверх до 10^32 — переполнение, а не молчаливая потеря.
	if got, err := mustParse(t, max32).Round(-1, RoundHalfAwayFromZero); !errors.Is(err, ErrOverflow) {
		t.Errorf("Round(max, -1) = %q, %v; want ErrOverflow", got.String(), err)
	}
}

func TestFromInt64(t *testing.T) {
	for _, c := range []struct {
		v    int64
		want string
	}{
		{0, "0"}, {1, "1"}, {-1, "-1"},
		{math.MaxInt64, "9223372036854775807"},
		{math.MinInt64, "-9223372036854775808"},
	} {
		if got := FromInt64(c.v).String(); got != c.want {
			t.Errorf("FromInt64(%d) = %q, want %q", c.v, got, c.want)
		}
	}
	if n := FromInt64(0); n.neg || n.Sign() != 0 {
		t.Error("FromInt64(0) has sign")
	}
}

// randNumber возвращает каноническую запись случайного числа и сам литерал.
func randNumber(r *rand.Rand) string {
	total := 1 + r.IntN(MaxDigits)
	digits := make([]byte, total)
	digits[0] = byte('1' + r.IntN(9))
	for i := 1; i < total; i++ {
		digits[i] = byte('0' + r.IntN(10))
	}
	frac := r.IntN(min(total+3, MaxScale) + 1)
	var s string
	if frac >= total {
		s = "0." + strings.Repeat("0", frac-total) + string(digits)
	} else if frac == 0 {
		s = string(digits)
	} else {
		s = string(digits[:total-frac]) + "." + string(digits[total-frac:])
	}
	if r.IntN(2) == 0 {
		s = "-" + s
	}
	return s
}

// Свойства на детерминированной выборке: текстовый round-trip, (a+b)-b == a,
// антисимметрия Cmp, масштаб суммы, согласованность Round с Truncate.
func TestProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var ok, overflow int
	for i := 0; i < 20000; i++ {
		sa, sb := randNumber(r), randNumber(r)
		a, b := mustParse(t, sa), mustParse(t, sb)
		if a.String() != sa || b.String() != sb {
			t.Fatalf("round-trip: %q -> %q, %q -> %q", sa, a.String(), sb, b.String())
		}
		if a.Cmp(b) != -b.Cmp(a) {
			t.Fatalf("Cmp antisymmetry: %s, %s", sa, sb)
		}
		sum, err := a.Add(b)
		if err != nil {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("%s + %s: %v", sa, sb, err)
			}
			overflow++
			continue
		}
		ok++
		if want := max(a.Scale(), b.Scale()); sum.Scale() != want {
			t.Fatalf("%s + %s: scale %d, want %d", sa, sb, sum.Scale(), want)
		}
		back, err := sum.Sub(b)
		if err != nil || back.Cmp(a) != 0 {
			t.Fatalf("(%s + %s) - %s = %q, %v", sa, sb, sb, back.String(), err)
		}
		if sum.Cmp(sum.Neg().Neg()) != 0 || sum.Sign() != -sum.Neg().Sign() {
			t.Fatalf("Neg: %s", sum.String())
		}
		d := r.IntN(MaxScale + 1)
		tr, err := a.Round(d, RoundTruncate)
		if err != nil {
			t.Fatalf("Round(%s, %d): %v", sa, d, err)
		}
		if absCmp(tr, a) > 0 {
			t.Fatalf("Truncate(%s, %d) = %s больше по модулю", sa, d, tr.String())
		}
		if tr.Scale() > a.Scale() || (a.Scale() > d && tr.Scale() != d) {
			t.Fatalf("Truncate(%s, %d) = %s: масштаб", sa, d, tr.String())
		}
	}
	if ok < 5000 || overflow == 0 {
		t.Fatalf("выборка вырождена: ok=%d overflow=%d", ok, overflow)
	}
}

func absCmp(a, b Number) int {
	if a.Sign() < 0 {
		a = a.Neg()
	}
	if b.Sign() < 0 {
		b = b.Neg()
	}
	return a.Cmp(b)
}

// Точный allocation-тест: после прогрева на подготовленных данных Add, Sub,
// Cmp и Round не выделяют память; результат проверяется. Разбор и печать
// измеряются отдельно и не входят в обещание.
func TestZeroAllocs(t *testing.T) {
	a, b := mustParse(t, "12345.678"), mustParse(t, "0.002")
	var sum Number
	var err error
	if n := testing.AllocsPerRun(1000, func() { sum, err = a.Add(b) }); n != 0 {
		t.Errorf("Add allocs = %v, want 0", n)
	}
	if err != nil || sum.String() != "12345.680" {
		t.Errorf("Add result = %q, %v", sum.String(), err)
	}
	var diff Number
	if n := testing.AllocsPerRun(1000, func() { diff, err = a.Sub(b) }); n != 0 {
		t.Errorf("Sub allocs = %v, want 0", n)
	}
	if err != nil || diff.String() != "12345.676" {
		t.Errorf("Sub result = %q, %v", diff.String(), err)
	}
	var c int
	if n := testing.AllocsPerRun(1000, func() { c = a.Cmp(b) }); n != 0 || c != 1 {
		t.Errorf("Cmp allocs = %v, result = %d", n, c)
	}
	var rd Number
	if n := testing.AllocsPerRun(1000, func() { rd, err = a.Round(1, RoundHalfAwayFromZero) }); n != 0 {
		t.Errorf("Round allocs = %v, want 0", n)
	}
	if err != nil || rd.String() != "12345.7" {
		t.Errorf("Round result = %q, %v", rd.String(), err)
	}
}

var benchSink Number

func BenchmarkAdd(b *testing.B) {
	x := mustParse(b, "12345.678")
	acc := FromInt64(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		acc, err = acc.Add(x)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	benchSink = acc
	// Сумма b.N слагаемых: проверяем результат, а не только скорость.
	want, err := FromInt64(int64(b.N)).Add(FromInt64(0))
	if err != nil {
		b.Fatal(err)
	}
	if acc.Cmp(want) < 0 {
		b.Fatalf("неожиданный результат %q", acc.String())
	}
}
