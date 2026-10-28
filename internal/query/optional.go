package query

import "strings"

// WithoutParams возвращает копию запроса без условий отбора, которые используют перечисленные параметры (без учёта
// регистра написания): параметр «не задан», его условие не отбирает. Условие вырезается только как целая верхняя
// конъюнкция отбора (`А = &П И Б > 0`: вырезается `А = &П`); параметр под ИЛИ или НЕ — ошибка, потому что смысл
// «не задано» там неоднозначен и угадывать его нельзя. Неизвестное имя — ошибка. Оставшиеся параметры
// перенумеровываются; исходный запрос не меняется.
func (q *Query) WithoutParams(names ...string) (*Query, error) {
	drop := map[int]bool{}
	for _, n := range names {
		found := false
		for i, p := range q.Params {
			if strings.EqualFold(p.Name, n) {
				drop[i], found = true, true
			}
		}
		if !found {
			return nil, paramErr("unknown parameter &%s: the query does not use it", n)
		}
	}
	out := *q
	out.Params = nil
	remap := map[int]int{}
	for i, p := range q.Params {
		if !drop[i] {
			remap[i] = len(out.Params)
			out.Params = append(out.Params, p)
		}
	}
	out.Where = nil
	var kept []Expr
	for _, c := range conjuncts(q.Where) {
		used := usedParams(c, nil)
		hit := false
		for _, i := range used {
			hit = hit || drop[i]
		}
		if !hit {
			kept = append(kept, reindex(c, remap))
			continue
		}
		if _, ok := c.(Cmp); !ok {
			for _, i := range used {
				if drop[i] {
					return nil, paramErr("parameter &%s cannot be left unset: it is used under OR or NOT; only a top-level AND condition can be dropped", q.Params[i].Name)
				}
			}
		}
	}
	for _, c := range kept {
		if out.Where == nil {
			out.Where = c
		} else {
			out.Where = And{Left: out.Where, Right: c}
		}
	}
	return &out, nil
}

// conjuncts разворачивает цепочку И в список верхних условий; nil — пустой список.
func conjuncts(e Expr) []Expr {
	switch e := e.(type) {
	case nil:
		return nil
	case And:
		return append(conjuncts(e.Left), conjuncts(e.Right)...)
	}
	return []Expr{e}
}

// usedParams дописывает индексы параметров, упомянутых в условии.
func usedParams(e Expr, acc []int) []int {
	switch e := e.(type) {
	case And:
		return usedParams(e.Right, usedParams(e.Left, acc))
	case Or:
		return usedParams(e.Right, usedParams(e.Left, acc))
	case Not:
		return usedParams(e.X, acc)
	case Cmp:
		for _, o := range []Operand{e.Left, e.Right} {
			if p, ok := o.(ParamOperand); ok {
				acc = append(acc, p.Index)
			}
		}
	}
	return acc
}

// reindex возвращает копию условия с новыми индексами параметров.
func reindex(e Expr, remap map[int]int) Expr {
	switch e := e.(type) {
	case And:
		return And{Left: reindex(e.Left, remap), Right: reindex(e.Right, remap)}
	case Or:
		return Or{Left: reindex(e.Left, remap), Right: reindex(e.Right, remap)}
	case Not:
		return Not{X: reindex(e.X, remap)}
	case Cmp:
		e.Left, e.Right = reindexOperand(e.Left, remap), reindexOperand(e.Right, remap)
		return e
	}
	return e
}

func reindexOperand(o Operand, remap map[int]int) Operand {
	if p, ok := o.(ParamOperand); ok {
		p.Index = remap[p.Index]
		return p
	}
	return o
}
