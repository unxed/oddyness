package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/unxed/oddyness/internal/bsl"
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

// runBSL выполняет `oddyness bsl check <file>`: разбор файла и диагностика.
func runBSL(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: oddyness bsl check <file>")
		return 2
	}
	path := args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 2
	}
	if !utf8.Valid(data) {
		fmt.Fprintf(stderr, "%s: file is not valid UTF-8\n", path)
		return 1
	}
	m, err := bsl.Parse(strings.TrimPrefix(string(data), "\uFEFF"))
	if err != nil {
		fmt.Fprintf(stderr, "%s:%v\n", path, err)
		return 1
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
