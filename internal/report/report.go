// Package report — декларативные отчёты по остаткам (P18). Отчёт описан в метаданных именем и текстом запроса
// (internal/query); форма отчёта строится из параметров запроса. Параметр отчёта необязателен: не заданный (пустая
// ссылка) параметр не отбирает строки, поэтому «Товар» пусто — все товары. Подробности и ограничения — query.WithoutParams.
package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/query"
)

// Report — отчёт с разобранным и проверенным запросом.
type Report struct {
	ID    string
	Name  string
	Query *query.Query
}

// Load находит отчёт по id или имени (без учёта регистра), разбирает его запрос по метаданным и проверяет, что все
// параметры — ссылки: форма отчёта умеет только их (фильтр по справочнику). Ошибка запроса называет отчёт и позицию.
func Load(m *metadata.Model, key string) (*Report, error) {
	for _, r := range m.Reports {
		if r.ID != key && !strings.EqualFold(r.Name, key) {
			continue
		}
		q, err := query.Parse(r.Query, m)
		if err != nil {
			return nil, fmt.Errorf("report %q: %w", r.ID, err)
		}
		for _, p := range q.Params {
			if p.Type.Kind != query.KindRef {
				return nil, fmt.Errorf("report %q: parameter &%s has type %s; the report form supports only reference parameters", r.ID, p.Name, p.Type)
			}
		}
		return &Report{ID: r.ID, Name: r.Name, Query: q}, nil
	}
	return nil, fmt.Errorf("unknown report %q", key)
}

// Params — параметры отчёта по первому упоминанию в запросе.
func (r *Report) Params() []query.Param { return r.Query.Params }

// Run выполняет отчёт над остатками. values — значения параметров по имени (без учёта регистра): value.Ref или nil;
// отсутствующий и пустой параметр не отбирает строки. Значение для параметра, которого нет в запросе, — ошибка.
func (r *Report) Run(ctx context.Context, rd query.BalanceReader, values map[string]any) (*query.Result, error) {
	given := map[string]any{}
	var unset []string
	known := map[string]bool{}
	for _, p := range r.Query.Params {
		known[strings.ToLower(p.Name)] = true
		var v any
		for k, val := range values {
			if strings.EqualFold(k, p.Name) {
				v = val
			}
		}
		if v == nil {
			unset = append(unset, p.Name)
			continue
		}
		given[p.Name] = v
	}
	for k := range values {
		if !known[strings.ToLower(k)] {
			return nil, fmt.Errorf("report %q: unknown parameter &%s", r.ID, k)
		}
	}
	q, err := r.Query.WithoutParams(unset...)
	if err != nil {
		return nil, fmt.Errorf("report %q: %w", r.ID, err)
	}
	return q.Run(ctx, rd, given)
}
