// Package ui привязывает форму справочника (internal/forms) к окну goWidgets.
// Окно только отражает состояние формы и пересылает ей команды: правила
// проверки и сохранения остаются в forms. Общих контролов здесь нет — только
// те, что даёт goWidgets (AGENTS.md п. 7).
package ui

import (
	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// View — окно формы справочника.
type View struct {
	app      *goWidgets.App
	win      *goWidgets.Window
	form     *forms.Form
	edits    map[string]*goWidgets.Edit
	checks   map[string]*goWidgets.CheckBox
	buttons  map[string]*goWidgets.Button
	errors   map[string]*goWidgets.Label
	formErr  *goWidgets.Label
	focusers map[string]func()
	onDone   func()
	saveID   string // кнопки: первая — «Сохранить», вторая — «Закрыть»
	closeID  string

	syncing   bool   // значения виджетов выставляет программа, а не пользователь: форме их не пересылать
	afterSave func() // вызывается после успешного «Сохранить» (окно каталога перечитывает список)
}

// Open создаёт окно для формы: подпись, поле или флажок и строка ошибки на
// каждое поле, затем кнопки и строка ошибки формы. onDone вызывается, когда
// форма закрыта (успешным сохранением, кнопкой или крестиком); nil означает
// app.Quit.
func Open(app *goWidgets.App, form *forms.Form, title string, onDone func()) (*View, error) {
	return open(app, form, title, onDone, 420, 560, nil)
}

// open строит окно; before (если задан) добавляет виджеты над карточкой.
func open(app *goWidgets.App, form *forms.Form, title string, onDone func(), w, h float64, before func(*View) error) (*View, error) {
	win, err := app.NewWindow(title, w, h)
	if err != nil {
		return nil, err
	}
	if onDone == nil {
		onDone = app.Quit
	}
	v := &View{
		app: app, win: win, form: form,
		edits: map[string]*goWidgets.Edit{}, checks: map[string]*goWidgets.CheckBox{},
		buttons: map[string]*goWidgets.Button{}, errors: map[string]*goWidgets.Label{},
		focusers: map[string]func(){}, onDone: onDone,
	}
	scope := app.Scope()
	if before != nil {
		if err := before(v); err != nil {
			return nil, err
		}
	}
	for _, c := range form.Controls() {
		switch c.Kind {
		case forms.Edit:
			if _, err := win.AddLabel(c.Label); err != nil {
				return nil, err
			}
			e, err := win.AddEdit(form.Text(c.ID))
			if err != nil {
				return nil, err
			}
			id := c.ID
			e.Text.OnChange(scope, func(s, _ string) { v.input(func() error { return form.SetText(id, s) }) })
			v.edits[id], v.focusers[id] = e, e.Focus
			if v.errors[id], err = win.AddLabel(""); err != nil {
				return nil, err
			}
		case forms.CheckBox:
			cb, err := win.AddCheckBox(c.Label, form.Checked(c.ID))
			if err != nil {
				return nil, err
			}
			id := c.ID
			cb.Checked.OnChange(scope, func(on, _ bool) { v.input(func() error { return form.SetChecked(id, on) }) })
			v.checks[id], v.focusers[id] = cb, cb.Focus
		case forms.Button:
			b, err := win.AddButton(c.Label)
			if err != nil {
				return nil, err
			}
			id := c.ID
			b.Clicked.On(scope, func(goWidgets.ClickInfo) { v.click(id) })
			v.buttons[id], v.focusers[id] = b, b.Focus
			if v.saveID == "" {
				v.saveID = id
			} else {
				v.closeID = id
			}
		}
	}
	if v.formErr, err = win.AddLabel(""); err != nil {
		return nil, err
	}
	win.Closing.On(scope, func(*goWidgets.CloseRequest) {
		form.Close()
		v.refresh(false)
	})
	v.refresh(true)
	return v, nil
}

// input передаёт форме правку пользователя; ошибка команды (форма закрыта)
// показывается в строке ошибки формы.
func (v *View) input(cmd func() error) {
	if v.syncing {
		return
	}
	if err := cmd(); err != nil {
		v.formErr.Text.Set(err.Error())
		return
	}
	v.refresh(false)
}

func (v *View) click(id string) {
	switch id {
	case v.saveID:
		if err := v.form.Save(); err == nil && v.afterSave != nil {
			v.afterSave()
		} // иначе результат виден через refresh: ошибки полей, ошибка формы, закрытие
	case v.closeID:
		v.form.Close()
	}
	v.refresh(true)
}

// refresh отражает состояние формы: ошибки полей и формы; закрытие формы
// завершает работу окна. Фокус перемещается только по команде формы (moveFocus):
// форма не знает, куда пользователь перешёл Tab-ом, и отражение ввода не должно
// возвращать фокус на прежний контрол (нативный сценарий нашёл именно эту ошибку).
func (v *View) refresh(moveFocus bool) {
	for id, l := range v.errors {
		l.Text.Set(v.form.Error(id))
	}
	v.formErr.Text.Set(v.form.FormError())
	if v.form.IsOpen() {
		if f := v.focusers[v.form.Focused()]; f != nil && moveFocus {
			f()
		}
		return
	}
	v.onDone()
}

// Window возвращает окно goWidgets.
func (v *View) Window() *goWidgets.Window { return v.win }

// Edit, CheckBox, Button и ErrorLabel возвращают виджеты по идентификатору
// контрола формы (nil, если такого нет); нужны тестам и прокликивающему harness.
func (v *View) Edit(id string) *goWidgets.Edit         { return v.edits[id] }
func (v *View) CheckBox(id string) *goWidgets.CheckBox { return v.checks[id] }
func (v *View) Button(id string) *goWidgets.Button     { return v.buttons[id] }
func (v *View) ErrorLabel(id string) *goWidgets.Label  { return v.errors[id] }

// FormErrorLabel возвращает строку ошибки формы.
func (v *View) FormErrorLabel() *goWidgets.Label { return v.formErr }
