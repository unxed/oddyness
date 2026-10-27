package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/unxed/oddyness/internal/bsl"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
	"github.com/unxed/oddyness/internal/vm"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintf(stdout, "oddyness %s\n", version)
		return 0
	}

	if len(args) > 0 && args[0] == "form" {
		return runForm(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "document" {
		return runDocument(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "db" {
		return runDB(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "catalog" {
		return runCatalog(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "config" {
		return runConfig(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "bsl" {
		return runBSL(args[1:], stdout, stderr)
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oddyness <command>")
		return 2
	}

	fmt.Fprintf(stderr, "oddyness: unknown command %q\n", args[0])
	return 2
}

// loadBSL читает и разбирает файл BSL (BOM допускается, не-UTF-8 отклоняется).
// code != 0 — процесс должен завершиться с этим кодом, ошибка уже напечатана.
func loadBSL(path string, stderr io.Writer) (m *bsl.Module, code int) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return nil, 2
	}
	if !utf8.Valid(data) {
		fmt.Fprintf(stderr, "%s: file is not valid UTF-8\n", path)
		return nil, 1
	}
	m, err = bsl.Parse(strings.TrimPrefix(string(data), "\uFEFF"))
	if err != nil {
		fmt.Fprintf(stderr, "%s:%v\n", path, err)
		return nil, 1
	}
	return m, 0
}

// runBSL выполняет `oddyness bsl check <file>` и
// `oddyness bsl run <file> <function> [args...]`.
func runBSL(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "check":
		return bslCheck(args[1], stdout, stderr)
	case len(args) >= 3 && args[0] == "run":
		return bslRun(args[1], args[2], args[3:], stdout, stderr)
	}
	fmt.Fprintln(stderr, "usage: oddyness bsl check <file> | oddyness bsl run <file> <function> [args...]")
	return 2
}

func bslCheck(path string, stdout, stderr io.Writer) int {
	m, code := loadBSL(path, stderr)
	if m == nil {
		return code
	}
	funcs := 0
	for _, r := range m.Routines {
		if r.IsFunction {
			funcs++
		}
	}
	fmt.Fprintf(stdout, "%s: ok, %d procedures, %d functions, %d module variable declarations\n",
		path, len(m.Routines)-funcs, funcs, len(m.Vars))
	return 0
}

// bslRun компилирует модуль и вызывает подпрограмму. Аргумент, записанный как
// число (точный литерал), передаётся числом, любой другой — строкой.
func bslRun(path, name string, rawArgs []string, stdout, stderr io.Writer) int {
	m, code := loadBSL(path, stderr)
	if m == nil {
		return code
	}
	prog, err := vm.Compile(m)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", path, err)
		return 1
	}
	args := make([]vm.Value, len(rawArgs))
	for i, a := range rawArgs {
		if n, perr := value.Parse(a); perr == nil {
			args[i] = vm.NumberValue(n)
		} else {
			args[i] = vm.StringValue(a)
		}
	}
	res, err := prog.Run(context.Background(), name, args, vm.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", path, err)
		return 1
	}
	fmt.Fprintln(stdout, res.String())
	return 0
}

// runConfig выполняет `oddyness config check <dir>`: загрузка конфигурации и
// список распознанных объектов либо адресная ошибка.
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: oddyness config check <dir>")
		return 2
	}
	m, err := metadata.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: ok, version %d, %d catalogs, %d documents\n", args[1], m.Version, len(m.Catalogs), len(m.Documents))
	for _, c := range m.Catalogs {
		fmt.Fprintf(stdout, "catalog %s %q: %d fields\n", c.ID, c.Name, len(c.Fields))
		for _, f := range c.Fields {
			fmt.Fprintf(stdout, "  %s %s %s\n", f.ID, f.Name, f.TypeString())
		}
	}
	for _, d := range m.Documents {
		fmt.Fprintf(stdout, "document %s %q: %d fields, %d tables\n", d.ID, d.Name, len(d.Fields), len(d.Tables))
		for _, f := range d.Fields {
			fmt.Fprintf(stdout, "  %s %s %s\n", f.ID, f.Name, f.TypeString())
		}
		for _, t := range d.Tables {
			fmt.Fprintf(stdout, "  table %s %q: %d fields\n", t.ID, t.Name, len(t.Fields))
			for _, f := range t.Fields {
				fmt.Fprintf(stdout, "    %s %s %s\n", f.ID, f.Name, f.TypeString())
			}
		}
	}
	return 0
}
