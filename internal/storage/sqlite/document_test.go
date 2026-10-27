package sqlite

import (
	"errors"
	"testing"

	"github.com/unxed/oddyness/internal/value"
)

type lines = []map[string]any

func receiptHeader(number, date string) map[string]any {
	return map[string]any{"receipt-number": number, "receipt-date": date, "receipt-comment": "тест"}
}

func receiptLine(t testing.TB, product int64, qty string) map[string]any {
	return map[string]any{"receipt-product": Ref{Catalog: "goods", ID: product}, "receipt-qty": num(t, qty)}
}

func items(rows ...map[string]any) map[string][]map[string]any {
	return map[string][]map[string]any{"receipt-items": rows}
}

func seedGoods(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.Insert(ctx, "goods", goods(t, string(rune('A'+i)), "товар", "0")); err != nil {
			t.Fatal(err)
		}
	}
}

func docCount(t *testing.T, s *Store) int {
	t.Helper()
	docs, err := s.ListDocuments(ctx, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	return len(docs)
}

func TestDocumentRoundTripAndRestart(t *testing.T) {
	m := loadModel(t)
	s, path := openTemp(t)
	seedGoods(t, s, 2)
	id, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "10.500"), receiptLine(t, 2, "2.250")))
	if err != nil || id != 1 {
		t.Fatalf("InsertDocument = %d, %v", id, err)
	}
	check := func(d Document) {
		t.Helper()
		if d.ID != id || d.Header["receipt-number"] != "R-1" || d.Header["receipt-date"] != "2026-10-27" || d.Header["receipt-comment"] != "тест" {
			t.Errorf("header = %+v", d)
		}
		rows := d.Tables["receipt-items"]
		if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
			t.Fatalf("rows = %+v", rows)
		}
		q1 := rows[0].Values["receipt-qty"].(value.Number)
		q2 := rows[1].Values["receipt-qty"].(value.Number)
		if q1.String() != "10.500" || q2.String() != "2.250" || rows[0].Values["receipt-product"] != (Ref{Catalog: "goods", ID: 1}) || rows[1].Values["receipt-product"] != (Ref{Catalog: "goods", ID: 2}) {
			t.Errorf("rows = %+v", rows)
		}
	}
	d, err := s.GetDocument(ctx, "receipt", id)
	if err != nil {
		t.Fatal(err)
	}
	check(d)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	docs, err := s2.ListDocuments(ctx, "receipt")
	if err != nil || len(docs) != 1 {
		t.Fatalf("ListDocuments = %v, %v", docs, err)
	}
	check(docs[0])
	// другой вид документа независим: тот же номер допустим
	if _, err := s2.InsertDocument(ctx, "issue", map[string]any{"issue-number": "R-1", "issue-date": "2026-10-28", "issue-recipient": "цех"}, nil); err != nil {
		t.Errorf("same number in another document kind: %v", err)
	}
}

func TestDocumentWithoutRows(t *testing.T) {
	s, _ := openTemp(t)
	id, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-0", "2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDocument(ctx, "receipt", id)
	if err != nil || len(d.Tables["receipt-items"]) != 0 {
		t.Errorf("document without rows: %+v, %v", d, err)
	}
}

func TestDocumentDuplicateNumber(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	if _, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "1"))); err != nil {
		t.Fatal(err)
	}
	_, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-28"), items(receiptLine(t, 1, "5")))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate number: %v", err)
	}
	docs, _ := s.ListDocuments(ctx, "receipt")
	if len(docs) != 1 || len(docs[0].Tables["receipt-items"]) != 1 {
		t.Errorf("duplicate left data behind: %+v", docs)
	}
}

