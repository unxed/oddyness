// Пакет проверяет окно документов (ui.OpenDocuments) на headless-backend goWidgets с настоящим ядром (SQLite, платформа,
// BSL «Малого склада»). Отдельный пакет: у headless-backend глобальное состояние таблиц, а поток интерфейса закреплён
// за горутиной теста (как в internal/ui/catalogcheck). Выбор строки таблицы имитируется событием Table.Selected:
// PickTableRow headless-backend берёт произвольную из нескольких таблиц окна.
package documentscheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/goWidgets"
	_ "github.com/unxed/goWidgets/backends/headless"
	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/ui"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

func TestDocumentsWindowHeadless(t *testing.T) {
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
	zero, _ := value.Parse("0")
	for _, code := range []string{"A", "B"} {
		if _, err := store.Insert(ctx, "goods", map[string]any{
			"goods-code": code, "goods-title": "товар " + code, "goods-unit": "шт", "goods-active": true, "goods-min": zero,
		}); err != nil {
			t.Fatal(err)
		}
	}
	pl, err := platform.Open(store, dir, platform.Options{})
	if err != nil {
		t.Fatal(err)
	}
	open := func(id, title string, done *int) *ui.DocumentsView {
		t.Helper()
		be, err := formsbackend.NewDocuments(store, pl, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := range model.Documents {
			if model.Documents[i].ID != id {
				continue
			}
			docs, err := forms.NewDocuments(&model.Documents[i], be)
			if err != nil {
				t.Fatal(err)
			}
			v, err := ui.OpenDocuments(app, docs, title, func() { *done++ })
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		t.Fatalf("no document %q", id)
		return nil
	}
	balance := func() string {
		bs, err := store.Balances(ctx, "stock", sqlite.BalanceQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bs {
			if b.Dimensions["stock-product"].(sqlite.Ref).ID == 1 {
				return b.Resources["stock-qty"].String()
			}
		}
		return "0"
	}
	click := func(v *ui.DocumentsView, id string) {
		t.Helper()
		b := v.Button(id)
		if b == nil {
			t.Fatalf("no button %q", id)
		}
		b.Clicked.Emit(goWidgets.ClickInfo{})
	}
	last := func(row []string) string { return row[len(row)-1] }
	// fill вводит шапку и одну строку так, как это делает пользователь (через виджеты) и записывает строку.
	fill := func(v *ui.DocumentsView, kind, number, product, qty string) {
		t.Helper()
		v.Header().Edit(kind + "/f/" + kind + "-number").Text.Set(number)
		v.Header().Edit(kind + "/f/" + kind + "-date").Text.Set("2026-10-28")
		other := map[string]string{"receipt": "comment", "issue": "recipient"}[kind]
		v.Header().Edit(kind + "/f/" + kind + "-" + other).Text.Set("x")
		v.Line().Edit(kind + "-items/f/" + kind + "-product").Text.Set(product)
		v.Line().Edit(kind + "-items/f/" + kind + "-qty").Text.Set(qty)
		click(v, ui.BtnLineSave)
	}

	rdone, idone := 0, 0
	rv := open("receipt", "Поступления", &rdone)
	if rv.DocumentTable().Fallback() || len(rv.DocumentTable().Rows()) != 0 || len(rv.LineTable().Rows()) != 0 {
		t.Fatal("the lists are not native tables or are not empty")
	}
	// S09 через окно: пустой товар — ошибка поля строки, строка не добавлена
	fill(rv, "receipt", "R-1", "", "10.500")
	if got := rv.Line().ErrorLabel("receipt-items/f/receipt-product").Text.Get(); got != "required" || len(rv.LineTable().Rows()) != 0 {
		t.Fatalf("empty product: label=%q lines=%v", got, rv.LineTable().Rows())
	}
	// S02: исправить, записать строку, сохранить, провести
	rv.Line().Edit("receipt-items/f/receipt-product").Text.Set("1")
	click(rv, ui.BtnLineSave)
	if rows := rv.LineTable().Rows(); len(rows) != 1 || rows[0][1] != "1" || rows[0][2] != "10.500" ||
		rv.Line().Edit("receipt-items/f/receipt-qty").Text.Get() != "" {
		t.Fatalf("lines = %v, qty field %q", rv.LineTable().Rows(), rv.Line().Edit("receipt-items/f/receipt-qty").Text.Get())
	}
	click(rv, ui.BtnSave)
	rows := rv.DocumentTable().Rows()
	if len(rows) != 1 || rows[0][1] != "R-1" || last(rows[0]) != "нет" || rv.DocumentTable().SelectedIndex() != 0 || balance() != "0" {
		t.Fatalf("after Save: rows=%v selected=%d balance=%s", rows, rv.DocumentTable().SelectedIndex(), balance())
	}
	click(rv, ui.BtnPost)
	rows = rv.DocumentTable().Rows()
	if last(rows[0]) != "да" || balance() != "10.500" || rv.Header().FormErrorLabel().Text.Get() != "" {
		t.Fatalf("after Post: rows=%v balance=%s error=%q", rows, balance(), rv.Header().FormErrorLabel().Text.Get())
	}

	// выдача: S04 — отказ ядра виден в строке ошибки, документ записан, но не проведён
	iv := open("issue", "Выдачи", &idone)
	fill(iv, "issue", "I-1", "1", "11.000")
	click(iv, ui.BtnPost)
	if got := iv.Header().FormErrorLabel().Text.Get(); !strings.Contains(got, "Недостаточно товара") || balance() != "10.500" {
		t.Fatalf("rejected issue: label=%q balance=%s", got, balance())
	}
	rows = iv.DocumentTable().Rows()
	if len(rows) != 1 || last(rows[0]) != "нет" {
		t.Fatalf("rejected issue rows = %v", rows)
	}
	// выбрать строку, исправить количество, провести: S03
	iv.LineTable().Selected.Emit(0)
	if got := iv.Line().Edit("issue-items/f/issue-qty").Text.Get(); got != "11.000" {
		t.Fatalf("the picked line was not loaded: qty=%q", got)
	}
	iv.Line().Edit("issue-items/f/issue-qty").Text.Set("2.250")
	click(iv, ui.BtnLineSave)
	if rows := iv.LineTable().Rows(); len(rows) != 1 || rows[0][2] != "2.250" {
		t.Fatalf("replaced line: %v", rows)
	}
	click(iv, ui.BtnPost)
	if got := iv.Header().FormErrorLabel().Text.Get(); got != "" || balance() != "8.250" || last(iv.DocumentTable().Rows()[0]) != "да" {
		t.Fatalf("issue posted: label=%q balance=%s rows=%v", got, balance(), iv.DocumentTable().Rows())
	}
	// S12: правка проведённого — отказ ядра, документ прежний
	iv.Header().Edit("issue/f/issue-recipient").Text.Set("другой цех")
	click(iv, ui.BtnSave)
	if got := iv.Header().FormErrorLabel().Text.Get(); !strings.Contains(got, "cancel the posting first") {
		t.Fatalf("editing a posted document: label=%q", got)
	}

	// S07: выбрать поступление в списке, отменить — отказ (остаток ушёл в выдачу), статус прежний
	rv.DocumentTable().Selected.Emit(0)
	if got := rv.Header().Edit("receipt/f/receipt-number").Text.Get(); got != "R-1" {
		t.Fatalf("the picked document was not loaded: number=%q", got)
	}
	click(rv, ui.BtnUnpost)
	if got := rv.Header().FormErrorLabel().Text.Get(); got == "" || last(rv.DocumentTable().Rows()[0]) != "да" || balance() != "8.250" {
		t.Fatalf("rejected unpost: label=%q rows=%v balance=%s", got, rv.DocumentTable().Rows(), balance())
	}
	// S06: отменить выдачу, затем отменить поступление; «Новый документ» очищает карточки
	click(iv, ui.BtnUnpost)
	if balance() != "10.500" || last(iv.DocumentTable().Rows()[0]) != "нет" {
		t.Fatalf("issue unposted: balance=%s rows=%v", balance(), iv.DocumentTable().Rows())
	}
	click(rv, ui.BtnUnpost)
	if balance() != "0" || rv.Header().FormErrorLabel().Text.Get() != "" || last(rv.DocumentTable().Rows()[0]) != "нет" {
		t.Fatalf("receipt unposted: balance=%s label=%q", balance(), rv.Header().FormErrorLabel().Text.Get())
	}
	click(rv, ui.BtnNew)
	if rv.Header().Edit("receipt/f/receipt-number").Text.Get() != "" || len(rv.LineTable().Rows()) != 0 || rv.DocumentTable().SelectedIndex() != -1 {
		t.Errorf("New: number=%q lines=%v", rv.Header().Edit("receipt/f/receipt-number").Text.Get(), rv.LineTable().Rows())
	}
	// «Закрыть» завершает окно
	click(rv, ui.BtnClose)
	click(iv, ui.BtnClose)
	if rdone != 1 || idone != 1 {
		t.Errorf("done: receipt=%d issue=%d", rdone, idone)
	}
}
