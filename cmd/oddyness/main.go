package main

import (
	"fmt"
	"io"
	"os"
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

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oddyness <command>")
		return 2
	}

	fmt.Fprintf(stderr, "oddyness: unknown command %q\n", args[0])
	return 2
}
