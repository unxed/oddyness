package forms

import (
	"fmt"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
)

// ReportParam — параметр отчёта: ссылка на запись справочника Catalog (фильтр по справочнику).
type ReportParam struct {
	Name    string
	Catalog string
}

// ReportBackend выполняет отчёт ядром. params — значения параметров по имени: value.Ref или nil (пусто — параметр
// не отбирает). Значения строк результата — value.Number, string, bool, value.Ref или nil, по одному на столбец отчёта.
type ReportBackend interface {
	Run(params map[string]any) (rows [][]any, err error)
}

// Report — состояние окна отчёта: форма параметров и результат последнего «Сформировать». Строится из описания
// параметров и не знает о виджетах. Результат хранится до следующего запуска: правка параметров его не меняет.
type Report struct {
	id, name string
	params   *Form
	be       ReportBackend
	values   map[string]any
	cols     []string // заголовки известны заранее (по описанию отчёта), строк до первого запуска нет
	rows     [][]string
	ran      bool
}

// NewReport открывает отчёт: форма параметров пуста (все параметры необязательны), результата ещё нет. columns —
// заголовки столбцов результата (известны по описанию отчёта до первого запуска: окно строит таблицу сразу).
func NewReport(id, name string, params []ReportParam, columns []string, be ReportBackend) *Report {
	r := &Report{id: id, name: name, be: be, cols: append([]string(nil), columns...)}
	fields := make([]metadata.Field, len(params))
	for i, p := range params {
		fields[i] = metadata.Field{ID: p.Name, Name: p.Name, Type: metadata.TypeRef, Target: p.Catalog}
	}
	r.params = NewFields(id, name, fields, func(v map[string]any) error { r.values = v; return nil })
	r.params.KeepOpen(true)
	return r
}

// Params возвращает форму параметров (контролы `<отчёт>/f/<параметр>`).
func (r *Report) Params() *Form { return r.params }

// IsOpen сообщает, открыт ли отчёт; Close закрывает его.
func (r *Report) IsOpen() bool { return r.params.IsOpen() }
func (r *Report) Close()       { r.params.Close() }

// Ran сообщает, формировался ли отчёт хотя бы раз.
func (r *Report) Ran() bool { return r.ran }

// Columns — заголовки столбцов результата.
func (r *Report) Columns() []string { return append([]string(nil), r.cols...) }

// Rows — строки результата в тексте (числа — точной записью, ссылка — номером записи, булево — «да»/«нет»).
func (r *Report) Rows() [][]string { return r.rows }

// Error — ошибка последнего формирования (ядра); пусто, если её нет. Ошибки полей параметров — в Params().
func (r *Report) Error() string { return r.params.FormError() }

// Run разбирает параметры и формирует отчёт. Неверный параметр — ErrInvalid, прежний результат остаётся. Ошибка ядра
// остаётся в Error, результат очищается (устаревший результат рядом с ошибкой вводил бы в заблуждение).
func (r *Report) Run() error {
	r.values = nil
	if err := r.params.Save(); err != nil {
		return err
	}
	rows, err := r.be.Run(r.values)
	if err != nil {
		r.rows, r.ran = nil, false
		r.params.SetFormError(err.Error())
		return err
	}
	r.rows = make([][]string, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = cell(v)
		}
		if len(cells) != len(r.cols) {
			r.rows, r.ran = nil, false
			err := fmt.Errorf("forms: the report backend returned %d values per row, the report has %d columns", len(cells), len(r.cols))
			r.params.SetFormError(err.Error())
			return err
		}
		r.rows[i] = cells
	}
	r.ran = true
	return nil
}

// Snapshot — стабильный текстовый снимок: параметры и результат.
func (r *Report) Snapshot() string {
	var sb strings.Builder
	state := "open"
	if !r.IsOpen() {
		state = "closed"
	}
	fmt.Fprintf(&sb, "report %s %q [%s]\n", r.id, r.name, state)
	sb.WriteString(r.params.Snapshot())
	if !r.ran {
		sb.WriteString("not run\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "columns: %s\n", strings.Join(r.cols, " | "))
	for _, row := range r.rows {
		fmt.Fprintf(&sb, "  %s\n", strings.Join(row, " | "))
	}
	return sb.String()
}
