package forms_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

// docEnv — настоящее ядро (SQLite, платформа, BSL из конфигурации «Малый склад») с тремя товарами и окнами документов.
type docEnv struct {
	t       *testing.T
	store   *sqlite.Store
	receipt *forms.Documents
	issue   *forms.Documents
}

func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../testdata/configs/small-stock")); err != nil {
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
	t.Cleanup(func() { store.Close() })
	zero, _ := value.Parse("0")
	for _, code := range []string{"A", "B", "C"} {
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
	e := &docEnv{t: t, store: store}
	open := func(id string) *forms.Documents {
		be, err := formsbackend.NewDocuments(store, pl, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := range model.Documents {
			if model.Documents[i].ID == id {
				d, err := forms.NewDocuments(&model.Documents[i], be)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
		}
		t.Fatalf("no document %q", id)
		return nil
	}
	e.receipt, e.issue = open("receipt"), open("issue")
	return e
}

// fill вводит шапку (номер, дата, комментарий или получатель) и строки (товар, количество) и записывает строки.
func (e *docEnv) fill(d *forms.Documents, kind, number string, lines ...[2]string) {
	e.t.Helper()
	last := map[string]string{"receipt": "comment", "issue": "recipient"}[kind]
	must := func(err error) {
		e.t.Helper()
		if err != nil {
			e.t.Fatal(err)
		}
	}
	h := d.Header()
	must(h.SetText(kind+"/f/"+kind+"-number", number))
	must(h.SetText(kind+"/f/"+kind+"-date", "2026-10-28"))
	must(h.SetText(kind+"/f/"+kind+"-"+last, "x"))
	for _, l := range lines {
		must(d.Line().SetText(kind+"-items/f/"+kind+"-product", l[0]))
		must(d.Line().SetText(kind+"-items/f/"+kind+"-qty", l[1]))
		must(d.SaveLine())
	}
}

func (e *docEnv) balance(product int64) string {
	e.t.Helper()
	bs, err := e.store.Balances(ctx, "stock", sqlite.BalanceQuery{})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range bs {
		if b.Dimensions["stock-product"].(sqlite.Ref).ID == product {
			return b.Resources["stock-qty"].String()
		}
	}
	return "0"
}

func (e *docEnv) movements(kind string, id int64) int {
	e.t.Helper()
	rows, err := e.store.Movements(ctx, "stock", kind, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(rows)
}

func (e *docEnv) posted(kind string, id int64) bool {
	e.t.Helper()
	d, err := e.store.GetDocument(ctx, kind, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Posted
}

// S02: поступление 10,500 — до проведения остаток 0, после 10,500; S05: повторное проведение остаток не удваивает.
func TestDocumentsReceiptSaveAndPost(t *testing.T) {
	e := newDocEnv(t)
	d := e.receipt
	e.fill(d, "receipt", "R-1", [2]string{"1", "10.500"})
	if got := d.Lines(); len(got) != 1 || got[0][2] != "10.500" || len(d.Rows()) != 0 {
		t.Fatalf("lines=%v rows=%v before Save", got, d.Rows())
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	i, id := d.Selected()
	if i != 0 || id != 1 || d.Posted() || len(d.Rows()) != 1 || d.Rows()[0][len(d.Rows()[0])-1] != "нет" {
		t.Fatalf("after Save: selected=%d id=%d posted=%v rows=%v", i, id, d.Posted(), d.Rows())
	}
	if e.balance(1) != "0" || e.posted("receipt", id) {
		t.Fatalf("a draft changed the stock: balance=%s", e.balance(1))
	}
	// «Сохранить» ещё раз обновляет тот же документ, а не создаёт второй
	if err := d.Save(); err != nil || len(d.Rows()) != 1 {
		t.Fatalf("second Save: %v rows=%v", err, d.Rows())
	}
	if err := d.Post(); err != nil {
		t.Fatal(err)
	}
	if !d.Posted() || e.balance(1) != "10.500" || !e.posted("receipt", id) || d.Rows()[0][len(d.Rows()[0])-1] != "да" || d.Error() != "" {
		t.Fatalf("after Post: posted=%v balance=%s rows=%v error=%q", d.Posted(), e.balance(1), d.Rows(), d.Error())
	}
	if err := d.Post(); err != nil {
		t.Fatal(err)
	}
	if e.balance(1) != "10.500" || e.movements("receipt", id) != 1 {
		t.Errorf("repeated Post: balance=%s movements=%d", e.balance(1), e.movements("receipt", id))
	}
}

// «Провести» нового документа записывает и проводит его одной командой.
func TestDocumentsPostNewDocument(t *testing.T) {
	e := newDocEnv(t)
	e.fill(e.receipt, "receipt", "R-1", [2]string{"2", "3.000"})
	if err := e.receipt.Post(); err != nil {
		t.Fatal(err)
	}
	if !e.receipt.Posted() || e.balance(2) != "3.000" {
		t.Fatalf("posted=%v balance=%s", e.receipt.Posted(), e.balance(2))
	}
}

// S03, S04, S06, S07, S10, S12 через формы выдачи и поступления.
func TestDocumentsIssueScenarios(t *testing.T) {
	e := newDocEnv(t)
	e.fill(e.receipt, "receipt", "R-1", [2]string{"1", "10.500"})
	if err := e.receipt.Post(); err != nil {
		t.Fatal(err)
	}
	// S03
	e.fill(e.issue, "issue", "I-1", [2]string{"1", "2.250"})
	if err := e.issue.Post(); err != nil {
		t.Fatal(err)
	}
	if e.balance(1) != "8.250" {
		t.Fatalf("S03 balance = %s", e.balance(1))
	}
	// S04: 9,000 при остатке 8,250 — отказ ядра, документ записан, но не проведён
	if err := e.issue.New(); err != nil {
		t.Fatal(err)
	}
	e.fill(e.issue, "issue", "I-2", [2]string{"1", "9.000"})
	err := e.issue.Post()
	if !errors.Is(err, platform.ErrRejected) || !strings.Contains(e.issue.Error(), "Недостаточно товара") {
		t.Fatalf("S04: err=%v form error=%q", err, e.issue.Error())
	}
	_, big := e.issue.Selected()
	if e.issue.Posted() || e.posted("issue", big) || e.movements("issue", big) != 0 || e.balance(1) != "8.250" {
		t.Fatalf("S04: posted=%v movements=%d balance=%s", e.issue.Posted(), e.movements("issue", big), e.balance(1))
	}
	if len(e.issue.Lines()) != 1 {
		t.Errorf("S04: the lines were lost: %v", e.issue.Lines())
	}
	// S10: две строки одного товара 5,000 + 4,000 при остатке 8,250 — отказ всей операции
	if err := e.issue.New(); err != nil {
		t.Fatal(err)
	}
	e.fill(e.issue, "issue", "I-3", [2]string{"1", "5.000"}, [2]string{"1", "4.000"})
	if err := e.issue.Post(); !errors.Is(err, platform.ErrRejected) {
		t.Fatalf("S10: %v", err)
	}
	_, two := e.issue.Selected()
	if e.posted("issue", two) || e.movements("issue", two) != 0 || e.balance(1) != "8.250" {
		t.Fatalf("S10: movements=%d balance=%s", e.movements("issue", two), e.balance(1))
	}
	// S12: проведённый документ изменить нельзя — отказ ядра, документ прежний
	if err := e.issue.Select(0); err != nil || !e.issue.Posted() {
		t.Fatalf("select the posted issue: %v posted=%v", err, e.issue.Posted())
	}
	if err := e.issue.Header().SetText("issue/f/issue-recipient", "другой цех"); err != nil {
		t.Fatal(err)
	}
	if err := e.issue.Save(); !errors.Is(err, sqlite.ErrPosted) || e.issue.Error() == "" {
		t.Fatalf("S12: err=%v form error=%q", err, e.issue.Error())
	}
	got, err := e.store.GetDocument(ctx, "issue", 1)
	if err != nil || got.Header["issue-recipient"] != "x" || !got.Posted {
		t.Fatalf("S12: %v %+v", err, got.Header)
	}
	// S06: отменить выдачу и снова провести
	if err := e.issue.Select(0); err != nil {
		t.Fatal(err)
	}
	if err := e.issue.Unpost(); err != nil {
		t.Fatal(err)
	}
	if e.issue.Posted() || e.balance(1) != "10.500" || e.posted("issue", 1) {
		t.Fatalf("S06 after Unpost: posted=%v balance=%s", e.issue.Posted(), e.balance(1))
	}
	if err := e.issue.Post(); err != nil {
		t.Fatal(err)
	}
	if !e.issue.Posted() || e.balance(1) != "8.250" {
		t.Fatalf("S06 after Post: posted=%v balance=%s", e.issue.Posted(), e.balance(1))
	}
	// S07: отменить поступление после выдачи — отказ; статус и движения не изменились
	if err := e.receipt.Select(0); err != nil {
		t.Fatal(err)
	}
	if err := e.receipt.Unpost(); !errors.Is(err, platform.ErrRejected) || e.receipt.Error() == "" {
		t.Fatalf("S07: err=%v form error=%q", err, e.receipt.Error())
	}
	if !e.receipt.Posted() || !e.posted("receipt", 1) || e.movements("receipt", 1) != 1 || e.balance(1) != "8.250" || e.movements("issue", 1) != 1 {
		t.Fatalf("S07: receipt posted=%v balance=%s", e.receipt.Posted(), e.balance(1))
	}
}

// S09: ноль, отрицательное, лишняя дробная цифра, пустой товар — понятные ошибки, данные не испорчены.
func TestDocumentsLineValidation(t *testing.T) {
	e := newDocEnv(t)
	d := e.receipt
	e.fill(d, "receipt", "R-1")
	const product, qty = "receipt-items/f/receipt-product", "receipt-items/f/receipt-qty"
	// ошибки формы строки: строка не добавляется, ввод остаётся
	for _, c := range []struct{ product, qty, field, msg string }{
		{"", "1", product, "required"},
		{"1", "", qty, "required"},
		{"1", "1.2345", qty, "fraction digits"},
		{"x", "1", product, "reference"},
		{"1", "abc", qty, "number"},
	} {
		d.Line().SetText(product, c.product)
		d.Line().SetText(qty, c.qty)
		if err := d.SaveLine(); !errors.Is(err, forms.ErrInvalid) || !strings.Contains(d.Line().Error(c.field), c.msg) || len(d.Lines()) != 0 {
			t.Errorf("%+v: err=%v field error=%q lines=%v", c, err, d.Line().Error(c.field), d.Lines())
		}
	}
	// ноль и отрицательное количество отклоняет ядро при записи документа: документа нет
	for _, q := range []string{"0", "-1"} {
		d.Line().SetText(product, "1")
		d.Line().SetText(qty, q)
		if err := d.SaveLine(); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if err := d.Save(); err == nil || d.Error() == "" || len(d.Rows()) != 0 {
			t.Fatalf("quantity %s: err=%v error=%q rows=%v", q, err, d.Error(), d.Rows())
		}
		d.Line().SetText(product, "")
		d.Line().SetText(qty, "")
		d.SelectLine(0)
		if err := d.DeleteLine(); err != nil {
			t.Fatal(err)
		}
	}
	// неизвестный товар (нет записи 99) и пустая шапка
	d.Line().SetText(product, "99")
	d.Line().SetText(qty, "1")
	if err := d.SaveLine(); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(); err == nil || len(d.Rows()) != 0 {
		t.Fatalf("unknown product: err=%v rows=%v", err, d.Rows())
	}
	if err := d.New(); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(); !errors.Is(err, forms.ErrInvalid) || d.Header().Error("receipt/f/receipt-number") != "required" {
		t.Fatalf("empty header: %v", err)
	}
	if bs, err := e.store.ListDocuments(ctx, "receipt"); err != nil || len(bs) != 0 {
		t.Fatalf("documents in the store: %d, %v", len(bs), err)
	}
}

// Правка строк в памяти: выбор, замена, удаление; ошибки команд.
func TestDocumentsLineCommands(t *testing.T) {
	e := newDocEnv(t)
	d := e.receipt
	e.fill(d, "receipt", "R-1", [2]string{"1", "1"}, [2]string{"2", "2"})
	if err := d.DeleteLine(); !errors.Is(err, forms.ErrNoLine) {
		t.Errorf("DeleteLine without a selection: %v", err)
	}
	if err := d.SelectLine(5); err == nil {
		t.Error("SelectLine out of range")
	}
	if err := d.SelectLine(0); err != nil || d.SelectedLine() != 0 || d.Line().Text("receipt-items/f/receipt-qty") != "1" {
		t.Fatalf("SelectLine(0): %v sel=%d", err, d.SelectedLine())
	}
	d.Line().SetText("receipt-items/f/receipt-qty", "7.500")
	if err := d.SaveLine(); err != nil || len(d.Lines()) != 2 || d.Lines()[0][2] != "7.500" || d.SelectedLine() != -1 {
		t.Fatalf("replace: %v lines=%v sel=%d", err, d.Lines(), d.SelectedLine())
	}
	if err := d.SelectLine(1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteLine(); err != nil || len(d.Lines()) != 1 {
		t.Fatalf("DeleteLine: %v lines=%v", err, d.Lines())
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	// после перечитывания строка в ядре одна, 7.500
	got, err := e.store.GetDocument(ctx, "receipt", 1)
	if err != nil || len(got.Tables["receipt-items"]) != 1 || got.Tables["receipt-items"][0].Values["receipt-qty"].(value.Number).String() != "7.500" {
		t.Fatalf("stored lines: %v %+v", err, got.Tables)
	}
	// «новый документ» очищает шапку и строки, выбор документа загружает их обратно
	if err := d.New(); err != nil || len(d.Lines()) != 0 || d.Header().Text("receipt/f/receipt-number") != "" {
		t.Fatalf("New: %v lines=%v", err, d.Lines())
	}
	if err := d.Select(0); err != nil || len(d.Lines()) != 1 || d.Header().Text("receipt/f/receipt-number") != "R-1" {
		t.Fatalf("Select(0): %v lines=%v", err, d.Lines())
	}
	if err := d.Select(3); err == nil {
		t.Error("Select out of range")
	}
	if err := d.Unpost(); err == nil || !errors.Is(err, platform.ErrNotPosted) {
		t.Errorf("Unpost of a draft: %v", err)
	}
}

func TestDocumentsUnsavedAndClosed(t *testing.T) {
	e := newDocEnv(t)
	d := e.receipt
	if err := d.Unpost(); !errors.Is(err, forms.ErrNotSaved) || d.Error() == "" {
		t.Errorf("Unpost of a new document: %v / %q", err, d.Error())
	}
	snap := d.Snapshot()
	for _, want := range []string{`documents receipt "Поступление" [open]`, "columns: № | Номер | Дата | Комментарий | Проведён", "lines: № | Товар | Количество", "receipt-items/f/receipt-product"} {
		if !strings.Contains(snap, want) {
			t.Errorf("snapshot has no %q:\n%s", want, snap)
		}
	}
	d.Close()
	if d.IsOpen() || !errors.Is(d.Post(), forms.ErrClosed) || !errors.Is(d.Unpost(), forms.ErrClosed) || !errors.Is(d.Select(-1), forms.ErrClosed) {
		t.Error("commands of a closed window must fail with ErrClosed")
	}
}

// Вид документа с несколькими табличными частями — явная ошибка, а не молчаливая потеря частей.
func TestDocumentsSeveralTablePartsUnsupported(t *testing.T) {
	model, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	d := model.Documents[0]
	d.Tables = append(d.Tables, d.Tables[0])
	if _, err := forms.NewDocuments(&d, nil); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Errorf("NewDocuments: %v", err)
	}
}
