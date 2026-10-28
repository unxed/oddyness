package query

import (
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
)

var (
	registerKinds  = []string{"accumulationregister", "регистрнакопления"}
	otherSources   = []string{"справочник", "catalog", "документ", "document", "регистрсведений", "informationregister"}
	balanceTables  = []string{"остатки", "balance"}
	unsupportedVTs = []string{"обороты", "turnovers", "остаткииобороты", "balanceandturnovers"}
)

func oneOf(s string, list []string) bool {
	s = strings.ToLower(s)
	for _, w := range list {
		if s == w {
			return true
		}
	}
	return false
}

type resolver struct {
	reg    *metadata.Register
	q      *Query
	fields []FieldRef
}

func (r *resolver) field(name string) (FieldRef, bool) {
	for _, f := range r.fields {
		if strings.EqualFold(f.Name, name) || f.ID == name {
			return f, true
		}
	}
	return FieldRef{}, false
}

func (r *resolver) fieldNames() string {
	var n []string
	for _, f := range r.fields {
		n = append(n, f.Name)
	}
	return strings.Join(n, ", ")
}

func resolve(rq *rawQuery, model *metadata.Model) (*Query, *Error) {
	if model == nil {
		return nil, fail(Pos{1, 1}, false, "no metadata to check the query against")
	}
	// Источник: РегистрНакопления.<Имя>.Остатки
	if !oneOf(rq.source[0], registerKinds) {
		if oneOf(rq.source[0], otherSources) {
			return nil, fail(rq.srcPos[0], true, "queries over %s are not supported: only AccumulationRegister.<name>.Balance", rq.source[0])
		}
		return nil, fail(rq.srcPos[0], false, "unknown source kind %q: expected AccumulationRegister", rq.source[0])
	}
	var reg *metadata.Register
	var names []string
	for i := range model.Registers {
		names = append(names, model.Registers[i].Name)
		if strings.EqualFold(model.Registers[i].Name, rq.source[1]) || model.Registers[i].ID == rq.source[1] {
			reg = &model.Registers[i]
		}
	}
	if reg == nil {
		return nil, fail(rq.srcPos[1], false, "unknown register %q (known: %s)", rq.source[1], strings.Join(names, ", "))
	}
	if oneOf(rq.source[2], unsupportedVTs) {
		return nil, fail(rq.srcPos[2], true, "virtual table %s is not supported: only Balance (current balances)", rq.source[2])
	}
	if !oneOf(rq.source[2], balanceTables) {
		return nil, fail(rq.srcPos[2], false, "unknown virtual table %q: expected Balance", rq.source[2])
	}
	r := &resolver{reg: reg, q: &Query{Register: reg.ID}}
	for _, f := range reg.Dimensions {
		r.fields = append(r.fields, FieldRef{ID: f.ID, Name: f.Name, Role: Dimension, Type: typeOf(f)})
	}
	for _, f := range reg.Resources {
		r.fields = append(r.fields, FieldRef{ID: f.ID, Name: f.Name, Role: Resource, Type: typeOf(f)})
	}
	for _, it := range rq.items {
		f, ok := r.field(it.name)
		if !ok {
			return nil, fail(it.pos, false, "unknown field %q of register %s (known: %s)", it.name, reg.Name, r.fieldNames())
		}
		name, pos := f.Name, it.pos
		if it.alias != "" {
			name, pos = it.alias, it.aliasPos
		}
		for _, c := range r.q.Columns {
			if strings.EqualFold(c.Name, name) {
				return nil, fail(pos, false, "duplicate result column %q", name)
			}
		}
		r.q.Columns = append(r.q.Columns, Column{Name: name, Field: f, Pos: it.pos})
	}
	if rq.where != nil {
		e, err := r.expr(rq.where)
		if err != nil {
			return nil, err
		}
		r.q.Where = e
	}
	for _, k := range rq.order {
		idx := -1
		for i, c := range r.q.Columns {
			if strings.EqualFold(c.Name, k.name) {
				idx = i
			}
		}
		if idx < 0 {
			for i, c := range r.q.Columns {
				if strings.EqualFold(c.Field.Name, k.name) || c.Field.ID == k.name {
					idx = i
				}
			}
		}
		if idx < 0 {
			return nil, fail(k.pos, false, "ORDER BY field %q is not in the select list", k.name)
		}
		for _, o := range r.q.Order {
			if o.Column == idx {
				return nil, fail(k.pos, false, "ORDER BY column %q is listed twice", k.name)
			}
		}
		r.q.Order = append(r.q.Order, OrderKey{Column: idx, Desc: k.desc, Pos: k.pos})
	}
	return r.q, nil
}

