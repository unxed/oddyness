// Пакет проверяет окна справочника и документов под ролью чтения (P19, часть 2) на headless-backend goWidgets с
// настоящим ядром: поля и команды записи недоступны, чтение работает, а команда, выданная мимо окна (событие клика
// кнопки, которая в окне недоступна), получает отказ ядра и ничего не записывает: окно не единственная защита.
// Отдельный пакет: у headless-backend глобальное состояние (как в internal/ui/documentscheck).
package rolecheck

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

// goodsBackend — справочник «Товары» над хранилищем (как в cmd/oddyness).
type goodsBackend struct{ s *sqlite.Store }

func (b goodsBackend) CanWrite() bool { return b.s.CanWrite() }
func (b goodsBackend) List() ([]forms.Row, error) {
	recs, err := b.s.List(ctx, "goods")
	rows := make([]forms.Row, len(recs))
	for i, r := range recs {
		rows[i] = forms.Row{ID: r.ID, Values: r.Values}
	}
	return rows, err
}
func (b goodsBackend) Insert(v map[string]any) (int64, error) { return b.s.Insert(ctx, "goods", v) }
func (b goodsBackend) Update(id int64, v map[string]any) error {
	return b.s.Update(ctx, "goods", id, v)
}

func TestWindowsUnderReadRole(t *testing.T) {
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
	if _, err := store.Insert(ctx, "goods", map[string]any{"goods-code": "K-1", "goods-title": "Кабель", "goods-unit": "м", "goods-active": true, "goods-min": num("0")}); err != nil {
		t.Fatal(err)
	}
	id, err := store.InsertDocument(ctx, "receipt", map[string]any{"receipt-number": "R-1", "receipt-date": "2026-10-28", "receipt-comment": "x"},
		map[string][]map[string]any{"receipt-items": {{"receipt-product": value.Ref{Catalog: "goods", ID: 1}, "receipt-qty": num("10.500")}}})
	if err != nil {
		t.Fatal(err)
	}
	pl, err := platform.Open(store, dir, platform.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Post(ctx, "receipt", id); err != nil {
		t.Fatal(err)
	}
	receipt := func() *forms.Documents {
		t.Helper()
		be, err := formsbackend.NewDocuments(store, pl, "receipt")
		if err != nil {
			t.Fatal(err)
		}
		for i := range model.Documents {
			if model.Documents[i].ID == "receipt" {
				d, err := forms.NewDocuments(&model.Documents[i], be)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
		}
		t.Fatal("no receipt")
		return nil
	}
	state := func() string {
		d, err := store.GetDocument(ctx, "receipt", id)
		if err != nil {
			t.Fatal(err)
		}
		docs, _ := store.ListDocuments(ctx, "receipt")
		recs, _ := store.List(ctx, "goods")
		bs, _ := store.Balances(ctx, "stock", sqlite.BalanceQuery{})
		return strings.Join([]string{
			map[bool]string{true: "posted", false: "draft"}[d.Posted], d.Header["receipt-comment"].(string),
			strings.Repeat("d", len(docs)), strings.Repeat("g", len(recs)), bs[0].Resources["stock-qty"].String(),
		}, "|")
	}
	before := state()

	store.SetRole(sqlite.RoleReader)
	docs := receipt()
	if !docs.ReadOnly() {
		t.Fatal("a document window under the read role must be read-only")
	}
	doneD := 0
	dv, err := ui.OpenDocuments(app, docs, "Поступления", func() { doneD++ })
	if err != nil {
		t.Fatal(err)
	}
	enabled := func(b *goWidgets.Button) bool { return b.Enabled.Get() }
	for _, bid := range []string{ui.BtnNew, ui.BtnSave, ui.BtnPost, ui.BtnUnpost, ui.BtnLineSave, ui.BtnLineNew, ui.BtnLineDelete} {
		if enabled(dv.Button(bid)) {
			t.Errorf("button %q is enabled under the read role", bid)
		}
	}
	if !enabled(dv.Button(ui.BtnClose)) {
		t.Error("«Закрыть» must stay enabled")
	}
	for _, id := range []string{"receipt/f/receipt-number", "receipt/f/receipt-date", "receipt/f/receipt-comment"} {
		if dv.Header().Edit(id).Enabled.Get() {
			t.Errorf("header field %q is editable under the read role", id)
		}
	}
	if dv.Line().Edit("receipt-items/f/receipt-qty").Enabled.Get() {
		t.Error("the line field is editable under the read role")
	}
	// чтение работает: список и выбор документа
	if rows := dv.DocumentTable().Rows(); len(rows) != 1 || rows[0][1] != "R-1" || rows[0][len(rows[0])-1] != "да" {
		t.Fatalf("the document list = %v", rows)
	}
	dv.DocumentTable().Selected.Emit(0)
	if got := dv.Header().Edit("receipt/f/receipt-number").Text.Get(); got != "R-1" || len(dv.LineTable().Rows()) != 1 {
		t.Fatalf("select under the read role: number=%q lines=%v", got, dv.LineTable().Rows())
	}
	// команды мимо окна: клик недоступных кнопок получает отказ ядра, ничего не записано
	for _, c := range []struct{ button, what string }{
		{ui.BtnSave, "save"}, {ui.BtnUnpost, "unpost"}, {ui.BtnPost, "post"}, {ui.BtnNew, "new"}, // «Новый» последним: он очищает форму
	} {
		dv.Header().Edit("receipt/f/receipt-comment").Text.Set("changed")
		dv.Button(c.button).Clicked.Emit(goWidgets.ClickInfo{})
		if c.button == ui.BtnNew {
			continue // «Новый документ» только очищает форму, записи не делает
		}
		if got := dv.Header().FormErrorLabel().Text.Get(); !strings.Contains(got, "not permitted for the session role") {
			t.Errorf("%s outside the window: label=%q", c.what, got)
		}
		if got := state(); got != before {
			t.Fatalf("%s outside the window changed the data: %s -> %s", c.what, before, got)
		}
	}
	if got := state(); got != before {
		t.Errorf("the read role changed the data: %s -> %s", before, got)
	}
	dv.Button(ui.BtnClose).Clicked.Emit(goWidgets.ClickInfo{})
	if doneD != 1 {
		t.Errorf("«Закрыть» under the read role: done=%d", doneD)
	}

	// справочник: список и карточка читаются, запись недоступна, клик мимо окна — отказ ядра
	cat, err := forms.NewCatalog(&model.Catalogs[0], goodsBackend{store})
	if err != nil {
		t.Fatal(err)
	}
	if !cat.ReadOnly() {
		t.Fatal("a catalog window under the read role must be read-only")
	}
	doneC := 0
	cv, err := ui.OpenCatalog(app, cat, "Товары", func() { doneC++ })
	if err != nil {
		t.Fatal(err)
	}
	if cv.NewButton().Enabled.Get() || cv.Button("goods/b/save").Enabled.Get() || !cv.Button("goods/b/close").Enabled.Get() || cv.Edit("goods/f/goods-code").Enabled.Get() {
		t.Error("the catalog window controls do not follow the read role")
	}
	if rows := cv.Table().Rows(); len(rows) != 1 || rows[0][1] != "K-1" {
		t.Fatalf("the catalog list = %v", rows)
	}
	cv.Edit("goods/f/goods-code").Text.Set("K-2")
	cv.Edit("goods/f/goods-title").Text.Set("Труба")
	cv.Edit("goods/f/goods-unit").Text.Set("м")
	cv.Edit("goods/f/goods-min").Text.Set("0")
	cv.Button("goods/b/save").Clicked.Emit(goWidgets.ClickInfo{})
	if got := cv.FormErrorLabel().Text.Get(); !strings.Contains(got, "not permitted for the session role") {
		t.Errorf("catalog save outside the window: label=%q", got)
	}
	if got := state(); got != before {
		t.Errorf("a catalog save outside the window changed the data: %s -> %s", before, got)
	}
	cv.Button("goods/b/close").Clicked.Emit(goWidgets.ClickInfo{})
	if doneC != 1 {
		t.Errorf("catalog «Закрыть»: done=%d", doneC)
	}

	// роль оператора: окна полностью доступны
	store.SetRole(sqlite.RoleOperator)
	opDocs := receipt()
	ov, err := ui.OpenDocuments(app, opDocs, "Поступления (оператор)", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if opDocs.ReadOnly() || !enabled(ov.Button(ui.BtnSave)) || !enabled(ov.Button(ui.BtnPost)) || !ov.Header().Edit("receipt/f/receipt-number").Enabled.Get() {
		t.Error("the operator window must be fully enabled")
	}
}
