package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// probe — проба корпуса совместимости (testdata/corpus/cases.json, docs/implementation/compat-matrix.md).
type probe struct {
	ID          string   `json:"id"`    // опыт исследования (docs/research/04-validation.md)
	Probe       string   `json:"probe"` // буква пробы
	Title       string   `json:"title"`
	Source      string   `json:"source"`  // источник ожидания: own или original:<версия>
	Version     string   `json:"version"` // версия оригинала, если source = original
	Kind        string   `json:"kind"`    // bsl или cli
	Unsupported bool     `json:"unsupported"`
	Module      string   `json:"module"`
	Function    string   `json:"function"`
	Args        []string `json:"args"`
	Expect      *string  `json:"expect"`
	ExpectError *string  `json:"expect_error"`
	Steps       []step   `json:"steps"`
}

type step struct {
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Expect   *string           `json:"expect"`   // точный stdout
	Contains []string          `json:"contains"` // подстроки stdout
	Error    string            `json:"error"`    // подстрока stderr; код 1
}

func loadCorpus(t *testing.T) []probe {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ps []probe
	if err := json.Unmarshal(data, &ps); err != nil {
		t.Fatal(err)
	}
	return ps
}

// researchIDs — идентификаторы опытов из таблицы исследования, в порядке таблицы.
func researchIDs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "research", "04-validation.md"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range regexp.MustCompile(`(?m)^\| ([A-Z]+-\d+) \|`).FindAllStringSubmatch(string(data), -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func runProbe(t *testing.T, p probe) {
	t.Helper()
	switch p.Kind {
	case "bsl":
		file := filepath.Join(t.TempDir(), "probe.bsl")
		if err := os.WriteFile(file, []byte(p.Module), 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errs := cli(t, append([]string{"bsl", "run", file, p.Function}, p.Args...)...)
		switch {
		case p.Expect != nil && p.ExpectError != nil:
			t.Fatal("a probe must have either expect or expect_error")
		case p.Expect != nil:
			if code != 0 || out != *p.Expect+"\n" {
				t.Errorf("exit=%d stdout=%q stderr=%q; want %q", code, out, errs, *p.Expect)
			}
		case p.ExpectError != nil:
			if code == 0 || out != "" || errs == "" || !strings.Contains(errs, *p.ExpectError) {
				t.Errorf("exit=%d stdout=%q stderr=%q; want an error containing %q", code, out, errs, *p.ExpectError)
			}
		default:
			t.Fatal("a probe has no expectation")
		}
	case "cli":
		db := filepath.Join(t.TempDir(), "db.sqlite")
		sub := func(s string) string { return strings.NewReplacer("$CFG", configDir, "$DB", db).Replace(s) }
		for i, st := range p.Steps {
			args := make([]string, len(st.Args))
			for j, a := range st.Args {
				args[j] = sub(a)
			}
			old := map[string]*string{}
			for k, v := range st.Env {
				if cur, ok := os.LookupEnv(k); ok {
					c := cur
					old[k] = &c
				} else {
					old[k] = nil
				}
				os.Setenv(k, v)
			}
			code, out, errs := cli(t, args...)
			for k, v := range old {
				if v == nil {
					os.Unsetenv(k)
				} else {
					os.Setenv(k, *v)
				}
			}
			if st.Error != "" {
				if code != 1 || !strings.Contains(errs, st.Error) {
					t.Fatalf("step %d %v: exit=%d stderr=%q; want an error containing %q", i+1, args[:2], code, errs, st.Error)
				}
				continue
			}
			if code != 0 {
				t.Fatalf("step %d %v: exit=%d stderr=%q", i+1, args[:2], code, errs)
			}
			if st.Expect != nil && out != sub(*st.Expect) {
				t.Fatalf("step %d %v: stdout=%q, want %q", i+1, args[:2], out, *st.Expect)
			}
			for _, c := range st.Contains {
				if !strings.Contains(out, c) {
					t.Fatalf("step %d %v: stdout=%q has no %q", i+1, args[:2], out, c)
				}
			}
		}
	default:
		t.Fatalf("unknown probe kind %q", p.Kind)
	}
}

// Каждая проба корпуса выполняется и даёт записанный результат. Проба без источника оригинала — собственная (own), и
// только проба с source original:<версия> и версией могла бы подтверждать совместимость.
func TestCorpusProbes(t *testing.T) {
	ids := map[string]bool{}
	for _, id := range researchIDs(t) {
		ids[id] = true
	}
	seen := map[string]bool{}
	for _, p := range loadCorpus(t) {
		name := p.ID + "/" + p.Probe
		t.Run(name, func(t *testing.T) {
			if !ids[p.ID] {
				t.Fatalf("%s is not an experiment of docs/research/04-validation.md", p.ID)
			}
			if seen[name] {
				t.Fatalf("duplicate probe %s", name)
			}
			seen[name] = true
			switch {
			case p.Source == "own" && p.Version == "":
			case strings.HasPrefix(p.Source, "original:") && p.Version != "":
			default:
				t.Fatalf("source %q / version %q: the source of the expectation must be own or original:<name> with a version", p.Source, p.Version)
			}
			runProbe(t, p)
		})
	}
}

// Матрица docs/implementation/compat-matrix.md совпадает с корпусом: строка на каждый опыт исследования, столбцы
// «Оригинал», «Наша реализация» и «Пробы» вычисляются из проб. Подтверждённой совместимости без пробы с источником
// оригинала матрица не заявляет.
func TestCompatMatrixMatchesCorpus(t *testing.T) {
	by := map[string][]probe{}
	for _, p := range loadCorpus(t) {
		by[p.ID] = append(by[p.ID], p)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "implementation", "compat-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if m := regexp.MustCompile(`^\| ([A-Z]+-\d+) \|`).FindStringSubmatch(line); m != nil {
			cells := strings.Split(strings.Trim(strings.TrimRight(line, "\r"), "| "), " | ") // файл мог быть выгружен с CRLF
			rows[m[1]] = cells
		}
	}
	for _, id := range researchIDs(t) {
		cells, ok := rows[id]
		if !ok || len(cells) != 5 {
			t.Errorf("%s: no row in the matrix (or the row is malformed): %v", id, cells)
			continue
		}
		ps := by[id]
		orig, ours := "не проверено (оригинала нет)", "нет пробы"
		var names []string
		unsupported := 0
		for _, p := range ps {
			names = append(names, p.ID+"/"+p.Probe)
			if p.Unsupported {
				unsupported++
			}
			if strings.HasPrefix(p.Source, "original:") {
				t.Errorf("%s: a probe with an original source needs the matrix columns for confirmed/diverged results; extend the matrix first", id)
			}
		}
		switch {
		case len(ps) == 0:
		case unsupported == len(ps):
			ours = "не поддержано (явная ошибка)"
		case unsupported == 0:
			ours = "реализовано"
		default:
			ours = "частично"
		}
		probes := "—"
		if len(names) > 0 {
			probes = strings.Join(names, ", ")
		}
		if cells[2] != orig || cells[3] != ours || cells[4] != probes {
			t.Errorf("%s: matrix says %q / %q / %q, corpus says %q / %q / %q", id, cells[2], cells[3], cells[4], orig, ours, probes)
		}
	}
	if len(rows) != len(researchIDs(t)) {
		t.Errorf("the matrix has %d rows, the research has %d experiments", len(rows), len(researchIDs(t)))
	}
}
