package ui

import (
	"fmt"
	"reflect"

	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// Идентификаторы кнопок окна документов (DocumentsView.Button).
const (
	BtnNew        = "new"         // новый документ
	BtnSave       = "save"        // сохранить документ
	BtnPost       = "post"        // провести
	BtnUnpost     = "unpost"      // отменить проведение
	BtnLineSave   = "line-save"   // записать строку
	BtnLineNew    = "line-new"    // новая строка
	BtnLineDelete = "line-delete" // удалить строку
	BtnClose      = "close"
)

// ReadOnlyNotice — подпись окна, открытого под ролью чтения.
const ReadOnlyNotice = "Режим чтения: запись недоступна"

// Trace, если задана, получает строку о каждой выполненной команде окна документов (диагностика нативных сценариев:
// команда и её результат попадают в stderr процесса; по умолчанию nil, ничего не печатается).
var Trace func(string)

// DocumentsView — окно документов одного вида: таблица документов, таблица строк выбранного документа, шапка и
// карточка строки (две панели), команды. Сверху вниз: списки, затем поля (на пути Tab между полями и кнопками нет
// таблиц), затем кнопки. Состояние и правила — в forms.Documents; окно только отражает их и пересылает команды.
type DocumentsView struct {
	app       *goWidgets.App
	win       *goWidgets.Window
	docs      *forms.Documents
	head      *panel
	line      *panel // nil без табличной части
	docTable  *goWidgets.Table
	lineTable *goWidgets.Table
	buttons   map[string]*goWidgets.Button
	onDone    func()
}

// OpenDocuments создаёт окно документов. «Сохранить» записывает документ целиком через ядро, «Провести» записывает и
// проводит, «Отменить проведение» отменяет; выбор строки списка загружает документ; «Записать строку», «Новая строка»,
// «Удалить строку» меняют строки документа в памяти до «Сохранить». «Закрыть» и крестик завершают окно (onDone, по
// умолчанию app.Quit). Ошибки ядра показываются в строке ошибки шапки.
func OpenDocuments(app *goWidgets.App, docs *forms.Documents, title string, onDone func()) (*DocumentsView, error) {
	win, err := app.NewWindow(title, 760, 1100)
	if err != nil {
		return nil, err
	}
	if onDone == nil {
		onDone = app.Quit
	}
	dv := &DocumentsView{app: app, win: win, docs: docs, buttons: map[string]*goWidgets.Button{}, onDone: onDone}
	scope := app.Scope()
	if docs.ReadOnly() {
		if _, err := win.AddLabel(ReadOnlyNotice); err != nil {
			return nil, err
		}
	}
	table := func(cols []string) (*goWidgets.Table, error) {
		tc := make([]goWidgets.TableColumn, len(cols))
		for i, c := range cols {
			tc[i] = goWidgets.TableColumn{Title: c}
			if i == 0 {
				tc[i].Align = goWidgets.AlignRight
			}
		}
		return win.AddTable(tc)
	}
	if dv.docTable, err = table(docs.Columns()); err != nil {
		return nil, err
	}
	dv.docTable.Selected.On(scope, func(i int) { dv.pickDocument(i) })
	if docs.HasLines() {
		if dv.lineTable, err = table(docs.LineColumns()); err != nil {
			return nil, err
		}
		dv.lineTable.Selected.On(scope, func(i int) { dv.pickLine(i) })
	}
	if dv.head, err = newPanel(app, win, docs.Header(), func(string) {}, func() { dv.head.labels() }); err != nil {
		return nil, err
	}
	if docs.HasLines() {
		if dv.line, err = newPanel(app, win, docs.Line(), func(string) {}, func() { dv.line.labels() }); err != nil {
			return nil, err
		}
	}
	add := func(id, label string, do func()) error {
		b, err := win.AddButton(label)
		if err != nil {
			return err
		}
		b.Clicked.On(scope, func(goWidgets.ClickInfo) { do() })
		dv.buttons[id] = b
		return nil
	}
	cmds := []struct {
		id, label string
		do        func()
	}{
		{BtnLineSave, "Записать строку", func() { dv.command(docs.SaveLine, false, true, true) }},
		{BtnLineNew, "Новая строка", func() { dv.command(docs.NewLine, false, true, true) }},
		{BtnLineDelete, "Удалить строку", func() { dv.command(docs.DeleteLine, false, true, true) }},
		{BtnSave, "Сохранить", func() { dv.command(docs.Save, false, false, false) }},
		{BtnPost, "Провести", func() { dv.command(docs.Post, true, true, false) }},
		{BtnUnpost, "Отменить проведение", func() { dv.command(docs.Unpost, true, true, false) }},
		{BtnNew, "Новый документ", func() { dv.command(docs.New, true, true, false) }},
		{BtnClose, "Закрыть", func() { docs.Close(); dv.show(false, false) }},
	}
	for _, c := range cmds {
		if !docs.HasLines() && (c.id == BtnLineSave || c.id == BtnLineNew || c.id == BtnLineDelete) {
			continue
		}
		if err := add(c.id, c.label, c.do); err != nil {
			return nil, err
		}
	}
	if docs.ReadOnly() {
		// роль сессии не разрешает запись: поля и команды записи недоступны, чтение (выбор документа) работает;
		// защитой служит ядро, команда мимо окна получит ErrDenied
		dv.head.setEnabled(false)
		if dv.line != nil {
			dv.line.setEnabled(false)
		}
		for id, b := range dv.buttons {
			b.Enabled.Set(id == BtnClose)
		}
	}
	win.Closing.On(scope, func(*goWidgets.CloseRequest) {
		docs.Close()
		dv.show(false, false)
	})
	dv.reloadTables()
	dv.show(true, false)
	return dv, nil
}

// command выполняет команду состояния и отражает результат в окне. Ошибка команды уже лежит в состоянии (ошибки
// полей и строка ошибки), поэтому здесь не разбирается. mirrorHead и mirrorLine — перенести тексты формы в
// виджеты: после команд, загружающих или очищающих форму. focusLine — фокус на карточку строки (команды строк),
// иначе на шапку.
func (dv *DocumentsView) command(run func() error, mirrorHead, mirrorLine, focusLine bool) {
	err := run()
	if Trace != nil {
		Trace(fmt.Sprintf("documents: command done: err=%v posted=%v error=%q", err, dv.docs.Posted(), dv.docs.Error()))
	}
	dv.reloadTables()
	if mirrorHead {
		dv.head.mirror()
	}
	if mirrorLine && dv.line != nil {
		dv.line.mirror()
	}
	dv.show(true, focusLine)
}

// pickDocument и pickLine отражают выбор строки таблицы пользователем. Событие выбора, совпадающее с состоянием,
// не команда: нативные backend сообщают и о программном выборе строки (и устаревшие сообщения приходят позже
// нового выбора), а команда перерисовывала бы таблицы и порождала новые сообщения — так два документа в списке
// переключали друг друга бесконечно (нативный сценарий нашёл именно это).
func (dv *DocumentsView) pickDocument(i int) {
	if cur, _ := dv.docs.Selected(); cur == i {
		return
	}
	dv.command(func() error { return dv.docs.Select(i) }, true, true, false)
}

func (dv *DocumentsView) pickLine(i int) {
	if dv.docs.SelectedLine() == i {
		return
	}
	dv.command(func() error { return dv.docs.SelectLine(i) }, false, true, true)
}

// reloadTables показывает списки из состояния и выбирает текущие документ и строку. Таблица меняется только если
// изменилось содержимое или выбор: лишний SetRows сбрасывает выбор и порождает новые сообщения backend.
func (dv *DocumentsView) reloadTables() {
	i, _ := dv.docs.Selected()
	syncTable(dv.docTable, dv.docs.Rows(), i)
	if dv.lineTable != nil {
		syncTable(dv.lineTable, dv.docs.Lines(), dv.docs.SelectedLine())
	}
}

func syncTable(t *goWidgets.Table, rows [][]string, sel int) {
	if !reflect.DeepEqual(t.Rows(), rows) {
		t.SetRows(rows)
	}
	if t.SelectedIndex() != sel {
		t.Select(sel)
	}
}

// show отражает ошибки обеих форм; закрытое окно завершается. Фокус переводится только по команде (moveFocus):
// форма не знает, куда пользователь перешёл Tab-ом, и отражение не должно возвращать фокус назад.
func (dv *DocumentsView) show(moveFocus, focusLine bool) {
	dv.head.labels()
	if dv.line != nil {
		dv.line.labels()
	}
	if !dv.docs.IsOpen() {
		dv.onDone()
		return
	}
	if !moveFocus || dv.docs.ReadOnly() {
		return
	}
	if focusLine && dv.line != nil {
		dv.line.focus()
		return
	}
	dv.head.focus()
}

// Window возвращает окно goWidgets.
func (dv *DocumentsView) Window() *goWidgets.Window { return dv.win }

// DocumentTable и LineTable возвращают таблицы документов и строк (LineTable — nil без табличной части).
func (dv *DocumentsView) DocumentTable() *goWidgets.Table { return dv.docTable }
func (dv *DocumentsView) LineTable() *goWidgets.Table     { return dv.lineTable }

// Header и Line возвращают панели шапки и строки (Line — nil без табличной части): виджеты по идентификатору
// контрола формы.
func (dv *DocumentsView) Header() *panel { return dv.head }
func (dv *DocumentsView) Line() *panel   { return dv.line }

// Button возвращает кнопку по идентификатору Btn* (nil, если такой нет).
func (dv *DocumentsView) Button(id string) *goWidgets.Button { return dv.buttons[id] }

// String — краткое описание окна для сообщений тестов.
func (dv *DocumentsView) String() string {
	return fmt.Sprintf("documents window (%d rows)", len(dv.docs.Rows()))
}
