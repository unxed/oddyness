package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/storage/sqlite"
)

func (e *env) issue(number string, lines ...[2]any) int64 {
	e.t.Helper()
	var rows []map[string]any
	for _, l := range lines {
		rows = append(rows, map[string]any{
			"issue-product": sqlite.Ref{Catalog: "goods", ID: int64(l[0].(int))},
			"issue-qty":     e.num(l[1].(string)),
		})
	}
	id, err := e.store.InsertDocument(ctx, "issue",
		map[string]any{"issue-number": number, "issue-date": "2026-10-28", "issue-recipient": "цех"},
		map[string][]map[string]any{"issue-items": rows})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) issueState(id int64) (posted bool, movements int) {
	e.t.Helper()
	d, err := e.store.GetDocument(ctx, "issue", id)
	if err != nil {
		e.t.Fatal(err)
	}
	rows, err := e.store.Movements(ctx, "stock", "issue", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Posted, len(rows)
}

// S03: поступление 10,500, выдача 2,250 -> остаток 8,250, и после перезапуска тот же.
// S04: выдача 9,000 — отказ; остаток 8,250, документ не проведён, новых движений нет.
func TestIssueS03AndS04(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "receipt", e.receipt("R-1", [2]any{1, "10.500"})); err != nil {
		t.Fatal(err)
	}
	id := e.issue("I-1", [2]any{1, "2.250"})
	if err := pl.Post(ctx, "issue", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250"})

	big := e.issue("I-2", [2]any{1, "9.000"})
	err := pl.Post(ctx, "issue", big)
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "Недостаточно товара") {
		t.Fatalf("issue 9.000: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	if posted, n := e.issueState(big); posted || n != 0 {
		t.Errorf("rejected issue: posted=%v movements=%d", posted, n)
	}

	// перезапуск
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(e.path, e.model)
	if err != nil {
		t.Fatal(err)
	}
	e.store = store
	t.Cleanup(func() { store.Close() })
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	if posted, n := e.issueState(id); !posted || n != 1 {
		t.Errorf("posted issue after restart: posted=%v movements=%d", posted, n)
	}
}

// Повторное проведение выдачи не считает её же движения доступным остатком и не удваивает их.
func TestIssueRepostIsIdempotent(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "receipt", e.receipt("R-1", [2]any{1, "10.500"})); err != nil {
		t.Fatal(err)
	}
	// выдача всего остатка: при повторе собственное движение не должно мешать
	id := e.issue("I-1", [2]any{1, "10.500"})
	for i := 0; i < 3; i++ {
		if err := pl.Post(ctx, "issue", id); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}
	eq(t, e.balances(), map[int64]string{1: "0.000"})
	if _, n := e.issueState(id); n != 1 {
		t.Errorf("movements = %d", n)
	}
}

// S10: две строки одного товара 5,000 + 4,000 при остатке 8,250 — отказ всей операции.
func TestIssueS10SameProductLinesAreSummed(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "receipt", e.receipt("R-1", [2]any{1, "8.250"}, [2]any{2, "1"})); err != nil {
		t.Fatal(err)
	}
	id := e.issue("I-1", [2]any{1, "5.000"}, [2]any{2, "1"}, [2]any{1, "4.000"})
	if err := pl.Post(ctx, "issue", id); !errors.Is(err, ErrRejected) {
		t.Fatalf("5.000 + 4.000 of 8.250: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250", 2: "1"})
	if posted, n := e.issueState(id); posted || n != 0 {
		t.Errorf("partial posting: posted=%v movements=%d", posted, n)
	}
	// те же строки в пределах остатка проходят
	ok := e.issue("I-2", [2]any{1, "5.000"}, [2]any{2, "1"}, [2]any{1, "3.250"})
	if err := pl.Post(ctx, "issue", ok); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "0.000", 2: "0"})
}

