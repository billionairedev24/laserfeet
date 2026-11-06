package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"laserfeet/lang"
	"laserfeet/pg"
)

// loadDotEnv reads settings from a .env file in dir (KEY=value lines, # for
// comments), for env("KEY", ...). Settings already in the environment win, so
// a real deployment's settings are never replaced by a forgotten .env.
func loadDotEnv(dir string) {
	f, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

// migrate is lf migrate [folder]: apply the migrations that haven't run yet
// to the database in DATABASE_URL.
func migrate(args []string) int {
	dir := "migrations"
	if len(args) > 0 {
		dir = args[0]
	}
	loadDotEnv(".")
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "Set DATABASE_URL to the database to change, like DATABASE_URL=postgres://user:password@localhost/shop lf migrate")
		return 2
	}
	cfg, err := pg.ParseURL(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't read the migrations folder %s\n", dir)
		return 2
	}
	var steps []lang.Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		steps = append(steps, lang.Migration{Name: e.Name(), SQL: string(b)})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })
	pool := pg.NewPool(cfg, 2)
	applied, err := lang.Migrate(pool, steps)
	for _, n := range applied {
		fmt.Println("applied " + n)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(applied) == 0 {
		fmt.Println("the database is up to date")
	}
	return 0
}
