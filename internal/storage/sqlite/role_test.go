package sqlite

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRole(t *testing.T) {
	for in, want := range map[string]Role{"read": RoleReader, "Reader": RoleReader, " READ ": RoleReader, "operator": RoleOperator, "Operator": RoleOperator} {
		if got, err := ParseRole(in); err != nil || got != want {
			t.Errorf("ParseRole(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "admin", "write", "read-only"} {
		if _, err := ParseRole(in); err == nil || !strings.Contains(err.Error(), "unknown role") {
			t.Errorf("ParseRole(%q) = %v", in, err)
		}
	}
	if RoleReader.String() != "read" || RoleOperator.String() != "operator" || Role(7).String() != "Role(7)" {
		t.Errorf("names: %s %s %s", RoleReader, RoleOperator, Role(7))
	}
}

// Роль чтения: каждая публичная операция записи отклоняется ErrDenied до проверки значений и ничего не меняет;
// чтение разрешено; роль оператора возвращает запись.
func TestReaderRoleDeniesEveryWrite(t *testing.T) {
	s, _ := openTemp(t)
	if !s.CanWrite() || s.Role() != RoleOperator {
		t.Fatal("the default role must be the operator")
	}
	seedGoods(t, s, 2)
	docID, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-28"), items(receiptLine(t, 1, "1.500")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceMovements(ctx, "stock", "receipt", docID, []map[string]any{{"stock-product": Ref{Catalog: "goods", ID: 1}, "stock-qty": num(t, "1.500")}}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		recs, _ := s.List(ctx, "goods")
		docs, _ := s.ListDocuments(ctx, "receipt")
		bs, _ := s.Balances(ctx, "stock", BalanceQuery{})
		mv, _ := s.Movements(ctx, "stock", "receipt", docID)
		return strings.Join([]string{
			strings.Repeat("g", len(recs)), strings.Repeat("d", len(docs)), strings.Repeat("b", len(bs)), strings.Repeat("m", len(mv)),
		}, "|")
	}
	before := snapshot()

	s.SetRole(RoleReader)
	if s.CanWrite() || s.Role() != RoleReader {
		t.Fatal("the role was not applied")
	}
	called := false
	denied := map[string]error{
		"Insert": func() error { _, err := s.Insert(ctx, "goods", goods(t, "X", "Н", "0")); return err }(),
		// недопустимые значения не должны подменять отказ: роль проверяется первой
		"Insert invalid": func() error { _, err := s.Insert(ctx, "goods", map[string]any{"nope": 1}); return err }(),
		"Insert unknown": func() error { _, err := s.Insert(ctx, "no-such-catalog", nil); return err }(),
		"Update":         s.Update(ctx, "goods", 1, goods(t, "A2", "Н", "0")),
		"Delete":         s.Delete(ctx, "goods", 2),
		"InsertDocument": func() error {
			_, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-2", "2026-10-28"), nil)
			return err
		}(),
		"UpdateDocument":     s.UpdateDocument(ctx, "receipt", docID, receiptHeader("R-1", "2026-10-29"), items(receiptLine(t, 1, "2"))),
		"DeleteDocument":     s.DeleteDocument(ctx, "receipt", docID),
		"ReplaceMovements":   s.ReplaceMovements(ctx, "stock", "receipt", docID, nil),
		"Tx":                 s.Tx(ctx, func(*Tx) error { called = true; return nil }),
		"Tx.ReplaceMovement": s.Tx(ctx, func(tx *Tx) error { called = true; return tx.ReplaceMovements("stock", "receipt", docID, nil) }),
	}
	for name, err := range denied {
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "role read") {
			t.Errorf("%s under the read role: %v", name, err)
		}
	}
	if called {
		t.Error("the transaction callback ran under the read role")
	}
	if got := snapshot(); got != before {
		t.Errorf("the read role changed the data: %s -> %s", before, got)
	}
	// чтение разрешено
	if _, err := s.Get(ctx, "goods", 1); err != nil {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.GetDocument(ctx, "receipt", docID); err != nil {
		t.Errorf("GetDocument: %v", err)
	}

	s.SetRole(RoleOperator)
	if err := s.Update(ctx, "goods", 1, goods(t, "A2", "Н", "0")); err != nil {
		t.Errorf("the operator role cannot write: %v", err)
	}
}
