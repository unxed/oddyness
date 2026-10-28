package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/query"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

// runQuery выполняет `oddyness query <config-dir> <db-file> <текст запроса> [имя=значение...]`.
// Значения параметров приводятся к типу, выведенному запросом: число — точным литералом, ссылка — числом id записи
// (пусто — пустая ссылка), дата ГГГГ-ММ-ДД, булево true/false, строка как есть.
func runQuery(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "usage: oddyness query <config-dir> <db-file> <query text> [name=value...]")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	m, err := metadata.Load(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	q, err := query.Parse(args[2], m)
	if err != nil {
		return fail(err)
	}
	params := map[string]any{}
	for _, a := range args[3:] {
		name, raw, ok := strings.Cut(a, "=")
		if !ok {
			return fail(fmt.Errorf("parameter %q: expected name=value", a))
		}
		var typ *query.Type
		for i := range q.Params {
			if strings.EqualFold(q.Params[i].Name, name) {
				typ = &q.Params[i].Type
			}
		}
		if typ == nil {
			return fail(fmt.Errorf("unknown parameter &%s: the query does not use it", name))
		}
		v, err := parseParam(*typ, raw)
		if err != nil {
			return fail(fmt.Errorf("parameter &%s: %w", name, err))
		}
		params[name] = v
	}
	s, err := sqlite.Open(args[1], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	res, err := q.Run(context.Background(), s, params)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, strings.Join(res.Columns, "\t"))
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = formatDimension(v)
			if n, ok := v.(value.Number); ok {
				cells[i] = n.String()
			}
		}
		fmt.Fprintln(stdout, strings.Join(cells, "\t"))
	}
	return 0
}

func parseParam(t query.Type, raw string) (any, error) {
	switch t.Kind {
	case query.KindNumber:
		return value.Parse(raw)
	case query.KindBool:
		return strconv.ParseBool(raw)
	case query.KindRef:
		if raw == "" {
			return nil, nil
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("a reference is the number of the record: %w", err)
		}
		return value.Ref{Catalog: t.Target, ID: id}, nil
	}
	return raw, nil
}