// Товар без движений: остаток нуль, выдача отклоняется; пустая выдача проводится без движений.
func TestIssueWithoutStockAndEmpty(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "issue", e.issue("I-1", [2]any{3, "0.001"})); !errors.Is(err, ErrRejected) {
		t.Errorf("issue of a product with no stock: %v", err)
	}
	empty := e.issue("I-2")
	if err := pl.Post(ctx, "issue", empty); err != nil {
		t.Fatal(err)
	}
	if posted, n := e.issueState(empty); !posted || n != 0 {
		t.Errorf("empty issue: posted=%v movements=%d", posted, n)
	}
}

// S12: проведённый документ нельзя изменить или удалить; ссылки, документ и остаток сохранены.
func TestPostedDocumentIsProtected(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	rid := e.receipt("R-1", [2]any{1, "10.500"})
	if err := pl.Post(ctx, "receipt", rid); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{"receipt-number": "R-1", "receipt-date": "2026-10-27", "receipt-comment": "правка"}
	tables := map[string][]map[string]any{"receipt-items": {{"receipt-product": sqlite.Ref{Catalog: "goods", ID: 1}, "receipt-qty": e.num("99")}}}
	if err := e.store.UpdateDocument(ctx, "receipt", rid, header, tables); !errors.Is(err, sqlite.ErrPosted) {
		t.Errorf("update of a posted document: %v", err)
	}
	if err := e.store.DeleteDocument(ctx, "receipt", rid); !errors.Is(err, sqlite.ErrPosted) {
		t.Errorf("delete of a posted document: %v", err)
	}
	// используемый товар удалить нельзя
	if err := e.store.Delete(ctx, "goods", 1); !errors.Is(err, sqlite.ErrInvalid) {
		t.Errorf("delete of a used product: %v", err)
	}
	d, err := e.store.GetDocument(ctx, "receipt", rid)
	if err != nil || !d.Posted || d.Header["receipt-comment"] != "" || d.Tables["receipt-items"][0].Values["receipt-qty"] != any(e.num("10.500")) {
		t.Errorf("document changed: %+v %v", d, err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	// черновик по-прежнему правится и удаляется
	draft := e.receipt("R-2", [2]any{2, "1"})
	if err := e.store.UpdateDocument(ctx, "receipt", draft, map[string]any{"receipt-number": "R-2", "receipt-date": "2026-10-27", "receipt-comment": "ok"}, tables); err != nil {
		t.Errorf("update of a draft: %v", err)
	}
	if err := e.store.DeleteDocument(ctx, "receipt", draft); err != nil {
		t.Errorf("delete of a draft: %v", err)
	}
}

// Функции Остаток и Отказ: ошибки аргументов явные и откатывают проведение.
func TestBalanceAndRejectArguments(t *testing.T) {
	e := newEnv(t)
	for name, c := range map[string]struct{ body, msg string }{
		"unknown register": {`Остаток("Нет", Неопределено);`, `unknown register "Нет"`},
		"arity":            {`Остаток("Запасы");`, "takes 1 dimension values"},
		"type":             {`Остаток("Запасы", 5);`, "expected a reference"},
		"reject type":      {`Отказ(5);`, "message must be a String"},
		"reject":           {`Отказ("нет");`, "нет"},
	} {
		src := "Процедура ПриПроведении(Документ)\n\t" + c.body + "\nКонецПроцедуры\n"
		if strings.HasPrefix(c.body, "Остаток") {
			src = "Процедура ПриПроведении(Документ)\n\tЕсли " + strings.TrimSuffix(c.body, ";") + " = 0 Тогда\n\t\tОтказ(\"x\");\n\tКонецЕсли;\nКонецПроцедуры\n"
		}
		pl, err := e.withHandler(src, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		err = pl.Post(ctx, "receipt", e.receipt("R-"+name, [2]any{1, "1"}))
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: err = %v, want containing %q", name, err, c.msg)
		}
	}
	eq(t, e.balances(), map[int64]string{})
}
