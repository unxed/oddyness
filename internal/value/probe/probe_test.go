// Package probe — разовое сравнение decimal-кандидатов для P03. Это отдельный
// модуль: зависимости пробы не попадают в go.mod продукта. Результаты
// (журнал TestCandidates и benchmem) записаны в docs/implementation/numbers.md.
package probe

import (
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"
	govalues "github.com/govalues/decimal"
	"github.com/shopspring/decimal"
	"github.com/unxed/oddyness/internal/value"
)

type candidate struct {
	name  string
	parse func(s string) (string, error)
	add   func(a, b string) (string, error)
}

func apdCtx() *apd.Context {
	// Точность 32 цифры; потеря цифр (Rounded/Inexact) — ошибка, как требует контракт.
	c := apd.BaseContext.WithPrecision(32)
	c.Traps |= apd.Inexact | apd.Rounded
	return c
}

func apdParse(s string) (*apd.Decimal, error) {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		return nil, err
	}
	out := new(apd.Decimal)
	if _, err := apdCtx().Round(out, d); err != nil {
		return nil, err
	}
	return out, nil
}

var candidates = []candidate{
	{
		name: "own value.Number",
		parse: func(s string) (string, error) {
			n, err := value.Parse(s)
			return n.String(), err
		},
		add: func(a, b string) (string, error) {
			x, err := value.Parse(a)
			if err != nil {
				return "", err
			}
			y, err := value.Parse(b)
			if err != nil {
				return "", err
			}
			r, err := x.Add(y)
			return r.String(), err
		},
	},
	{
		name: "cockroachdb/apd precision 32",
		parse: func(s string) (string, error) {
			d, err := apdParse(s)
			if err != nil {
				return "", err
			}
			return d.Text('f'), nil
		},
		add: func(a, b string) (string, error) {
			x, err := apdParse(a)
			if err != nil {
				return "", err
			}
			y, err := apdParse(b)
			if err != nil {
				return "", err
			}
			r := new(apd.Decimal)
			_, err = apdCtx().Add(r, x, y)
			return r.Text('f'), err
		},
	},
	{
		name: "shopspring/decimal",
		parse: func(s string) (string, error) {
			d, err := decimal.NewFromString(s)
			return d.String(), err
		},
		add: func(a, b string) (string, error) {
			x, err := decimal.NewFromString(a)
			if err != nil {
				return "", err
			}
			y, err := decimal.NewFromString(b)
			if err != nil {
				return "", err
			}
			return x.Add(y).String(), nil
		},
	},
	{
		name: "govalues/decimal",
		parse: func(s string) (string, error) {
			d, err := govalues.Parse(s)
			return d.String(), err
		},
		add: func(a, b string) (string, error) {
			x, err := govalues.Parse(a)
			if err != nil {
				return "", err
			}
			y, err := govalues.Parse(b)
			if err != nil {
				return "", err
			}
			r, err := x.Add(y)
			return r.String(), err
		},
	},
}

// TestCandidates печатает таблицу исходов NUM-01/NUM-02 для каждого кандидата.
// Тест не ограничивает кандидатов ожиданиями: это измерение, а не контракт;
// падает только выбранная собственная реализация (см. internal/value).
func TestCandidates(t *testing.T) {
	var lits []string
	for _, n := range []int{19, 20, 31, 32, 33} {
		lits = append(lits, strings.Repeat("1234567890", 4)[:n])
	}
	lits = append(lits, "-0", "0.30")
	adds := [][2]string{
		{"0.1", "0.2"},
		{"9.99", "0.01"},
		{strings.Repeat("9", 32), "1"},
		{strings.Repeat("9", 20), "1"},
	}
	for _, c := range candidates {
		for _, s := range lits {
			out, err := c.parse(s)
			t.Logf("RESULT parse | %s | %s | -> %q | err=%v", c.name, s, out, err)
		}
		for _, p := range adds {
			out, err := c.add(p[0], p[1])
			t.Logf("RESULT add | %s | %s + %s | -> %q | err=%v", c.name, p[0], p[1], out, err)
		}
	}
	// Собственная реализация обязана соответствовать контракту.
	own := candidates[0]
	if out, err := own.parse(lits[3]); err != nil || out != lits[3] {
		t.Errorf("own 32 digits: %q, %v", out, err)
	}
	if _, err := own.parse(lits[4]); err == nil {
		t.Error("own 33 digits accepted")
	}
	if out, err := own.add("0.1", "0.2"); err != nil || out != "0.3" {
		t.Errorf("own 0.1+0.2 = %q, %v", out, err)
	}
}

func BenchmarkAddOwn(b *testing.B) {
	x, _ := value.Parse("12345.678")
	y, _ := value.Parse("0.002")
	var r value.Number
	var err error
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, err = x.Add(y)
	}
	want, _ := value.Parse("12345.68")
	if err != nil || r.Cmp(want) != 0 {
		b.Fatalf("result %q, %v", r.String(), err)
	}
}

func BenchmarkAddApd(b *testing.B) {
	x, _ := apdParse("12345.678")
	y, _ := apdParse("0.002")
	want, _ := apdParse("12345.68")
	ctx := apdCtx()
	r := new(apd.Decimal)
	var err error
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err = ctx.Add(r, x, y)
	}
	if err != nil || r.Cmp(want) != 0 {
		b.Fatalf("result %s, %v", r.Text('f'), err)
	}
}

func BenchmarkAddShopspring(b *testing.B) {
	x, _ := decimal.NewFromString("12345.678")
	y, _ := decimal.NewFromString("0.002")
	want, _ := decimal.NewFromString("12345.68")
	var r decimal.Decimal
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r = x.Add(y)
	}
	if !r.Equal(want) {
		b.Fatalf("result %s", r.String())
	}
}

func BenchmarkAddGovalues(b *testing.B) {
	x, _ := govalues.Parse("12345.678")
	y, _ := govalues.Parse("0.002")
	want, _ := govalues.Parse("12345.68")
	var r govalues.Decimal
	var err error
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, err = x.Add(y)
	}
	if err != nil || r.Cmp(want) != 0 {
		b.Fatalf("result %s, %v", r.String(), err)
	}
}
