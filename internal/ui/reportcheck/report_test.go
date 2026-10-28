// Пакет проверяет окно отчёта (ui.OpenReport) на headless-backend goWidgets с настоящим ядром (SQLite, платформа, BSL
// «Малого склада», запрос отчёта из метаданных). Отдельный пакет: у headless-backend глобальное состояние таблицы, а
// поток интерфейса закреплён за горутиной теста (как в internal/ui/catalogcheck).
package reportcheck

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unxed/goWidgets"
	_ "github.com/unxed/goWidgets/backends/headless"
	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/report"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/ui"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

const param = "stock-report/f/Товар"

func TestReportWindowHeadless(t *testing.T) {
	t.Setenv("goWidgets_BACKEND", "headless")
	app, err := goWidgets.NewApp()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../../testdata/configs/small-stock")); err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Create(filepath.Join(t.TempDir(), "db.sqlite"), model)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	num := func(s string) value.Number {
		n, err := value.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, code := range []string{"A", "B"} {
		if _, err := store.Insert(ctx, "goods", map[string]any{"goods-code": code, "goods-title": "т", "goods-unit": "шт", "goods-active": true, "goods-min": num("0")}); err != nil {
			t.Fatal(err)
		}
	}
	pl, err := platform.Open(store, dir, platform.Options{})
	if err != nil {
		t.Fatal(err)
	}
	post := func(kind, number string, lines ...[2]string) int64 {
		t.Helper()
		var rows []map[string]any
		for _, l := range lines {
			rows = append(rows, map[string]any{kind + "-product": value.Ref{Catalog: "goods", ID: int64(l[0][0] - 'A' + 1)}, kind + "-qty": num(l[1])})
		}
		other := map[string]string{"receipt": "receipt-comment", "issue": "issue-recipient"}[kind]
		id, err := store.InsertDocument(ctx, kind, map[string]any{kind + "-number": number, kind + "-date": "2026-10-28", other: "x"}, map[string][]map[string]any{kind + "-items": rows})
		if err != nil {
			t.Fatal(err)
		}
		if err := pl.Post(ctx, kind, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	rep, err := report.Load(model, "stock-report")
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	view, err := ui.OpenReport(app, formsbackend.OpenReport(store, rep), "Остатки", func() { done++ })
	if err != nil {
		t.Fatal(err)
	}
	if view.Table().Fallback() || len(view.Table().Rows()) != 0 || view.Button(ui.BtnRun) == nil || view.Params().Edit(param) == nil {
		t.Fatal("the result is not a native table, is not empty, or the window lacks the controls")
	}
	run := func() { view.Button(ui.BtnRun).Clicked.Emit(goWidgets.ClickInfo{}) }
	rows := func() [][]string { return view.Table().Rows() }
	label := func() string { return view.Params().FormErrorLabel().Text.Get() }

	// пустой склад
	run()
	if len(rows()) != 0 || label() != "" {
		t.Fatalf("empty store: rows=%v label=%q", rows(), label())
	}
	// несколько товаров, дробные количества
	post("receipt", "R-1", [2]string{"A", "10.500"}, [2]string{"B", "0.125"})
	run()
	if want := [][]string{{"1", "10.500"}, {"2", "0.125"}}; !reflect.DeepEqual(rows(), want) {
		t.Fatalf("after the receipt: %v", rows())
	}
	// результат обновляется только командой: S03 после выдачи
	issue := post("issue", "I-1", [2]string{"A", "2.250"})
	if len(rows()) != 2 || rows()[0][1] != "10.500" {
		t.Fatalf("the table changed without a command: %v", rows())
	}
	run()
	if want := [][]string{{"1", "8.250"}, {"2", "0.125"}}; !reflect.DeepEqual(rows(), want) {
		t.Fatalf("S03: %v", rows())
	}
	// фильтр по товару и неверный параметр
	view.Params().Edit(param).Text.Set("2")
	run()
	if want := [][]string{{"2", "0.125"}}; !reflect.DeepEqual(rows(), want) {
		t.Fatalf("filter 2: %v", rows())
	}
	view.Params().Edit(param).Text.Set("x")
	run()
	if got := view.Params().ErrorLabel(param).Text.Get(); got == "" || len(rows()) != 1 {
		t.Fatalf("invalid parameter: label=%q rows=%v", got, rows())
	}
	// отмена выдачи и пустой фильтр
	if err := pl.Unpost(ctx, "issue", issue); err != nil {
		t.Fatal(err)
	}
	view.Params().Edit(param).Text.Set("")
	run()
	if want := [][]string{{"1", "10.500"}, {"2", "0.125"}}; !reflect.DeepEqual(rows(), want) || view.Params().ErrorLabel(param).Text.Get() != "" {
		t.Fatalf("after unposting: %v", rows())
	}
	// «Закрыть» завершает окно
	view.Button(ui.BtnReportClose).Clicked.Emit(goWidgets.ClickInfo{})
	if done != 1 {
		t.Errorf("done = %d after Close", done)
	}
}
