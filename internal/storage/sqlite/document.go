package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
)

// Document — запись вида документа: шапка и строки табличных частей. Номер строки
// (Record.ID у строк) считается с единицы по порядку записи; Values индексированы по id поля.
type Document struct {
	ID     int64
	Posted bool // документ проведён: его движения записаны в регистры
	Header map[string]any
	Tables map[string][]Record
}

func (s *Store) document(id string) (*metadata.Document, error) {
	for i := range s.model.Documents {
		if s.model.Documents[i].ID == id {
			return &s.model.Documents[i], nil
		}
	}
	return nil, fmt.Errorf("%w: document %q", ErrUnknown, id)
}

type encodedPart struct {
	table metadata.TablePart
	rows  [][]any
}

// encodeDocument проверяет шапку и все строки до начала транзакции.
func encodeDocument(d *metadata.Document, header map[string]any, tables map[string][]map[string]any) ([]any, []encodedPart, error) {
	h, err := encodeFields("document "+d.ID, d.Fields, header)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	var parts []encodedPart
	for _, t := range d.Tables {
		known[t.ID] = true
		p := encodedPart{table: t}
		for i, row := range tables[t.ID] {
			enc, err := encodeFields(fmt.Sprintf("table %s.%s row %d", d.ID, t.ID, i+1), t.Fields, row)
			if err != nil {
				return nil, nil, err
			}
			p.rows = append(p.rows, enc)
		}
		parts = append(parts, p)
	}
	for id := range tables {
		if !known[id] {
			return nil, nil, invalid("unknown table %q in document %q", id, d.ID)
		}
	}
	return h, parts, nil
}

func insertRows(ctx context.Context, tx *sql.Tx, d *metadata.Document, owner int64, parts []encodedPart) error {
	for _, p := range parts {
		cols := []string{`"owner"`, `"line"`}
		marks := []string{"?", "?"}
		for _, f := range p.table.Fields {
			cols = append(cols, columnName(f.ID))
			marks = append(marks, "?")
		}
		q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, partTable(d.ID, p.table.ID), strings.Join(cols, ", "), strings.Join(marks, ", "))
		for i, row := range p.rows {
			if _, err := tx.ExecContext(ctx, q, append([]any{owner, i + 1}, row...)...); err != nil {
				return mapErr(err)
			}
		}
	}
	return nil
}

// InsertDocument атомарно записывает шапку и строки всех табличных частей
// (tables: id части -> строки в порядке записи). Любая ошибка — проверки,
// уникальности номера, ссылки на несуществующую запись — откатывает всё.
func (s *Store) InsertDocument(ctx context.Context, docID string, header map[string]any, tables map[string][]map[string]any) (int64, error) {
	d, err := s.document(docID)
	if err != nil {
		return 0, err
	}
	h, parts, err := encodeDocument(d, header, tables)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkUniqueIn(ctx, tx, docTable(d.ID), d.Fields, h, 0); err != nil {
			return err
		}
		cols := make([]string, len(d.Fields))
		marks := make([]string, len(d.Fields))
		for i, f := range d.Fields {
			cols[i], marks[i] = columnName(f.ID), "?"
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, docTable(d.ID), strings.Join(cols, ", "), strings.Join(marks, ", ")), h...)
		if err != nil {
			return mapErr(err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertRows(ctx, tx, d, id, parts)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateDocument атомарно заменяет шапку и все строки черновика.
func (s *Store) UpdateDocument(ctx context.Context, docID string, id int64, header map[string]any, tables map[string][]map[string]any) error {
	d, err := s.document(docID)
	if err != nil {
		return err
	}
	h, parts, err := encodeDocument(d, header, tables)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkUniqueIn(ctx, tx, docTable(d.ID), d.Fields, h, id); err != nil {
			return err
		}
		sets := make([]string, len(d.Fields))
		for i, f := range d.Fields {
			sets[i] = columnName(f.ID) + " = ?"
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE "id" = ?`, docTable(d.ID), strings.Join(sets, ", ")), append(h, id)...)
		if err != nil {
			return mapErr(err)
		}
		if err := affected(res, docID, id); err != nil {
			return err
		}
		for _, t := range d.Tables {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE "owner" = ?`, partTable(d.ID, t.ID)), id); err != nil {
				return mapErr(err)
			}
		}
		return insertRows(ctx, tx, d, id, parts)
	})
}

