package forms

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

func goodsCatalog(t testing.TB) *metadata.Catalog {
	t.Helper()
	m, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	return &m.Catalogs[0]
}

const (
	code   = "goods/f/goods-code"
	title  = "goods/f/goods-title"
	unit   = "goods/f/goods-unit"
	active = "goods/f/goods-active"
	min    = "goods/f/goods-min"
	analog = "goods/f/goods-analog"
	save   = "goods/b/save"
	closeB = "goods/b/close"
)

const initialSnapshot = `form goods "Товары" [open]
* goods/f/goods-code edit "Код" = ""
  goods/f/goods-title edit "Наименование" = ""
  goods/f/goods-unit edit "ЕдиницаИзмерения" = ""
  goods/f/goods-active checkbox "Используется" = [ ]
  goods/f/goods-min edit "МинимальныйОстаток" = ""
  goods/f/goods-analog edit "Аналог" = ""
  goods/b/save button "Сохранить"
  goods/b/close button "Закрыть"
`

func TestInitialLayout(t *testing.T) {
	f := New(goodsCatalog(t), nil)
	if got := f.Snapshot(); got != initialSnapshot {
		t.Errorf("snapshot:\n%s\nwant:\n%s", got, initialSnapshot)
	}
	var ids []string
	for _, c := range f.Controls() {
		ids = append(ids, c.ID)
	}
	want := []string{code, title, unit, active, min, analog, save, closeB}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Errorf("controls = %v", ids)
	}
	if !f.IsOpen() || f.Focused() != code || f.Saved() != nil {
		t.Errorf("initial state: open=%v focus=%s saved=%v", f.IsOpen(), f.Focused(), f.Saved())
	}
	if FieldControlID("goods", "x") != "goods/f/x" || ButtonControlID("goods", "save") != save {
		t.Error("control id helpers")
	}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Сценарий 1: заполнить, сохранить; значения типизированы, форма закрывается.
func TestScenarioFillAndSave(t *testing.T) {
	t.Parallel()
	var calls int
	var got map[string]any
	f := New(goodsCatalog(t), func(v map[string]any) error { calls++; got = v; return nil })
	must(t, f.SetText(code, "K-1"))
	must(t, f.SetText(title, "Кабель"))
	must(t, f.SetText(unit, "м"))
	must(t, f.SetChecked(active, true))
	must(t, f.SetText(min, "10.500"))
	must(t, f.SetText(analog, " 7 "))
	must(t, f.Save())
	if f.IsOpen() || calls != 1 {
		t.Fatalf("open=%v calls=%d", f.IsOpen(), calls)
	}
	n, ok := got["goods-min"].(value.Number)
	if got["goods-code"] != "K-1" || got["goods-title"] != "Кабель" || got["goods-unit"] != "м" || got["goods-active"] != true ||
		!ok || n.String() != "10.500" || got["goods-analog"] != (value.Ref{Catalog: "goods", ID: 7}) || len(got) != 6 {
		t.Errorf("saved = %#v", got)
	}
	if len(f.Saved()) != 6 || f.Error(code) != "" || f.FormError() != "" {
		t.Errorf("state after save: %v %q %q", f.Saved(), f.Error(code), f.FormError())
	}
	if err := f.Save(); !errors.Is(err, ErrClosed) || calls != 1 {
		t.Errorf("second Save: %v, calls=%d", err, calls)
	}
}

const invalidSnapshot = `form goods "Товары" [open]
* goods/f/goods-code edit "Код" = "" !error: required
  goods/f/goods-title edit "Наименование" = "" !error: required
  goods/f/goods-unit edit "ЕдиницаИзмерения" = "" !error: required
  goods/f/goods-active checkbox "Используется" = [ ]
  goods/f/goods-min edit "МинимальныйОстаток" = "" !error: required
  goods/f/goods-analog edit "Аналог" = ""
  goods/b/save button "Сохранить"
  goods/b/close button "Закрыть"
`

// Сценарий 2: обязательные поля, фокус на первом неверном, исправление по шагам.
func TestScenarioRequiredFields(t *testing.T) {
	t.Parallel()
	calls := 0
	f := New(goodsCatalog(t), func(map[string]any) error { calls++; return nil })
	must(t, f.Focus(save))
	if err := f.Save(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Save of an empty form: %v", err)
	}
	if got := f.Snapshot(); got != invalidSnapshot {
		t.Errorf("snapshot:\n%s\nwant:\n%s", got, invalidSnapshot)
	}
	if calls != 0 || !f.IsOpen() {
		t.Errorf("calls=%d open=%v after invalid Save", calls, f.IsOpen())
	}
	must(t, f.SetText(code, "K-1"))
	if f.Error(code) != "" || f.Error(title) != "required" {
		t.Errorf("errors after fixing code: %q %q", f.Error(code), f.Error(title))
	}
	if err := f.Save(); !errors.Is(err, ErrInvalid) || f.Focused() != title {
		t.Errorf("second Save: %v, focus %s", err, f.Focused())
	}
	must(t, f.SetText(title, "Кабель"))
	must(t, f.SetText(unit, "м"))
	must(t, f.SetText(min, "1"))
	must(t, f.Save())
	if calls != 1 || f.IsOpen() {
		t.Errorf("calls=%d open=%v", calls, f.IsOpen())
	}
	if v, present := f.Saved()["goods-analog"]; !present || v != nil {
		t.Errorf("empty reference = %v", f.Saved()["goods-analog"])
	}
}

