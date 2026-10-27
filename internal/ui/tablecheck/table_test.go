// Package tablecheck проверяет готовый goWidgets.Table на закреплённой в go.mod
// ревизии: заголовки, пустой набор, выбор и обновление строк. Отдельный каталог —
// отдельный тестовый бинарник: goWidgets закрепляет поток интерфейса за горутиной,
// а у headless-backend глобальное состояние. Частной копии Table в oddyness нет
// (AGENTS.md п. 7); нативный GTK проверяет шаг CI на тесте самого goWidgets.
package tablecheck

import (
	"reflect"
	"testing"
	"time"

	"github.com/unxed/goWidgets"
	"github.com/unxed/goWidgets/backends/headless"
)

func TestTableOnPinnedRevision(t *testing.T) {
	t.Setenv("goWidgets_BACKEND", "headless")
	app, err := goWidgets.NewApp()
	if err != nil {
		t.Fatal(err)
	}
	win, err := app.NewWindow("table", 480, 320)
	if err != nil {
		t.Fatal(err)
	}
	cols := []goWidgets.TableColumn{{Title: "Товар"}, {Title: "Количество", Align: goWidgets.AlignRight}}
	table, err := win.AddTable(cols)
	if err != nil {
		t.Fatal(err)
	}
	if table.Fallback() || !app.Diagnostics().Caps.GridView {
		t.Fatal("ожидается нативный путь TableWindow, а не запасной ListBox")
	}

	// заголовки и пустой набор
	gotCols, rows, selected, _, _, ok := headless.TableState()
	if !ok || len(gotCols) != 2 || gotCols[0].Title != "Товар" || gotCols[1].Title != "Количество" || gotCols[1].Align != goWidgets.AlignRight {
		t.Fatalf("заголовки: %v ok=%v", gotCols, ok)
	}
	if len(rows) != 0 || selected != -1 || len(table.Rows()) != 0 || table.SelectedIndex() != -1 {
		t.Fatalf("пустая таблица: rows=%v selected=%d", rows, selected)
	}
	table.SetRows(nil)
	table.Select(0) // выбирать нечего: остаётся без выбора
	if table.SelectedIndex() != -1 {
		t.Errorf("выбор в пустой таблице: %d", table.SelectedIndex())
	}

	// заполнение, в том числе кириллица, кавычки и пустая ячейка
	data := [][]string{{"гайка «М6»", "10.500"}, {"болт \"8х30\"", "2.250"}, {"шайба", ""}}
	table.SetRows(data)
	data[0][0] = "изменено снаружи"
	if got := table.Rows(); !reflect.DeepEqual(got, [][]string{{"гайка «М6»", "10.500"}, {"болт \"8х30\"", "2.250"}, {"шайба", ""}}) {
		t.Fatalf("строки: %v", got)
	}
	_, stored, _, _, _, _ := headless.TableState()
	if len(stored) != 3 || stored[0][0] != "гайка «М6»" || stored[2][1] != "" {
		t.Errorf("строки в backend: %v", stored)
	}

	// выбор программой и пользователем
	var events []int
	table.Selected.On(app.Scope(), func(i int) { events = append(events, i) })
	table.Select(1)
	if table.SelectedIndex() != 1 {
		t.Errorf("Select(1): %d", table.SelectedIndex())
	}
	if !headless.PickTableRow(2) {
		t.Fatal("backend не принял выбор строки")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !(table.SelectedIndex() == 2 && len(events) > 0) {
		app.PumpOnce()
		time.Sleep(time.Millisecond)
	}
	if table.SelectedIndex() != 2 || len(events) == 0 || events[len(events)-1] != 2 {
		t.Fatalf("после выбора строки 2 backend-ом: selected=%d events=%v", table.SelectedIndex(), events)
	}
	table.Select(99) // вне диапазона снимает выбор
	if table.SelectedIndex() != -1 {
		t.Errorf("Select(99): %d", table.SelectedIndex())
	}

	// обновление: новый набор сбрасывает выбор
	table.Select(0)
	table.SetRows([][]string{{"кабель", "100"}})
	if table.SelectedIndex() != -1 || len(table.Rows()) != 1 || table.Rows()[0][0] != "кабель" {
		t.Errorf("после обновления: selected=%d rows=%v", table.SelectedIndex(), table.Rows())
	}
	_, stored, _, _, _, _ = headless.TableState()
	if len(stored) != 1 || stored[0][0] != "кабель" {
		t.Errorf("строки в backend после обновления: %v", stored)
	}
	table.SetRows(nil)
	if len(table.Rows()) != 0 {
		t.Error("пустой набор после данных")
	}
}
