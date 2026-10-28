package query

import (
	"reflect"
	"strings"
	"testing"
)

const filtered = "ВЫБРАТЬ Товар, Количество КАК Остаток ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Товар И Количество > &Минимум УПОРЯДОЧИТЬ ПО Товар"

func TestWithoutParams(t *testing.T) {
	s := stockStore(t)
	all := rows(t, run(t, s, "ВЫБРАТЬ Товар, Количество КАК Остаток ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Товар", nil))
	if len(all) < 2 {
		t.Fatalf("fixture rows = %v", all)
	}
	q := mustParse(t, filtered)

	// без параметров вовсе: условия вырезаны, остаётся полный список
	free, err := q.WithoutParams("Товар", "минимум")
	if err != nil || free.Where != nil || len(free.Params) != 0 {
		t.Fatalf("both dropped: %v where=%v params=%v", err, free.Where, free.Params)
	}
	res, err := free.Run(ctx, s, nil)
	if err != nil || !reflect.DeepEqual(rows(t, res), all) {
		t.Fatalf("both dropped: %v rows=%v want %v", err, rows(t, res), all)
	}

	// остаётся только условие по количеству; индекс параметра пересчитан
	byQty, err := q.WithoutParams("Товар")
	if err != nil || len(byQty.Params) != 1 || byQty.Params[0].Name != "Минимум" {
		t.Fatalf("product dropped: %v params=%v", err, byQty.Params)
	}
	res, err = byQty.Run(ctx, s, map[string]any{"Минимум": num(t, "9")})
	if err != nil {
		t.Fatal(err)
	}
	if got := rows(t, res); !reflect.DeepEqual(got, [][2]string{{"goods#2", "9.99"}}) {
		t.Errorf("quantity > 9: %v", got)
	}

	// остаётся только условие по товару
	byProduct, err := q.WithoutParams("Минимум")
	if err != nil || len(byProduct.Params) != 1 || byProduct.Params[0].Name != "Товар" {
		t.Fatalf("minimum dropped: %v", err)
	}
	res, err = byProduct.Run(ctx, s, map[string]any{"Товар": ref(2)})
	if err != nil || !reflect.DeepEqual(rows(t, res), [][2]string{{"goods#2", "9.99"}}) {
		t.Fatalf("product 2: %v %v", err, rows(t, res))
	}

	// исходный запрос не изменился
	if len(q.Params) != 2 || q.Where == nil {
		t.Errorf("the original query changed: params=%v", q.Params)
	}
	if _, err := q.Run(ctx, s, map[string]any{"Товар": ref(2), "Минимум": num(t, "0")}); err != nil {
		t.Errorf("original query no longer runs: %v", err)
	}
	// ничего не вырезать — тот же запрос
	same, err := q.WithoutParams()
	if err != nil || len(same.Params) != 2 || same.Where == nil {
		t.Errorf("WithoutParams(): %v", err)
	}
}

func TestWithoutParamsRefusals(t *testing.T) {
	for _, c := range []struct{ name, src, drop, msg string }{
		{"unknown name", filtered, "Нет", "unknown parameter &Нет"},
		{"under OR", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Товар ИЛИ Количество > 0", "Товар", "under OR or NOT"},
		{"under NOT", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ НЕ Товар = &Товар", "Товар", "under OR or NOT"},
		{"nested under OR in AND", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество > 0 И (Товар = &Товар ИЛИ Количество > 5)", "Товар", "under OR or NOT"},
	} {
		q := mustParse(t, c.src)
		if _, err := q.WithoutParams(c.drop); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %q", c.name, err, c.msg)
		}
	}
	// параметр под ИЛИ не мешает, пока его не вырезают
	q := mustParse(t, "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Товар ИЛИ Количество > 0")
	if out, err := q.WithoutParams(); err != nil || len(out.Params) != 1 {
		t.Errorf("nothing to drop: %v", err)
	}
}
