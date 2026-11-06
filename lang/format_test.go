package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatLaysOutCode(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"indent and spaces",
			"beam add(a:Int,b:Int) gives Int{\nsend a+b\n}\n",
			"beam add(a: Int, b: Int) gives Int {\n    send a + b\n}\n"},
		{"blank lines",
			"\n\nlaunch {\n\n    show 1\n\n\n\n    show 2\n\n}\n\n\n",
			"launch {\n    show 1\n\n    show 2\n}\n"},
		{"comments stay where they are",
			"-- top\nlaunch {\n-- inside\n  show 1   -- one\n}\n",
			"-- top\nlaunch {\n    -- inside\n    show 1   -- one\n}\n"},
		{"negative numbers and calls",
			"x = - 5\ny = f( x , -2 )\nz = x - 2\nshow [ 1 , 2 ][0]\n",
			"x = -5\ny = f(x, -2)\nz = x - 2\nshow [1, 2][0]\n"},
		{"types",
			"beam find(id:Int) gives Item ?{\nitems:List[Item]=[]\nsend nothing\n}\n",
			"beam find(id: Int) gives Item? {\n    items: List[Item] = []\n    send nothing\n}\n"},
		{"continued calls indent once",
			"launch {\ndb.run(\"insert\",\n[1, 2])\n}\n",
			"launch {\n    db.run(\"insert\",\n        [1, 2])\n}\n"},
		{"maps and otherwise",
			"when x {\nsend {ok: yes}\n}otherwise{\nsend { ok: no }\n}\n",
			"when x {\n    send {ok: yes}\n} otherwise {\n    send { ok: no }\n}\n"},
		{"text is untouched",
			"show \"a  {b}   c\"\nsql = \"\"\"\n   select  *\n\"\"\"\n",
			"show \"a  {b}   c\"\nsql = \"\"\"\n   select  *\n\"\"\"\n"},
	}
	for _, c := range cases {
		got, err := Format(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
		if again, _ := Format(got); again != got {
			t.Errorf("%s: formatting twice changed it again: %q", c.name, again)
		}
	}
}

// Every example and package must already be laid out the standard way, and
// formatting must never change what a program does.
func TestExamplesAreFormatted(t *testing.T) {
	var files []string
	for _, root := range []string{"../examples", "../packages", "../bench"} {
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && strings.HasSuffix(p, ".lf") && !strings.Contains(p, "mistakes") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) < 10 {
		t.Fatalf("found only %d files", len(files))
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		got, err := Format(string(src))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if got != string(src) {
			t.Errorf("%s is not formatted; run: lf fmt %s", f, f)
		}
	}
}

func TestFixUpdatesOldCode(t *testing.T) {
	old := "use web\nuse postgres\ndb = postgres.open(\"postgres://x@localhost/db\")\nserver = web.open(8080)\n" +
		"server.get(\"/u/:id\") {\n    send request.params.id\n}\n" +
		"server.get(\"/hi\") {\n    send \"hi\"\n}\n" +
		"beam move() {\n    db.transaction() {\n        tx.run(\"select 1\")\n    }\n}\n" +
		"show \"n: \" + [1, 2] then count\n"
	edits, err := FixEdits(old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 4 { // as request, as tx, and two brackets
		t.Fatalf("expected 4 edits, got %v", edits)
	}
}
