package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("run(version) exit = %d, want 0", got)
	}
	if got, want := stdout.String(), "oddyness dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"nope"}, &stdout, &stderr); got == 0 {
		t.Fatal("run(unknown command) exit = 0, want non-zero")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "nope"`) {
		t.Fatalf("stderr = %q, want clear unknown-command error", got)
	}
}

func TestRunWithoutCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(nil, &stdout, &stderr); got == 0 {
		t.Fatal("run(no arguments) exit = 0, want non-zero")
	}
	if got := stderr.String(); !strings.Contains(got, "usage: oddyness <command>") {
		t.Fatalf("stderr = %q, want usage", got)
	}
}
