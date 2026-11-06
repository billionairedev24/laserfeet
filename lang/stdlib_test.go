//go:build !js

package lang

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSrc(t *testing.T, src string) string {
	t.Helper()
	var out bytes.Buffer
	if err := NewInterp(&out).Run(src); err != nil {
		t.Fatalf("unexpected problem: %v", err)
	}
	return out.String()
}

func TestJSONKeepsOrder(t *testing.T) {
	got := runSrc(t, `use json
show json.write({zebra: 1, apple: [1, 2], mid: {b: yes, a: nothing}})
back = json.read("{\"b\": 2, \"a\": [1, \"x\"]}")
show back.keys()
show back.a[1]`)
	want := "{\"zebra\":1,\"apple\":[1,2],\"mid\":{\"b\":true,\"a\":null}}\n[\"b\", \"a\"]\nx\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestStoreSavesSafely(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data.json")
	src := `use store
db = store.open("` + filepath.ToSlash(db) + `")
db.add("notes", {text: "hi"})
db.add("notes", {text: "there"})
db.update("notes", 2, {text: "you"})
db.remove("notes", 1)
show db.all("notes")`
	if got := runSrc(t, src); got != "[{id: 2, text: \"you\"}]\n" {
		t.Errorf("got %q", got)
	}
	st, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("store file should be private (0600), got %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files were left behind: %v", entries)
	}
	// reopening keeps the data and the id counter
	again := runSrc(t, `use store
db = store.open("`+filepath.ToSlash(db)+`")
show db.add("notes", {text: "new"}).id`)
	if again != "3\n" {
		t.Errorf("ids must keep counting after a restart, got %q", again)
	}
}

func TestUseFiles(t *testing.T) {
	files := map[string]string{
		"helpers.lf": "beam twice(x: Int) gives Int {\n    send x * 2\n}\n",
		"loop_a.lf":  "use \"loop_b.lf\"\n",
		"loop_b.lf":  "use \"loop_a.lf\"\n",
	}
	newIn := func(out *bytes.Buffer) *Interp {
		in := NewInterp(out)
		in.ReadSource = func(p string) (string, error) {
			if s, ok := files[p]; ok {
				return s, nil
			}
			return "", errors.New("not found")
		}
		return in
	}
	var out bytes.Buffer
	if err := newIn(&out).Run("use \"helpers.lf\"\nshow helpers.twice(21)"); err != nil || out.String() != "42\n" {
		t.Fatalf("got %q, %v", out.String(), err)
	}
	for src, want := range map[string]string{
		`use "../secret.lf"`:   "must be inside your program's folder",
		`use "/etc/passwd.lf"`: "must be inside your program's folder",
		`use "notes.txt"`:      "use a .lf file",
		`use "loop_a.lf"`:      "in a loop",
	} {
		err := newIn(&bytes.Buffer{}).Run(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: expected %q, got %v", src, want, err)
		}
	}
}

func TestNamesSetInBlocksStay(t *testing.T) {
	got := runSrc(t, `launch {
    when yes {
        found = "kept"
    }
    each n in [1, 2] {
        last = n
    }
    show "{found} {last}"
}`)
	if got != "kept 2\n" {
		t.Errorf("got %q", got)
	}
}

func TestTextInsideBraces(t *testing.T) {
	got := runSrc(t, `items = {fruit: "mango"}
show "I like {items["fruit"].upper()}"
show "{\"json\": true}"`)
	if got != "I like MANGO\n{\"json\": true}\n" {
		t.Errorf("got %q", got)
	}
}
