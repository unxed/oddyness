package ui

import (
	"strings"
	"testing"

	"github.com/unxed/goWidgets"
	_ "github.com/unxed/goWidgets/backends/headless"
	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

const (
	code   = "goods/f/goods-code"
	title  = "goods/f/goods-title"
	unit   = "goods/f/goods-unit"
	active = "goods/f/goods-active"
	minF   = "goods/f/goods-min"
	analog = "goods/f/goods-analog"
	saveB  = "goods/b/save"
	closeB = "goods/b/close"
)

// Сценарий на headless-backend goWidgets без окна ОС: ввод идёт через те же
// свойства виджетов (Edit.Text, CheckBox.Checked, Button.Clicked), через которые
// приходит ввод пользователя. Все шаги — в одной горутине: goWidgets закрепляет
// за ней поток интерфейса, поэтому без подтестов и t.Parallel.
func TestFormWindowHeadless(t *testing.T) {
	t.Setenv("goWidgets_BACKEND", "headless")
	app, err := goWidgets.NewApp()
	if err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	saves := 0
	form := forms.New(&model.Catalogs[0], func(v map[string]any) error { saves++; saved = v; return nil })
	done := 0
	view, err := Open(app, form, "Товары", func() { done++ })
	if err != nil {
		t.Fatal(err)
	}

	// все контролы формы получили виджеты
	for _, c := range form.Controls() {
		var have bool
		switch c.Kind {
		case forms.Edit:
			have = view.Edit(c.ID) != nil && view.ErrorLabel(c.ID) != nil
		case forms.CheckBox:
			have = view.CheckBox(c.ID) != nil
		case forms.Button:
			have = view.Button(c.ID) != nil
		}
		if !have {
			t.Errorf("no widget for %s", c.ID)
		}
	}
	if view.Edit("nope") != nil || view.Window() == nil || view.FormErrorLabel() == nil {
		t.Error("accessors")
	}

	// 1. пустая форма: «Сохранить» показывает ошибки обязательных полей
	view.Button(saveB).Clicked.Emit(goWidgets.ClickInfo{})
	for _, id := range []string{code, title, unit, minF} {
		if got := view.ErrorLabel(id).Text.Get(); got != "required" {
			t.Errorf("error label of %s = %q, want required", id, got)
		}
	}
	if got := view.ErrorLabel(analog).Text.Get(); got != "" {
		t.Errorf("optional reference shows %q", got)
	}
	if saves != 0 || done != 0 || form.Focused() != code {
		t.Errorf("saves=%d done=%d focus=%s after an invalid Save", saves, done, form.Focused())
	}

	// 2. ввод в поле сбрасывает его ошибку и попадает в форму
	view.Edit(code).Text.Set("K-1")
	if form.Text(code) != "K-1" || view.ErrorLabel(code).Text.Get() != "" || view.ErrorLabel(title).Text.Get() != "required" {
		t.Errorf("after typing: form=%q code-error=%q title-error=%q", form.Text(code), view.ErrorLabel(code).Text.Get(), view.ErrorLabel(title).Text.Get())
	}
	view.Edit(title).Text.Set("Кабель")
	view.Edit(unit).Text.Set("м")
	view.CheckBox(active).Checked.Set(true)
	if !form.Checked(active) {
		t.Error("check box did not reach the form")
	}

	// 3. число с лишними знаками отклоняется, а не округляется
	view.Edit(minF).Text.Set("0.0001")
	view.Button(saveB).Clicked.Emit(goWidgets.ClickInfo{})
	if got := view.ErrorLabel(minF).Text.Get(); !strings.Contains(got, "fraction digits") {
		t.Errorf("min error = %q", got)
	}
	if saves != 0 || done != 0 {
		t.Errorf("saves=%d done=%d after a rejected number", saves, done)
	}

	// 4. исправление и успешное сохранение закрывают форму и завершают окно
	view.Edit(minF).Text.Set("10.500")
	view.Button(saveB).Clicked.Emit(goWidgets.ClickInfo{})
	if saves != 1 || done != 1 || form.IsOpen() {
		t.Fatalf("saves=%d done=%d open=%v after a valid Save", saves, done, form.IsOpen())
	}
	n, ok := saved["goods-min"].(value.Number)
	if saved["goods-code"] != "K-1" || saved["goods-title"] != "Кабель" || saved["goods-unit"] != "м" || saved["goods-active"] != true || !ok || n.String() != "10.500" {
		t.Errorf("saved = %#v", saved)
	}
	for id := range map[string]bool{code: true, title: true, unit: true, minF: true, analog: true} {
		if got := view.ErrorLabel(id).Text.Get(); got != "" {
			t.Errorf("error label of %s after success = %q", id, got)
		}
	}
	// правка закрытой формы не меняет данные и показывает причину
	view.Edit(code).Text.Set("other")
	if form.Text(code) != "K-1" || !strings.Contains(view.FormErrorLabel().Text.Get(), "closed") {
		t.Errorf("edit after close: form=%q label=%q", form.Text(code), view.FormErrorLabel().Text.Get())
	}
	_ = closeB
}
