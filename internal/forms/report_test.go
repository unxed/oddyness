package forms_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/report"
	"github.com/unxed/oddyness/internal/value"
)

const product = "stock-report/f/Товар"

func (e *docEnv) report() *forms.Report {
	e.t.Helper()
	rep, err := report.Load(e.store.Model(), "stock-report")
	if err != nil {
		e.t.Fatal(err)
	}
	return formsbackend.OpenReport(e.store, rep)
}

func mustRun(t *testing.T, r *forms.Report) [][]string {
	t.Helper()
	if err := r.Run(); err != nil {
		t.Fatalf("Run: %v (error %q)", err, r.Error())
	}
	return r.Rows()
}

// Отчёт «Остатки» через форму: пустой склад, несколько товаров, дробные количества, фильтр, обновление после
// проведения и отмены (S03: 10,500 - 2,250 = 8,250), совпадение с остатками ядра.
func TestReportForm(t *testing.T) {
	e := newDocEnv(t)
	r := e.report()
	if r.Ran() || !strings.Contains(r.Snapshot(), "not run") {
		t.Fatalf("a new report must not have a result:\n%s", r.Snapshot())
	}
	if got := mustRun(t, r); len(got) != 0 || !r.Ran() || !reflect.DeepEqual(r.Columns(), []string{"Товар", "Остаток"}) {
		t.Fatalf("empty store: rows=%v columns=%v", got, r.Columns())
	}
	e.fill(e.receipt, "receipt", "R-1", [2]string{"1", "10.500"}, [2]string{"2", "0.125"})
	if err := e.receipt.Post(); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, r); !reflect.DeepEqual(got, [][]string{{"1", "10.500"}, {"2", "0.125"}}) {
		t.Fatalf("after the receipt: %v", got)
	}
	e.fill(e.issue, "issue", "I-1", [2]string{"1", "2.250"})
	if err := e.issue.Post(); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, r); !reflect.DeepEqual(got, [][]string{{"1", "8.250"}, {"2", "0.125"}}) || e.balance(1) != "8.250" {
		t.Fatalf("S03: %v balance=%s", got, e.balance(1))
	}
	// результат хранится до следующего запуска: правка параметра его не меняет
	if err := r.Params().SetText(product, "2"); err != nil {
		t.Fatal(err)
	}
	if len(r.Rows()) != 2 {
		t.Errorf("editing a parameter changed the result: %v", r.Rows())
	}
	if got := mustRun(t, r); !reflect.DeepEqual(got, [][]string{{"2", "0.125"}}) {
		t.Errorf("filter 2: %v", got)
	}
	if err := r.Params().SetText(product, "3"); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, r); len(got) != 0 {
		t.Errorf("filter 3: %v", got)
	}
	if err := r.Params().SetText(product, "  "); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, r); len(got) != 2 {
		t.Errorf("a blank filter must show all: %v", got)
	}
	// неверный параметр: ошибка поля, прежний результат остаётся
	if err := r.Params().SetText(product, "x"); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(); !errors.Is(err, forms.ErrInvalid) || r.Params().Error(product) == "" || len(r.Rows()) != 2 {
		t.Errorf("invalid parameter: err=%v field=%q rows=%v", err, r.Params().Error(product), r.Rows())
	}
	// отмена выдачи возвращает остаток
	if err := r.Params().SetText(product, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.issue.Select(0); err != nil {
		t.Fatal(err)
	}
	if err := e.issue.Unpost(); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, r); !reflect.DeepEqual(got, [][]string{{"1", "10.500"}, {"2", "0.125"}}) {
		t.Errorf("after unposting the issue: %v", got)
	}
	snap := r.Snapshot()
	for _, want := range []string{`report stock-report "Остатки" [open]`, "stock-report/f/Товар edit", "columns: Товар | Остаток", "  1 | 10.500"} {
		if !strings.Contains(snap, want) {
			t.Errorf("snapshot has no %q:\n%s", want, snap)
		}
	}
	r.Close()
	if r.IsOpen() || !errors.Is(r.Run(), forms.ErrClosed) {
		t.Error("Run of a closed report must fail with ErrClosed")
	}
}

type stubBackend struct {
	err   error
	rows  [][]any
	calls int
	got   map[string]any
}

func (b *stubBackend) Run(params map[string]any) ([]string, [][]any, error) {
	b.calls++
	b.got = params
	return []string{"Флаг", "Число", "Ссылка", "Пусто"}, b.rows, b.err
}

// Ошибка ядра очищает результат и остаётся в Error; параметров может не быть; ячейки всех типов.
func TestReportFormBackend(t *testing.T) {
	n, _ := value.Parse("1.50")
	be := &stubBackend{rows: [][]any{{true, n, value.Ref{Catalog: "goods", ID: 7}, nil}, {false, n, nil, nil}}}
	r := forms.NewReport("r", "Р", nil, be)
	if len(r.Params().Controls()) != 0 || r.Params().Focused() != "" {
		t.Fatalf("a report without parameters has controls: %v", r.Params().Controls())
	}
	if err := r.Run(); err != nil || len(be.got) != 0 {
		t.Fatalf("Run: %v params=%v", err, be.got)
	}
	if want := [][]string{{"да", "1.50", "7", ""}, {"нет", "1.50", "", ""}}; !reflect.DeepEqual(r.Rows(), want) {
		t.Errorf("rows = %v, want %v", r.Rows(), want)
	}
	be.err = errors.New("storage is unavailable")
	if err := r.Run(); err == nil || r.Error() != "storage is unavailable" || r.Ran() || len(r.Rows()) != 0 || len(r.Columns()) != 0 {
		t.Errorf("backend error: err=%v error=%q ran=%v rows=%v", err, r.Error(), r.Ran(), r.Rows())
	}
	be.err = nil
	if err := r.Run(); err != nil || r.Error() != "" || !r.Ran() {
		t.Errorf("Run after the error: %v error=%q", err, r.Error())
	}
	// параметр передаётся ссылкой на справочник из описания
	r2 := forms.NewReport("r2", "Р2", []forms.ReportParam{{Name: "Товар", Catalog: "goods"}}, be)
	if err := r2.Params().SetText("r2/f/Товар", "5"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Run(); err != nil || be.got["Товар"] != (value.Ref{Catalog: "goods", ID: 5}) {
		t.Errorf("parameter: %v got=%v", err, be.got)
	}
	if err := r2.Params().SetText("r2/f/Товар", ""); err != nil {
		t.Fatal(err)
	}
	if err := r2.Run(); err != nil || be.got["Товар"] != nil {
		t.Errorf("empty parameter must be nil: %v got=%v", err, be.got)
	}
}
