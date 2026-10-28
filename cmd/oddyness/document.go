package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

const documentUsage = `usage:
  oddyness document add <config-dir> <db-file> <document> [<field>=<value>...] [--row <table> <field>=<value>...]...
  oddyness document get <config-dir> <db-file> <document> <id>
  oddyness document list <config-dir> <db-file> <document>
  oddyness document post <config-dir> <db-file> <document> <id>
  oddyness document unpost <config-dir> <db-file> <document> <id>`

// runDocument выполняет команды работы с документами. `add` записывает шапку и
// строки атомарно: любая ошибка оставляет базу без изменений.
func runDocument(args []string, stdout, stderr io.Writer) int {
	need := map[string]int{"add": 4, "get": 5, "list": 4, "post": 5, "unpost": 5}
	if len(args) == 0 || need[args[0]] == 0 || len(args) < need[args[0]] || (args[0] != "add" && len(args) != need[args[0]]) {
		fmt.Fprintln(stderr, documentUsage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	m, err := metadata.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	d, err := findDocument(m, args[3])
	if err != nil {
		return fail(err)
	}
	s, err := sqlite.Open(args[2], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	ctx := context.Background()
	switch args[0] {
	case "add":
		header, tables, err := parseDocument(d, args[4:])
		if err != nil {
			return fail(err)
		}
		id, err := s.InsertDocument(ctx, d.ID, header, tables)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "id %d\n", id)
	case "get":
		id, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return fail(fmt.Errorf("invalid id %q", args[4]))
		}
		doc, err := s.GetDocument(ctx, d.ID, id)
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, formatDocument(d, doc))
	case "post", "unpost":
		id, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return fail(fmt.Errorf("invalid id %q", args[4]))
		}
		pl, err := platform.Open(s, args[1], platform.Options{})
		if err != nil {
			return fail(err)
		}
		if args[0] == "unpost" {
			if err := pl.Unpost(ctx, d.ID, id); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "unposted %d\n", id)
			break
		}
		if err := pl.Post(ctx, d.ID, id); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "posted %d\n", id)
	case "list":
		docs, err := s.ListDocuments(ctx, d.ID)
		if err != nil {
			return fail(err)
		}
		for _, doc := range docs {
			fmt.Fprint(stdout, formatDocument(d, doc))
		}
	}
	return 0
}

func findDocument(m *metadata.Model, key string) (*metadata.Document, error) {
	for i := range m.Documents {
		d := &m.Documents[i]
		if d.ID == key || strings.EqualFold(d.Name, key) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("unknown document %q", key)
}

// parseDocument разбирает `поле=значение...` шапки и группы `--row <часть> поле=значение...`.
func parseDocument(d *metadata.Document, args []string) (map[string]any, map[string][]map[string]any, error) {
	end := len(args)
	for i, a := range args {
		if a == "--row" {
			end = i
			break
		}
	}
	owner := fmt.Sprintf("document %q", d.Name)
	header, err := parseFieldAssignments(owner, d.Fields, args[:end])
	if err != nil {
		return nil, nil, err
	}
	tables := map[string][]map[string]any{}
	rest := args[end:]
	for len(rest) > 0 {
		if rest[0] != "--row" || len(rest) < 2 {
			return nil, nil, fmt.Errorf("--row needs a table name")
		}
		var part *metadata.TablePart
		for i := range d.Tables {
			if d.Tables[i].ID == rest[1] || strings.EqualFold(d.Tables[i].Name, rest[1]) {
				part = &d.Tables[i]
			}
		}
		if part == nil {
			return nil, nil, fmt.Errorf("%s has no table %q", owner, rest[1])
		}
		next := 2
		for next < len(rest) && rest[next] != "--row" {
			next++
		}
		row, err := parseFieldAssignments(fmt.Sprintf("table %q", part.Name), part.Fields, rest[2:next])
		if err != nil {
			return nil, nil, err
		}
		tables[part.ID] = append(tables[part.ID], row)
		rest = rest[next:]
	}
	return header, tables, nil
}

func formatDocument(d *metadata.Document, doc sqlite.Document) string {
	var sb strings.Builder
	sb.WriteString(formatValues(doc.ID, d.Fields, doc.Header))
	if doc.Posted {
		sb.WriteString(" [posted]")
	}
	sb.WriteByte('\n')
	for _, t := range d.Tables {
		for _, row := range doc.Tables[t.ID] {
			line := formatValues(row.ID, t.Fields, row.Values)
			fmt.Fprintf(&sb, "  %s[%s\n", t.Name, strings.Replace(line, ":", "]:", 1))
		}
	}
	return sb.String()
}
