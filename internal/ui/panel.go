package ui

import (
	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// panel — виджеты одной формы внутри окна: подпись, поле или флажок и строка ошибки на каждое поле, затем кнопки
// формы (если они есть) и строка ошибки формы. Окно справочника содержит одну панель, окно документов — две
// (шапка и строка). Панель только отражает состояние forms.Form и пересылает ей ввод пользователя.
type panel struct {
	form     *forms.Form
	edits    map[string]*goWidgets.Edit
	checks   map[string]*goWidgets.CheckBox
	buttons  map[string]*goWidgets.Button
	errors   map[string]*goWidgets.Label
	formErr  *goWidgets.Label
	focusers map[string]func()

	syncing bool         // значения виджетов выставляет программа, а не пользователь: форме их не пересылать
	click   func(string) // клик по кнопке формы (идентификатор контрола)
	changed func()       // после того как ввод пользователя передан форме
}

// newPanel добавляет виджеты формы в окно в порядке обхода фокуса формы.
func newPanel(app *goWidgets.App, win *goWidgets.Window, form *forms.Form, click func(string), changed func()) (*panel, error) {
	p := &panel{
		form:  form,
		edits: map[string]*goWidgets.Edit{}, checks: map[string]*goWidgets.CheckBox{},
		buttons: map[string]*goWidgets.Button{}, errors: map[string]*goWidgets.Label{},
		focusers: map[string]func(){}, click: click, changed: changed,
	}
	scope := app.Scope()
	for _, c := range form.Controls() {
		id := c.ID
		switch c.Kind {
		case forms.Edit:
			if _, err := win.AddLabel(c.Label); err != nil {
				return nil, err
			}
			e, err := win.AddEdit(form.Text(id))
			if err != nil {
				return nil, err
			}
			e.Text.OnChange(scope, func(s, _ string) { p.input(func() error { return form.SetText(id, s) }) })
			p.edits[id], p.focusers[id] = e, e.Focus
			if p.errors[id], err = win.AddLabel(""); err != nil {
				return nil, err
			}
		case forms.CheckBox:
			cb, err := win.AddCheckBox(c.Label, form.Checked(id))
			if err != nil {
				return nil, err
			}
			cb.Checked.OnChange(scope, func(on, _ bool) { p.input(func() error { return form.SetChecked(id, on) }) })
			p.checks[id], p.focusers[id] = cb, cb.Focus
		case forms.Button:
			b, err := win.AddButton(c.Label)
			if err != nil {
				return nil, err
			}
			b.Clicked.On(scope, func(goWidgets.ClickInfo) { p.click(id) })
			p.buttons[id], p.focusers[id] = b, b.Focus
		}
	}
	var err error
	if p.formErr, err = win.AddLabel(""); err != nil {
		return nil, err
	}
	return p, nil
}

// input передаёт форме правку пользователя; ошибка команды (форма закрыта) показывается в строке ошибки формы.
func (p *panel) input(cmd func() error) {
	if p.syncing {
		return
	}
	if err := cmd(); err != nil {
		p.formErr.Text.Set(err.Error())
		return
	}
	p.changed()
}

// labels отражает ошибки полей и ошибку формы.
func (p *panel) labels() {
	for id, l := range p.errors {
		l.Text.Set(p.form.Error(id))
	}
	p.formErr.Text.Set(p.form.FormError())
}

// focus переводит фокус виджета на контрол, который форма считает сфокусированным.
func (p *panel) focus() {
	if f := p.focusers[p.form.Focused()]; f != nil {
		f()
	}
}

// mirror копирует тексты и флажки формы в виджеты; пересылка в форму на это время отключена.
func (p *panel) mirror() {
	p.syncing = true
	defer func() { p.syncing = false }()
	for id, e := range p.edits {
		e.Text.Set(p.form.Text(id))
	}
	for id, c := range p.checks {
		c.Checked.Set(p.form.Checked(id))
	}
}

// Edit, CheckBox, Button и ErrorLabel возвращают виджеты по идентификатору контрола формы (nil, если такого нет);
// нужны тестам и прокликивающему harness.
func (p *panel) Edit(id string) *goWidgets.Edit         { return p.edits[id] }
func (p *panel) CheckBox(id string) *goWidgets.CheckBox { return p.checks[id] }
func (p *panel) Button(id string) *goWidgets.Button     { return p.buttons[id] }
func (p *panel) ErrorLabel(id string) *goWidgets.Label  { return p.errors[id] }

// FormErrorLabel возвращает строку ошибки формы.
func (p *panel) FormErrorLabel() *goWidgets.Label { return p.formErr }
