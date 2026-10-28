package forms

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

const (
	cCode   = "goods/f/goods-code"
	cTitle  = "goods/f/goods-title"
	cUnit   = "goods/f/goods-unit"
	cActive = "goods/f/goods-active"
	cMin    = "goods/f/goods-min"
	cSave   = "goods/b/save"
)

// memBackend — ядро в памяти с уникальностью кода (как в хранилище): ошибка дубликата приходит из «ядра».
type memBackend struct {
	rows    []Row
	next    int64
	inserts int
	updates int
	listErr error
}

var errDup = errors.New("duplicate value of a unique field")

func (b *memBackend) List() ([]Row, error) {
	if b.listErr != nil {
		return nil, b.listErr
	}
	return append([]Row(nil), b.rows...), nil
}

func (b *memBackend) dup(id int64, v map[string]any) bool {
	for _, r := range b.rows {
		if r.ID != id && r.Values["goods-code"] == v["goods-code"] {
			return true
		}
	}
	return false
}

func (b *memBackend) Insert(v map[string]any) (int64, error) {
	if b.dup(0, v) {
		return 0, errDup
	}
	b.next++
	b.inserts++
	b.rows = append(b.rows, Row{ID: b.next, Values: v})
	return b.next, nil
}

func (b *memBackend) Update(id int64, v map[string]any) error {
	if b.dup(id, v) {
		return errDup
	}
	for i := range b.rows {
		if b.rows[i].ID == id {
			b.rows[i].Values = v
			b.updates++
			return nil
		}
	}
	return errors.New("record not found")
}

func goods(t *testing.T) *metadata.Catalog {
	t.Helper()
	m, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	return &m.Catalogs[0]
}

