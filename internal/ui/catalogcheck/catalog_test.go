// Пакет проверяет окно каталога (ui.OpenCatalog) на headless-backend goWidgets. Отдельный пакет: у headless-backend
// глобальное состояние таблицы, а поток интерфейса закреплён за горутиной теста (как в internal/ui/tablecheck).
package catalogcheck

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/unxed/goWidgets"
	"github.com/unxed/goWidgets/backends/headless"
	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/ui"
)

type mem struct {
	rows []forms.Row
}

func (b *mem) List() ([]forms.Row, error) { return append([]forms.Row(nil), b.rows...), nil }

func (b *mem) Insert(v map[string]any) (int64, error) {
	for _, r := range b.rows {
		if r.Values["goods-code"] == v["goods-code"] {
			return 0, errors.New("duplicate value of a unique field")
		}
	}
	id := int64(len(b.rows) + 1)
	b.rows = append(b.rows, forms.Row{ID: id, Values: v})
	return id, nil
}

func (b *mem) Update(id int64, v map[string]any) error {
	b.rows[id-1].Values = v
	return nil
}

const (
	code   = "goods/f/goods-code"
	title  = "goods/f/goods-title"
	unit   = "goods/f/goods-unit"
	minF   = "goods/f/goods-min"
	saveB  = "goods/b/save"
	closeB = "goods/b/close"
)

func TestCatalogWindowHeadless(t *testing.T) {
	t.Setenv("goWidgets_BACKEND", "headless")
	app, err := goWidgets.NewApp()
	if err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load("../../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	be := &mem{}
	cat, err := forms.NewCatalog(&model.Catalogs[0], be)
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	view, err := ui.OpenCatalog(app, cat, "Товары", func() { done++ })
	if err != nil {
		t.Fatal(err)
	}
	if view.Table().Fallback() || len(view.Table().Rows()) != 0 || view.NewButton() == nil {
		t.Fatal("the list is not a native table or is not empty")
	}
	save := func() { view.Button(saveB).Clicked.Emit(goWidgets.ClickInfo{}) }
	typeIn := func(id, text string) { view.Edit(id).Text.Set(text) }

	// пустое наименование: ошибка поля, записи нет
	typeIn(code, "K-1")
	typeIn(unit, "м")
	typeIn(minF, "10.500")
	save()
	if got := view.ErrorLabel(title).Text.Get(); got != "required" || len(be.rows) != 0 {
		t.Fatalf("empty title: label=%q rows=%d", got, len(be.rows))
	}
	// S01: создать «Кабель»
	typeIn(title, "Кабель")
	view.CheckBox("goods/f/goods-active").Checked.Set(true)
	save()
	if len(be.rows) != 1 || done != 0 {
		t.Fatalf("rows=%d done=%d after a valid Save", len(be.rows), done)
	}
	rows := view.Table().Rows()
	if len(rows) != 1 || rows[0][1] != "K-1" || rows[0][2] != "Кабель" || rows[0][4] != "да" || view.Table().SelectedIndex() != 0 {
		t.Errorf("table = %v selected=%d", rows, view.Table().SelectedIndex())
	}
	// дубликат кода: ошибка ядра в строке ошибки формы
	view.NewButton().Clicked.Emit(goWidgets.ClickInfo{})
	if view.Edit(code).Text.Get() != "" || view.Table().SelectedIndex() != -1 {
		t.Errorf("the card is not new: code=%q selected=%d", view.Edit(code).Text.Get(), view.Table().SelectedIndex())
	}
	typeIn(code, "K-1")
	typeIn(title, "Дубль")
	typeIn(unit, "шт")
	typeIn(minF, "1")
	save()
	if got := view.FormErrorLabel().Text.Get(); !strings.Contains(got, "duplicate") || len(be.rows) != 1 {
		t.Fatalf("duplicate: label=%q rows=%d", got, len(be.rows))
	}
	typeIn(code, "K-2")
	save()
	if len(be.rows) != 2 || len(view.Table().Rows()) != 2 || view.FormErrorLabel().Text.Get() != "" {
		t.Fatalf("rows=%d table=%v label=%q", len(be.rows), view.Table().Rows(), view.FormErrorLabel().Text.Get())
	}
	// headless-backend повторяет каждую программную запись текста и выбор строки таблицы сообщением (так делают и
	// настоящие платформы, но синхронно), и эти сообщения ждут в очереди до первого PumpOnce. Выбор пользователя
	// должен прийти в тихую очередь: иначе устаревшие сообщения программных записей приходят вперемешку с ним, форма
	// получает чужие тексты, и тест краснеет случайно (дважды наблюдалось).
	drain := func() {
		for i := 0; i < 1000 && app.PumpOnce(); i++ {
		}
	}
	drain()
	// пользователь выбирает первую строку: карточка загружает «Кабель», правка обновляет запись
	if !headless.PickTableRow(0) {
		t.Fatal("backend did not accept the row pick")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !(view.Edit(code).Text.Get() == "K-1" && view.Edit(title).Text.Get() == "Кабель") {
		app.PumpOnce()
		time.Sleep(time.Millisecond)
	}
	drain()
	if view.Edit(code).Text.Get() != "K-1" || view.Edit(title).Text.Get() != "Кабель" || !view.CheckBox("goods/f/goods-active").Checked.Get() {
		t.Fatalf("card after the pick: code=%q title=%q formError=%q selected=%d table selected=%d form code=%q", view.Edit(code).Text.Get(), view.Edit(title).Text.Get(), view.FormErrorLabel().Text.Get(), func() int { i, _ := cat.Selected(); return i }(), view.Table().SelectedIndex(), cat.Card().Text(code))
	}
	typeIn(title, "Кабель ВВГ")
	save()
	if len(be.rows) != 2 || be.rows[0].Values["goods-title"] != "Кабель ВВГ" || view.Table().Rows()[0][2] != "Кабель ВВГ" || view.Table().SelectedIndex() != 0 {
		t.Errorf("after the edit: rows=%d table=%v selected=%d", len(be.rows), view.Table().Rows(), view.Table().SelectedIndex())
	}
	// «Закрыть» завершает окно
	view.Button(closeB).Clicked.Emit(goWidgets.ClickInfo{})
	if done != 1 || cat.IsOpen() {
		t.Errorf("done=%d open=%v after Close", done, cat.IsOpen())
	}
}