func TestDocumentInvalidInput(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	good := func() map[string]any { return receiptHeader("R-1", "2026-10-27") }
	ok := receiptLine(t, 1, "1")
	cases := map[string]struct {
		header map[string]any
		tables map[string][]map[string]any
	}{
		"second row too precise":  {good(), items(ok, receiptLine(t, 1, "0.0001"))},
		"too many integer digits": {good(), items(receiptLine(t, 1, "1000000000000"))},
		"zero quantity":           {good(), items(receiptLine(t, 1, "0"))},
		"negative quantity":       {good(), items(ok, receiptLine(t, 1, "-1"))},
		"bad date":                {receiptHeader("R-1", "2026-13-01"), items(ok)},
		"date in other format":    {receiptHeader("R-1", "27.10.2026"), items(ok)},
		"missing header field":    {map[string]any{"receipt-number": "R-1", "receipt-date": "2026-10-27"}, items(ok)},
		"unknown header field":    {map[string]any{"receipt-number": "R-1", "receipt-date": "2026-10-27", "receipt-comment": "", "x": 1}, items(ok)},
		"missing row field":       {good(), items(map[string]any{"receipt-qty": num(t, "1")}, ok)},
		"unknown row field":       {good(), items(map[string]any{"receipt-qty": num(t, "1"), "receipt-product": Ref{Catalog: "goods", ID: 1}, "x": 1})},
		"unknown table":           {good(), map[string][]map[string]any{"nope": {ok}}},
		"quantity type":           {good(), items(map[string]any{"receipt-qty": 1.5, "receipt-product": Ref{Catalog: "goods", ID: 1}})},
		"reference to a catalog":  {good(), items(map[string]any{"receipt-qty": num(t, "1"), "receipt-product": Ref{Catalog: "other", ID: 1}})},
	}
	for name, c := range cases {
		if _, err := s.InsertDocument(ctx, "receipt", c.header, c.tables); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
	if n := docCount(t, s); n != 0 {
		t.Errorf("invalid documents left %d records", n)
	}
	if _, err := s.InsertDocument(ctx, "nope", good(), nil); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown document: %v", err)
	}
	if _, err := s.GetDocument(ctx, "receipt", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if err := s.DeleteDocument(ctx, "receipt", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown id: %v", err)
	}
	if err := s.UpdateDocument(ctx, "receipt", 99, good(), nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("update unknown id: %v", err)
	}
}

// Ошибка базы на второй строке откатывает и шапку, и первую строку: после
// неудачи идентификатор освобождается, а у нового документа нет чужих строк.
func TestDocumentRollbackOnSecondRow(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	_, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "1"), receiptLine(t, 999, "2")))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("reference to a missing product: %v", err)
	}
	if n := docCount(t, s); n != 0 {
		t.Fatalf("header survived the rollback: %d documents", n)
	}
	id, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "3")))
	if err != nil || id != 1 {
		t.Fatalf("InsertDocument after rollback = %d, %v", id, err)
	}
	d, _ := s.GetDocument(ctx, "receipt", id)
	rows := d.Tables["receipt-items"]
	if len(rows) != 1 || rows[0].Values["receipt-qty"].(value.Number).String() != "3" {
		t.Errorf("rows after rollback: %+v", rows)
	}
}

func TestDocumentUpdate(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 2)
	id, _ := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "1"), receiptLine(t, 2, "2")))
	other, _ := s.InsertDocument(ctx, "receipt", receiptHeader("R-2", "2026-10-27"), nil)

	// успешная замена: две строки -> одна, новая шапка
	h := receiptHeader("R-1", "2026-11-01")
	h["receipt-comment"] = "исправлено"
	if err := s.UpdateDocument(ctx, "receipt", id, h, items(receiptLine(t, 2, "7.125"))); err != nil {
		t.Fatal(err)
	}
	d, _ := s.GetDocument(ctx, "receipt", id)
	rows := d.Tables["receipt-items"]
	if d.Header["receipt-date"] != "2026-11-01" || d.Header["receipt-comment"] != "исправлено" || len(rows) != 1 || rows[0].Values["receipt-qty"].(value.Number).String() != "7.125" {
		t.Fatalf("after update: %+v", d)
	}
	// конфликт номера и ошибка строки не меняют документ
	before, _ := s.GetDocument(ctx, "receipt", id)
	if err := s.UpdateDocument(ctx, "receipt", id, receiptHeader("R-2", "2026-12-01"), items(receiptLine(t, 1, "9"))); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate number on update: %v", err)
	}
	if err := s.UpdateDocument(ctx, "receipt", id, receiptHeader("R-1", "2026-12-01"), items(receiptLine(t, 1, "9"), receiptLine(t, 999, "1"))); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad reference on update: %v", err)
	}
	after, _ := s.GetDocument(ctx, "receipt", id)
	if after.Header["receipt-date"] != before.Header["receipt-date"] || len(after.Tables["receipt-items"]) != 1 ||
		after.Tables["receipt-items"][0].Values["receipt-qty"].(value.Number).String() != "7.125" {
		t.Errorf("failed update changed the document: %+v", after)
	}
	if o, _ := s.GetDocument(ctx, "receipt", other); o.Header["receipt-number"] != "R-2" {
		t.Errorf("another document changed: %+v", o)
	}
}

func TestDocumentUsesProductsAndCascades(t *testing.T) {
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	id, _ := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), items(receiptLine(t, 1, "1")))
	// используемый товар удалить нельзя
	if err := s.Delete(ctx, "goods", 1); !errors.Is(err, ErrInvalid) {
		t.Errorf("Delete of a product used by a document: %v", err)
	}
	if _, err := s.Get(ctx, "goods", 1); err != nil {
		t.Errorf("used product vanished: %v", err)
	}
	// удаление документа убирает строки, после этого товар удалить можно
	if err := s.DeleteDocument(ctx, "receipt", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "goods", 1); err != nil {
		t.Errorf("Delete after the document is gone: %v", err)
	}
	seedGoods(t, s, 1)
	id2, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-27"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDocument(ctx, "receipt", id2); len(d.Tables["receipt-items"]) != 0 {
		t.Errorf("stale rows of a deleted document: %+v", d.Tables)
	}
}
