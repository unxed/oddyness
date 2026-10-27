package sqlite

import (
	"errors"
	"fmt"
	"testing"

	"github.com/unxed/oddyness/internal/value"
)

func mv(t testing.TB, product int64, qty string) map[string]any {
	return map[string]any{"stock-product": Ref{Catalog: "goods", ID: product}, "stock-qty": num(t, qty)}
}

// registerFixture: два товара (1, 2), два поступления и одна выдача.
func registerFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := openTemp(t)
	seedGoods(t, s, 3)
	for _, n := range []string{"R-1", "R-2"} {
		if _, err := s.InsertDocument(ctx, "receipt", receiptHeader(n, "2026-10-27"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertDocument(ctx, "issue", map[string]any{"issue-number": "I-1", "issue-date": "2026-10-28", "issue-recipient": "цех"}, nil); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func balanceMap(t *testing.T, bs []Balance) map[int64]string {
	t.Helper()
	out := map[int64]string{}
	for _, b := range bs {
		out[b.Dimensions["stock-product"].(Ref).ID] = b.Resources["stock-qty"].String()
	}
	return out
}

func wantBalances(t *testing.T, got []Balance, want map[int64]string) {
	t.Helper()
	m := balanceMap(t, got)
	if len(m) != len(want) {
		t.Errorf("balances = %v, want %v", m, want)
		return
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("balances = %v, want %v", m, want)
			return
		}
	}
}

// Движения → остатки: два поступления и выдача, дробные количества суммируются точно.
func TestMovementsToBalances(t *testing.T) {
	s, _ := registerFixture(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.ReplaceMovements(ctx, "stock", "receipt", 1, []map[string]any{mv(t, 1, "10.500"), mv(t, 2, "2.250"), mv(t, 3, "0.1")}))
	must(s.ReplaceMovements(ctx, "stock", "receipt", 2, []map[string]any{mv(t, 1, "0.125"), mv(t, 1, "0.1"), mv(t, 3, "0.2")}))
	must(s.ReplaceMovements(ctx, "stock", "issue", 1, []map[string]any{mv(t, 1, "-2.250")}))
	bs, err := s.Balances(ctx, "stock", BalanceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	// 10.500 + 0.125 + 0.1 - 2.250 = 8.475; 0.1 + 0.2 = 0.3 точно
	wantBalances(t, bs, map[int64]string{1: "8.475", 2: "2.250", 3: "0.3"})
	if bs[0].Dimensions["stock-product"].(Ref).ID != 1 || bs[2].Dimensions["stock-product"].(Ref).ID != 3 {
		t.Errorf("default order is not by dimension: %v", balanceMap(t, bs))
	}
	// отбор по измерению
	one, err := s.Balances(ctx, "stock", BalanceQuery{Filter: map[string]any{"stock-product": Ref{Catalog: "goods", ID: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	wantBalances(t, one, map[int64]string{2: "2.250"})
	none, err := s.Balances(ctx, "stock", BalanceQuery{Filter: map[string]any{"stock-product": nil}})
	if err != nil || len(none) != 0 {
		t.Errorf("filter by empty reference: %v, %v", none, err)
	}
	// строки движения нумеруются с единицы
	rows, err := s.Movements(ctx, "stock", "receipt", 2)
	if err != nil || len(rows) != 3 || rows[0].ID != 1 || rows[2].ID != 3 || rows[1].Values["stock-qty"].(value.Number).String() != "0.1" {
		t.Errorf("Movements = %+v, %v", rows, err)
	}
}

func TestReplaceMovementsIsASetReplacement(t *testing.T) {
	s, _ := registerFixture(t)
	set := []map[string]any{mv(t, 1, "5"), mv(t, 2, "3")}
	for i := 0; i < 3; i++ { // повторная замена тем же набором не удваивает остатки
		if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, set); err != nil {
			t.Fatal(err)
		}
	}
	bs, _ := s.Balances(ctx, "stock", BalanceQuery{})
	wantBalances(t, bs, map[int64]string{1: "5", 2: "3"})
	// замена другим набором убирает прежние движения
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, []map[string]any{mv(t, 1, "1.5")}); err != nil {
		t.Fatal(err)
	}
	bs, _ = s.Balances(ctx, "stock", BalanceQuery{})
	wantBalances(t, bs, map[int64]string{1: "1.5"})
	// движения других регистраторов не затронуты
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 2, []map[string]any{mv(t, 2, "4")}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, nil); err != nil {
		t.Fatal(err)
	}
	bs, _ = s.Balances(ctx, "stock", BalanceQuery{})
	wantBalances(t, bs, map[int64]string{2: "4"})
}

func TestMovementsRollback(t *testing.T) {
	s, _ := registerFixture(t)
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, []map[string]any{mv(t, 1, "10")}); err != nil {
		t.Fatal(err)
	}
	original := func() {
		t.Helper()
		bs, _ := s.Balances(ctx, "stock", BalanceQuery{})
		wantBalances(t, bs, map[int64]string{1: "10"})
	}
	// ошибка базы на второй строке (товара 999 нет) откатывает и удаление прежнего набора
	err := s.ReplaceMovements(ctx, "stock", "receipt", 1, []map[string]any{mv(t, 2, "1"), mv(t, 999, "1")})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing product: %v", err)
	}
	original()
	// проверка значений до записи
	for name, rows := range map[string][]map[string]any{
		"precision":     {mv(t, 1, "0.0001")},
		"missing field": {{"stock-product": Ref{Catalog: "goods", ID: 1}}},
		"unknown field": {{"stock-product": Ref{Catalog: "goods", ID: 1}, "stock-qty": num(t, "1"), "x": 1}},
		"empty product": {{"stock-qty": num(t, "1")}},
		"quantity type": {{"stock-product": Ref{Catalog: "goods", ID: 1}, "stock-qty": "1"}},
	} {
		if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, rows); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	original()
	// Tx: внутри транзакции видны новые движения, ошибка снимает всё
	boom := errors.New("boom")
	err = s.Tx(ctx, func(tx *Tx) error {
		if err := tx.ReplaceMovements("stock", "receipt", 1, []map[string]any{mv(t, 2, "7")}); err != nil {
			return err
		}
		inside, err := tx.Balances("stock", BalanceQuery{})
		if err != nil {
			return err
		}
		wantBalances(t, inside, map[int64]string{2: "7"})
		rows, err := tx.Movements("stock", "receipt", 1)
		if err != nil || len(rows) != 1 {
			t.Errorf("Tx.Movements = %v, %v", rows, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v", err)
	}
	original()
	// успешная Tx фиксирует несколько замен вместе
	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.ReplaceMovements("stock", "receipt", 1, []map[string]any{mv(t, 1, "2")}); err != nil {
			return err
		}
		return tx.ReplaceMovements("stock", "receipt", 2, []map[string]any{mv(t, 1, "3")})
	}); err != nil {
		t.Fatal(err)
	}
	bs, _ := s.Balances(ctx, "stock", BalanceQuery{})
	wantBalances(t, bs, map[int64]string{1: "5"})
}

func TestMovementsSurviveRestart(t *testing.T) {
	m := loadModel(t)
	s, path := registerFixture(t)
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, []map[string]any{mv(t, 1, "10.500"), mv(t, 1, "0.250")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	bs, err := s2.Balances(ctx, "stock", BalanceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	wantBalances(t, bs, map[int64]string{1: "10.750"})
}

func TestMovementsRecorderAndRegisterChecks(t *testing.T) {
	s, _ := registerFixture(t)
	row := []map[string]any{mv(t, 1, "1")}
	if err := s.ReplaceMovements(ctx, "nope", "receipt", 1, row); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown register: %v", err)
	}
	if err := s.ReplaceMovements(ctx, "stock", "nope", 1, row); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown recorder kind: %v", err)
	}
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 99, row); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown recorder: %v", err)
	}
	if _, err := s.Balances(ctx, "nope", BalanceQuery{}); !errors.Is(err, ErrUnknown) {
		t.Errorf("Balances of an unknown register: %v", err)
	}
	if _, err := s.Movements(ctx, "nope", "receipt", 1); !errors.Is(err, ErrUnknown) {
		t.Errorf("Movements of an unknown register: %v", err)
	}
	for name, q := range map[string]BalanceQuery{
		"filter by a resource": {Filter: map[string]any{"stock-qty": num(t, "1")}},
		"filter type":          {Filter: map[string]any{"stock-product": "x"}},
		"order by a dimension": {OrderBy: "stock-product"},
	} {
		if _, err := s.Balances(ctx, "stock", q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// удаление документа убирает его движения
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, row); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDocument(ctx, "receipt", 1); err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.Balances(ctx, "stock", BalanceQuery{}); len(bs) != 0 {
		t.Errorf("movements of a deleted document survived: %v", balanceMap(t, bs))
	}
}

// Сортировка чисел — в ядре по значению: текстовый порядок дал бы 10.5, 100, 0.5, 9.99.
func TestBalancesOrderNumbersByValue(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 5)
	if _, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), nil); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{mv(t, 1, "10.5"), mv(t, 2, "9.99"), mv(t, 3, "100"), mv(t, 4, "0.5"), mv(t, 5, "1"), mv(t, 5, "-1")}
	if err := s.ReplaceMovements(ctx, "stock", "receipt", 1, rows); err != nil {
		t.Fatal(err)
	}
	order := func(q BalanceQuery) string {
		t.Helper()
		bs, err := s.Balances(ctx, "stock", q)
		if err != nil {
			t.Fatal(err)
		}
		var out string
		for _, b := range bs {
			out += fmt.Sprintf("%d=%s ", b.Dimensions["stock-product"].(Ref).ID, b.Resources["stock-qty"])
		}
		return out
	}
	if got, want := order(BalanceQuery{OrderBy: "stock-qty"}), "5=0 4=0.5 2=9.99 1=10.5 3=100 "; got != want {
		t.Errorf("ascending = %q, want %q", got, want)
	}
	if got, want := order(BalanceQuery{OrderBy: "stock-qty", Desc: true}), "3=100 1=10.5 2=9.99 4=0.5 5=0 "; got != want {
		t.Errorf("descending = %q, want %q", got, want)
	}
	if got, want := order(BalanceQuery{OrderBy: "stock-qty", Nonzero: true}), "4=0.5 2=9.99 1=10.5 3=100 "; got != want {
		t.Errorf("non-zero = %q, want %q", got, want)
	}
	if got, want := order(BalanceQuery{}), "1=10.5 2=9.99 3=100 4=0.5 5=0 "; got != want {
		t.Errorf("by dimension = %q, want %q", got, want)
	}
}
