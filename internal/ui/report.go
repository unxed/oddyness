package ui

import (
	"fmt"

	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// Идентификаторы кнопок окна отчёта (ReportView.Button).
const (
	BtnRun         = "run"          // сформировать
	BtnReportClose = "report-close" // закрыть
)

// ReportView — окно отчёта: таблица результата (goWidgets.Table), поля параметров и кнопки «Сформировать» и «Закрыть».
// Состояние и правила — в forms.Report; окно только отражает их. Результат обновляется командой «Сформировать»
// (правка параметра его не меняет). Порядок обхода Tab: поля параметров, «Сформировать», «Закрыть» (таблица стоит
// выше и в обход не входит).
type ReportView struct {
	app     *goWidgets.App
	win     *goWidgets.Window
	rep     *forms.Report
	params  *panel
	table   *goWidgets.Table
	buttons map[string]*goWidgets.Button
	onDone  func()
}

// OpenReport создаёт окно отчёта; «Закрыть» и крестик завершают окно (onDone, по умолчанию app.Quit). Результата до
// первого «Сформировать» нет: таблица пуста.
func OpenReport(app *goWidgets.App, rep *forms.Report, title string, onDone func()) (*ReportView, error) {
	win, err := app.NewWindow(title, 760, 700)
	if err != nil {
		return nil, err
	}
	if onDone == nil {
		onDone = app.Quit
	}
	rv := &ReportView{app: app, win: win, rep: rep, buttons: map[string]*goWidgets.Button{}, onDone: onDone}
	cols := make([]goWidgets.TableColumn, 0, len(rep.Columns()))
	for _, c := range rep.Columns() {
		cols = append(cols, goWidgets.TableColumn{Title: c})
	}
	if rv.table, err = win.AddTable(cols); err != nil {
		return nil, err
	}
	if rv.params, err = newPanel(app, win, rep.Params(), func(string) {}, func() { rv.params.labels() }); err != nil {
		return nil, err
	}
	scope := app.Scope()
	for _, c := range []struct {
		id, label string
		do        func()
	}{
		{BtnRun, "Сформировать", rv.run},
		{BtnReportClose, "Закрыть", func() { rep.Close(); rv.show(false) }},
	} {
		b, err := win.AddButton(c.label)
		if err != nil {
			return nil, err
		}
		do := c.do
		b.Clicked.On(scope, func(goWidgets.ClickInfo) { do() })
		rv.buttons[c.id] = b
	}
	win.Closing.On(scope, func(*goWidgets.CloseRequest) {
		rep.Close()
		rv.show(false)
	})
	rv.show(true)
	return rv, nil
}

func (rv *ReportView) run() {
	err := rv.rep.Run()
	if Trace != nil {
		Trace(fmt.Sprintf("report: run err=%v error=%q rows=%v", err, rv.rep.Error(), rv.rep.Rows()))
	}
	syncTable(rv.table, rv.rep.Rows(), -1)
	rv.show(true)
}

// show отражает ошибки формы параметров; закрытое окно завершается. Фокус — на первый параметр, когда команда его
// переводит (форма знает первый неверный параметр).
func (rv *ReportView) show(moveFocus bool) {
	rv.params.labels()
	if !rv.rep.IsOpen() {
		rv.onDone()
		return
	}
	if moveFocus {
		rv.params.focus()
	}
}

// Window возвращает окно goWidgets.
func (rv *ReportView) Window() *goWidgets.Window { return rv.win }

// Table возвращает таблицу результата, Params — панель параметров (виджеты по идентификатору контрола формы),
// Button — кнопку по идентификатору Btn* (nil, если такой нет); нужны тестам и прокликивающему harness.
func (rv *ReportView) Table() *goWidgets.Table { return rv.table }
func (rv *ReportView) Params() *panel          { return rv.params }
func (rv *ReportView) Button(id string) *goWidgets.Button {
	return rv.buttons[id]
}