// DeleteDocument удаляет документ вместе со строками.
func (s *Store) DeleteDocument(ctx context.Context, docID string, id int64) error {
	d, err := s.document(docID)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		// движения удаляемого регистратора не должны остаться сиротами
		for _, r := range s.model.Registers {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE "recorder_doc" = ? AND "recorder_id" = ?`, regTable(r.ID)), d.ID, id); err != nil {
				return mapErr(err)
			}
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE "id" = ?`, docTable(d.ID)), id)
		if err != nil {
			return mapErr(err)
		}
		return affected(res, docID, id)
	})
}

func selectFields(table, idColumn string, fields []metadata.Field) string {
	cols := []string{idColumn}
	for _, f := range fields {
		cols = append(cols, columnName(f.ID))
	}
	return fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(cols, ", "), table)
}

func loadLines(ctx context.Context, q querier, d *metadata.Document, id int64) (map[string][]Record, error) {
	out := map[string][]Record{}
	for _, t := range d.Tables {
		rows, err := q.QueryContext(ctx, selectFields(partTable(d.ID, t.ID), `"line"`, t.Fields)+` WHERE "owner" = ? ORDER BY "line"`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			rec, err := scanFields(t.Fields, rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[t.ID] = append(out[t.ID], rec)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// GetDocument возвращает документ или ErrNotFound.
func (s *Store) GetDocument(ctx context.Context, docID string, id int64) (Document, error) {
	return getDocument(ctx, s.db, s, docID, id)
}

// GetDocument возвращает документ внутри транзакции.
func (t *Tx) GetDocument(docID string, id int64) (Document, error) {
	return getDocument(t.ctx, t.tx, t.s, docID, id)
}

func getDocument(ctx context.Context, q querier, s *Store, docID string, id int64) (Document, error) {
	d, err := s.document(docID)
	if err != nil {
		return Document{}, err
	}
	var posted int
	err = q.QueryRowContext(ctx, fmt.Sprintf(`SELECT "posted" FROM %s WHERE "id" = ?`, docTable(d.ID)), id).Scan(&posted)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, fmt.Errorf("%w: %s %d", ErrNotFound, docID, id)
	}
	if err != nil {
		return Document{}, err
	}
	rec, err := scanFields(d.Fields, q.QueryRowContext(ctx, selectFields(docTable(d.ID), `"id"`, d.Fields)+` WHERE "id" = ?`, id))
	if err != nil {
		return Document{}, err
	}
	lines, err := loadLines(ctx, q, d, id)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: rec.ID, Posted: posted == 1, Header: rec.Values, Tables: lines}, nil
}

// SetPosted ставит или снимает признак проведения документа; сами движения пишет
// ReplaceMovements в той же транзакции.
func (t *Tx) SetPosted(docID string, id int64, posted bool) error {
	d, err := t.s.document(docID)
	if err != nil {
		return err
	}
	v := 0
	if posted {
		v = 1
	}
	res, err := t.tx.ExecContext(t.ctx, fmt.Sprintf(`UPDATE %s SET "posted" = ? WHERE "id" = ?`, docTable(d.ID)), v, id)
	if err != nil {
		return mapErr(err)
	}
	return affected(res, docID, id)
}

// ListDocuments возвращает все документы вида по возрастанию идентификатора.
func (s *Store) ListDocuments(ctx context.Context, docID string) ([]Document, error) {
	d, err := s.document(docID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, selectFields(docTable(d.ID), `"id"`, d.Fields)+` ORDER BY "id"`)
	if err != nil {
		return nil, err
	}
	var heads []Record
	for rows.Next() {
		rec, err := scanFields(d.Fields, rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		heads = append(heads, rec)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]Document, 0, len(heads))
	for _, h := range heads {
		doc, err := getDocument(ctx, s.db, s, docID, h.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}