func TestFieldValidation(t *testing.T) {
	cases := []struct {
		id, text, msg string
	}{
		{min, "abc", "not a valid number"},
		{min, "1,5", "not a valid number"},
		{min, "0.0001", "fraction digits"},
		{min, "1000000000000", "integer digits"},
		{min, "   ", "required"},
		{code, "   ", "required"},
		{analog, "x", "positive record number"},
		{analog, "0", "positive record number"},
		{analog, "-3", "positive record number"},
	}
	for _, c := range cases {
		f := New(goodsCatalog(t), nil)
		for _, id := range []string{code, title, unit} {
			must(t, f.SetText(id, "v"))
		}
		must(t, f.SetText(min, "1"))
		must(t, f.SetText(c.id, c.text))
		if err := f.Save(); !errors.Is(err, ErrInvalid) || !strings.Contains(f.Error(c.id), c.msg) || f.Focused() != c.id {
			t.Errorf("%s=%q: err=%v error=%q focus=%s; want containing %q", c.id, c.text, err, f.Error(c.id), f.Focused(), c.msg)
		}
	}
	// граничные допустимые значения
	for _, text := range []string{"999999999999.999", "-999999999999.999", "0", "0.001"} {
		f := New(goodsCatalog(t), nil)
		for _, id := range []string{code, title, unit} {
			must(t, f.SetText(id, "v"))
		}
		must(t, f.SetText(min, text))
		if err := f.Save(); err != nil {
			t.Errorf("%q rejected: %v", text, err)
		}
	}
}

func TestFocusAndCommandErrors(t *testing.T) {
	f := New(goodsCatalog(t), nil)
	var visited []string
	for i := 0; i < 8; i++ {
		visited = append(visited, f.Focused())
		must(t, f.FocusNext())
	}
	if f.Focused() != code || visited[7] != closeB {
		t.Errorf("FocusNext does not wrap: %v then %s", visited, f.Focused())
	}
	must(t, f.FocusPrev())
	if f.Focused() != closeB {
		t.Errorf("FocusPrev from the first = %s", f.Focused())
	}
	must(t, f.Focus(min))
	if f.Focused() != min {
		t.Error("Focus did not move")
	}
	checks := []struct {
		name string
		err  error
		want error
	}{
		{"Focus unknown", f.Focus("nope"), ErrUnknownControl},
		{"SetText unknown", f.SetText("nope", "x"), ErrUnknownControl},
		{"SetText on checkbox", f.SetText(active, "x"), ErrWrongControl},
		{"SetText on button", f.SetText(save, "x"), ErrWrongControl},
		{"SetChecked on edit", f.SetChecked(code, true), ErrWrongControl},
	}
	for _, c := range checks {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.err, c.want)
		}
	}
	if f.Focused() != min {
		t.Error("failed commands moved the focus")
	}
}

func TestClose(t *testing.T) {
	calls := 0
	f := New(goodsCatalog(t), func(map[string]any) error { calls++; return nil })
	must(t, f.SetText(code, "K-1"))
	f.Close()
	if f.IsOpen() || !strings.Contains(f.Snapshot(), "[closed]") {
		t.Error("form is still open")
	}
	for name, err := range map[string]error{
		"SetText": f.SetText(code, "x"), "SetChecked": f.SetChecked(active, true),
		"Focus": f.Focus(code), "FocusNext": f.FocusNext(), "FocusPrev": f.FocusPrev(), "Save": f.Save(),
	} {
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close: %v", name, err)
		}
	}
	if calls != 0 || f.Saved() != nil {
		t.Errorf("closed form saved: calls=%d saved=%v", calls, f.Saved())
	}
}

func TestSaveFailureKeepsFormOpen(t *testing.T) {
	boom := errors.New("duplicate value of a unique field")
	f := New(goodsCatalog(t), func(map[string]any) error { return boom })
	for _, id := range []string{code, title, unit} {
		must(t, f.SetText(id, "v"))
	}
	must(t, f.SetText(min, "1"))
	if err := f.Save(); !errors.Is(err, boom) {
		t.Fatalf("Save: %v", err)
	}
	if !f.IsOpen() || f.FormError() != boom.Error() || f.Saved() != nil || f.Text(code) != "v" {
		t.Errorf("state: open=%v formErr=%q saved=%v text=%q", f.IsOpen(), f.FormError(), f.Saved(), f.Text(code))
	}
	if !strings.HasSuffix(f.Snapshot(), "! duplicate value of a unique field\n") {
		t.Errorf("snapshot does not show the error:\n%s", f.Snapshot())
	}
	must(t, f.SetText(code, "other"))
	if f.FormError() != "" {
		t.Error("form error survived an edit")
	}
}