func (r *resolver) expr(e rawExpr) (Expr, *Error) {
	switch e := e.(type) {
	case rawBin:
		l, err := r.expr(e.left)
		if err != nil {
			return nil, err
		}
		rr, err := r.expr(e.right)
		if err != nil {
			return nil, err
		}
		if e.op == "and" {
			return And{l, rr}, nil
		}
		return Or{l, rr}, nil
	case rawNot:
		x, err := r.expr(e.x)
		if err != nil {
			return nil, err
		}
		return Not{x}, nil
	}
	return r.cmp(e.(rawCmp))
}

func (r *resolver) operand(o rawOperand) (Operand, *Error) {
	switch o.kind {
	case 'i':
		f, ok := r.field(o.text)
		if !ok {
			return nil, fail(o.pos, false, "unknown field %q of register %s (known: %s)", o.text, r.reg.Name, r.fieldNames())
		}
		return FieldOperand{Field: f}, nil
	case 'p':
		for i, p := range r.q.Params {
			if strings.EqualFold(p.Name, o.text) {
				return ParamOperand{Index: i, Type: p.Type}, nil
			}
		}
		r.q.Params = append(r.q.Params, Param{Name: o.text, Pos: o.pos})
		return ParamOperand{Index: len(r.q.Params) - 1}, nil
	case 'n':
		return Const{Type: Type{Kind: KindNumber}, Num: o.num}, nil
	case 's':
		return Const{Type: Type{Kind: KindString}, Str: o.text}, nil
	}
	return Const{Type: Type{Kind: KindBool}, Bool: o.b}, nil
}

func (r *resolver) cmp(c rawCmp) (Expr, *Error) {
	l, err := r.operand(c.left)
	if err != nil {
		return nil, err
	}
	rr, err := r.operand(c.right)
	if err != nil {
		return nil, err
	}
	lp, lIsParam := l.(ParamOperand)
	rp, rIsParam := rr.(ParamOperand)
	_, lIsConst := l.(Const)
	_, rIsConst := rr.(Const)
	switch {
	case lIsParam && rIsParam:
		return nil, fail(c.pos, true, "comparison of two parameters is not supported: compare a parameter with a field")
	case lIsConst && rIsConst:
		return nil, fail(c.pos, true, "comparison of two constants is not supported: one side must be a field")
	case lIsParam || rIsParam:
		p, other, oPos := lp, rr, c.right.pos
		if rIsParam {
			p, other, oPos = rp, l, c.left.pos
		}
		_ = oPos
		t := other.operandType()
		cur := r.q.Params[p.Index].Type
		if cur.Kind != 0 && cur != t {
			return nil, fail(c.pos, false, "parameter &%s is used as %s and as %s", r.q.Params[p.Index].Name, cur, t)
		}
		r.q.Params[p.Index].Type = t
		if lIsParam {
			l = ParamOperand{Index: p.Index, Type: t}
		} else {
			rr = ParamOperand{Index: p.Index, Type: t}
		}
	default:
		lt, rt := l.operandType(), rr.operandType()
		if lt != rt {
			if lt.Kind == KindDate && rt.Kind == KindString || lt.Kind == KindString && rt.Kind == KindDate {
				if _, ok := rr.(Const); ok || lIsConst {
					return nil, fail(c.pos, false, "a date field cannot be compared with a string literal; pass the date as a parameter")
				}
			}
			return nil, fail(c.pos, false, "cannot compare %s with %s", lt, rt)
		}
	}
	t := l.operandType()
	if (t.Kind == KindBool || t.Kind == KindRef) && c.op != OpEq && c.op != OpNe {
		return nil, fail(c.pos, false, "operator %s is not defined for %s: only = and <>", c.op, t)
	}
	return Cmp{Op: c.op, Left: l, Right: rr, Pos: c.pos}, nil
}
