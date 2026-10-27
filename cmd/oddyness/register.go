package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// runRegister выполняет `oddyness register balance <config-dir> <db-file> <register>`:
// точные остатки регистра на текущий момент.
func runRegister(args []string, stdout, stderr io.Writer) int {
	if len(args) != 4 || args[0] != "balance" {
		fmt.Fprintln(stderr, "usage: oddyness register balance <config-dir> <db-file> <register>")
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
	var reg *metadata.Register
	for i := range m.Registers {
		if m.Registers[i].ID == args[3] || strings.EqualFold(m.Registers[i].Name, args[3]) {
			reg = &m.Registers[i]
		}
	}
	if reg == nil {
		return fail(fmt.Errorf("unknown register %q", args[3]))
	}
	s, err := sqlite.Open(args[2], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	balances, err := s.Balances(context.Background(), reg.ID, sqlite.BalanceQuery{})
	if err != nil {
		return fail(err)
	}
	for _, b := range balances {
		var parts []string
		for _, f := range reg.Dimensions {
			parts = append(parts, fmt.Sprintf("%s=%s", f.Name, formatDimension(b.Dimensions[f.ID])))
		}
		for _, f := range reg.Resources {
			parts = append(parts, fmt.Sprintf("%s=%s", f.Name, b.Resources[f.ID]))
		}
		fmt.Fprintln(stdout, strings.Join(parts, " "))
	}
	return 0
}

func formatDimension(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case sqlite.Ref:
		return fmt.Sprint(x.ID)
	case string:
		return fmt.Sprintf("%q", x)
	}
	return fmt.Sprint(v)
}
