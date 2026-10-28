package query

import (
	"strings"
	"time"

	"github.com/unxed/oddyness/internal/value"
)

// Bind проверяет значения параметров по выведенным типам и возвращает их под объявленными именами.
// Значения передаются отдельно от текста запроса и никогда не подставляются в него: число — только
// value.Number (float64 и целые Go отклоняются), строка — string, дата — string ГГГГ-ММ-ДД, булево — bool,
// ссылка — value.Ref нужного справочника (nil — пустая ссылка). Не заданный или лишний параметр — ошибка.
func (q *Query) Bind(params map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(q.Params))
	used := map[string]bool{}
	for _, p := range q.Params {
		var key string
		var v any
		found := false
		for k, val := range params {
			if strings.EqualFold(k, p.Name) {
				if found {
					return nil, paramErr("parameter &%s is given twice (names differ only by case)", p.Name)
				}
				key, v, found = k, val, true
			}
		}
		if !found {
			return nil, paramErr("parameter &%s is not set (expected %s)", p.Name, p.Type)
		}
		used[key] = true
		if err := checkValue(p, v); err != nil {
			return nil, err
		}
		out[p.Name] = v
	}
	for k := range params {
		if !used[k] {
			return nil, paramErr("unknown parameter &%s: the query does not use it", k)
		}
	}
	return out, nil
}

func checkValue(p Param, v any) error {
	bad := func() error {
		return paramErr("parameter &%s: expected %s, got %T", p.Name, p.Type, v)
	}
	switch p.Type.Kind {
	case KindNumber:
		if _, ok := v.(value.Number); !ok {
			return bad()
		}
	case KindString:
		if _, ok := v.(string); !ok {
			return bad()
		}
	case KindBool:
		if _, ok := v.(bool); !ok {
			return bad()
		}
	case KindDate:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return paramErr("parameter &%s: %q is not a date YYYY-MM-DD", p.Name, s)
		}
	case KindRef:
		if v == nil {
			return nil
		}
		r, ok := v.(value.Ref)
		if !ok {
			return bad()
		}
		if r.Catalog != p.Type.Target {
			return paramErr("parameter &%s: expected a reference to %q, got a reference to %q", p.Name, p.Type.Target, r.Catalog)
		}
	}
	return nil
}