func fill(t *testing.T, f *Form, code, title, unit, min string) {
	t.Helper()
	for id, v := range map[string]string{cCode: code, cTitle: title, cUnit: unit, cMin: min} {
		if err := f.SetText(id, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogCreateEditSelect(t *testing.T) {
	be := &memBackend{}
	cat, err := NewCatalog(goods(t), be)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cat.Columns(), "|"); got != "№|Код|Наименование|ЕдиницаИзмерения|Используется|МинимальныйОстаток|Аналог" {
		t.Errorf("columns = %s", got)
	}
	if i, id := cat.Selected(); i != -1 || id != 0 || len(cat.Rows()) != 0 || !cat.IsOpen() {
		t.Fatalf("initial state: %d %d %v", i, id, cat.Rows())
	}
	// S01: создать «Кабель», единица «м»
	card := cat.Card()
	fill(t, card, "K-1", "Кабель", "м", "10.500")
	card.SetChecked(cActive, true)
	if err := card.Save(); err != nil {
		t.Fatal(err)
	}
	if !cat.IsOpen() {
		t.Error("the catalog closed after saving")
	}
	want := [][]string{{"1", "K-1", "Кабель", "м", "да", "10.500", ""}}
	if got := cat.Rows(); len(got) != 1 || strings.Join(got[0], "|") != strings.Join(want[0], "|") {
		t.Errorf("rows = %v", got)
	}
	if i, id := cat.Selected(); i != 0 || id != 1 {
		t.Errorf("selected = %d %d, want the saved record", i, id)
	}
	// после сохранения следующая правка обновляет ту же запись, а не добавляет дубль
	card.SetText(cTitle, "Кабель ВВГ")
	if err := card.Save(); err != nil {
		t.Fatal(err)
	}
	if be.inserts != 1 || be.updates != 1 || len(cat.Rows()) != 1 || cat.Rows()[0][2] != "Кабель ВВГ" {
		t.Errorf("inserts=%d updates=%d rows=%v", be.inserts, be.updates, cat.Rows())
	}
	// новая запись: пустая карточка
	if err := cat.New(); err != nil {
		t.Fatal(err)
	}
	if card.Text(cCode) != "" || card.Checked(cActive) {
		t.Error("the card was not cleared")
	}
	fill(t, card, "K-2", "Труба", "шт", "1")
	if err := card.Save(); err != nil {
		t.Fatal(err)
	}
	if len(cat.Rows()) != 2 || be.inserts != 2 {
		t.Fatalf("rows = %v", cat.Rows())
	}
	// выбор первой записи загружает её в карточку
	if err := cat.Select(0); err != nil {
		t.Fatal(err)
	}
	if card.Text(cCode) != "K-1" || card.Text(cTitle) != "Кабель ВВГ" || !card.Checked(cActive) || card.Text(cMin) != "10.500" {
		t.Errorf("card after Select: %s", card.Snapshot())
	}
	if i, id := cat.Selected(); i != 0 || id != 1 {
		t.Errorf("selected = %d %d", i, id)
	}
	// Select(-1) — новая запись
	if err := cat.Select(-1); err != nil || card.Text(cCode) != "" {
		t.Errorf("Select(-1): %v %q", err, card.Text(cCode))
	}
	if err := cat.Select(5); err == nil {
		t.Error("row out of range accepted")
	}
	if !strings.Contains(cat.Snapshot(), "catalog goods \"Товары\" [open]") {
		t.Errorf("snapshot: %s", cat.Snapshot())
	}
	cat.Close()
	if cat.IsOpen() || cat.New() == nil || cat.Select(0) == nil {
		t.Error("commands on a closed catalog")
	}
}

// Пустое имя и дубликат кода: ошибки видны, список и данные не меняются.
func TestCatalogRefusals(t *testing.T) {
	be := &memBackend{}
	cat, err := NewCatalog(goods(t), be)
	if err != nil {
		t.Fatal(err)
	}
	card := cat.Card()
	fill(t, card, "K-1", "", "м", "1") // пустое наименование
	if err := card.Save(); !errors.Is(err, ErrInvalid) || card.Error(cTitle) != "required" || be.inserts != 0 {
		t.Fatalf("empty title: err=%v error=%q inserts=%d", err, card.Error(cTitle), be.inserts)
	}
	card.SetText(cTitle, "Кабель")
	if err := card.Save(); err != nil {
		t.Fatal(err)
	}
	// дубликат кода: ошибка ядра в форме, запись не добавлена, ввод сохранён
	cat.New()
	fill(t, card, "K-1", "Другой", "м", "1")
	if err := card.Save(); !errors.Is(err, errDup) || !strings.Contains(card.FormError(), "duplicate") {
		t.Fatalf("duplicate: err=%v form error=%q", err, card.FormError())
	}
	if len(cat.Rows()) != 1 || card.Text(cTitle) != "Другой" {
		t.Errorf("rows=%v title=%q", cat.Rows(), card.Text(cTitle))
	}
	// исправление кода — сохранение проходит
	card.SetText(cCode, "K-2")
	if err := card.Save(); err != nil || len(cat.Rows()) != 2 {
		t.Fatalf("after fixing: %v rows=%v", err, cat.Rows())
	}
	// правка существующей записи на занятый код — тоже отказ
	cat.Select(1)
	card.SetText(cCode, "K-1")
	if err := card.Save(); !errors.Is(err, errDup) || cat.Rows()[1][1] != "K-2" {
		t.Errorf("edit to a duplicate: %v rows=%v", err, cat.Rows())
	}
}

func TestCatalogListError(t *testing.T) {
	be := &memBackend{listErr: errors.New("boom")}
	if _, err := NewCatalog(goods(t), be); err == nil {
		t.Fatal("list error ignored")
	}
	be.listErr = nil
	cat, _ := NewCatalog(goods(t), be)
	be.listErr = errors.New("boom")
	if err := cat.Refresh(); err == nil || cat.ListError() != "boom" || !strings.Contains(cat.Snapshot(), "! boom") {
		t.Errorf("refresh: %v %q", err, cat.ListError())
	}
	fill(t, cat.Card(), "K-1", "Т", "м", "1")
	if err := cat.Card().Save(); err == nil || !strings.Contains(err.Error(), "list could not be read") {
		t.Errorf("save with a broken list: %v", err)
	}
}

func TestFormLoad(t *testing.T) {
	f := New(goods(t), nil)
	n, _ := value.Parse("2.5")
	err := f.Load(map[string]any{"goods-code": "A", "goods-active": true, "goods-min": n, "goods-analog": value.Ref{Catalog: "goods", ID: 7}})
	if err != nil || f.Text(cCode) != "A" || !f.Checked(cActive) || f.Text(cMin) != "2.5" || f.Text("goods/f/goods-analog") != "7" {
		t.Fatalf("Load: %v %s", err, f.Snapshot())
	}
	for name, v := range map[string]map[string]any{
		"bool in string": {"goods-code": true},
		"string in bool": {"goods-active": "x"},
		"string number":  {"goods-min": "1"},
		"number string":  {"goods-code": n},
		"ref string":     {"goods-code": value.Ref{Catalog: "goods", ID: 1}},
		"unsupported":    {"goods-code": 5},
	} {
		if err := f.Load(v); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if f.Text(cCode) != "A" {
			t.Errorf("%s: the form changed after a refused Load", name)
		}
	}
	f.Close()
	if err := f.Load(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Load on a closed form: %v", err)
	}
}
