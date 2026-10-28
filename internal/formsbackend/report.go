package formsbackend

import (
	"context"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/report"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// reportBackend выполняет отчёт над остатками хранилища (запрос идёт через ядро: internal/report, internal/query).
type reportBackend struct {
	store *sqlite.Store
	rep   *report.Report
}

func (b reportBackend) Run(params map[string]any) ([]string, [][]any, error) {
	res, err := b.rep.Run(context.Background(), b.store, params)
	if err != nil {
		return nil, nil, err
	}
	return res.Columns, res.Rows, nil
}

// OpenReport открывает форму отчёта над хранилищем; параметры формы — параметры запроса отчёта (ссылки).
func OpenReport(store *sqlite.Store, rep *report.Report) *forms.Report {
	params := make([]forms.ReportParam, len(rep.Params()))
	for i, p := range rep.Params() {
		params[i] = forms.ReportParam{Name: p.Name, Catalog: p.Type.Target}
	}
	return forms.NewReport(rep.ID, rep.Name, params, reportBackend{store, rep})
}
