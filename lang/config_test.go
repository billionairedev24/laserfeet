package lang

import (
	"strings"
	"testing"
)

func configJSON(t *testing.T, kind, src string) string {
	t.Helper()
	var v Value
	var err error
	if kind == "yaml" {
		v, err = parseYAML("t.yaml", src)
	} else {
		v, err = parseProperties("t.properties", src)
	}
	if err != nil {
		t.Fatalf("%s %q: %v", kind, src, err)
	}
	return Text(toJSON(v))
}

func TestYAMLSettings(t *testing.T) {
	t.Setenv("LF_TEST_PORT", "3000")
	cases := []struct{ src, want string }{
		{"a: 1\nb: two\nc: true\nd: ~\ne: 1.5", `{"a":1,"b":"two","c":true,"d":null,"e":1.5}`},
		{"zip: 01234\nanswer: no\nver: 1.10", `{"zip":"01234","answer":"no","ver":1.10}`},
		{"a:\n  b:\n    c: deep", `{"a":{"b":{"c":"deep"}}}`},
		{"l:\n  - 1\n  - two\n", `{"l":[1,"two"]}`},
		{"l:\n- 1\n- 2", `{"l":[1,2]}`},
		{"- - a\n  - b\n- - c", `[["a","b"],["c"]]`},
		{"people:\n  - name: Ada\n    age: 36\n  - name: Bo", `{"people":[{"name":"Ada","age":36},{"name":"Bo"}]}`},
		{"f: [1, 'two', \"three\", {x: y}]", `{"f":[1,"two","three",{"x":"y"}]}`},
		{"f: {a: 1,\n  b: [2,\n  3]}", `{"f":{"a":1,"b":[2,3]}}`},
		{"t: |\n  one\n  two\nn: 1", `{"t":"one\ntwo\n","n":1}`},
		{"t: |-\n  one\n\n  two\n\n", `{"t":"one\n\ntwo"}`},
		{"t: >\n  one\n  two\n\n  three\n", `{"t":"one two\nthree\n"}`},
		{"a: 'it''s' # comment\nb: \"x # not a comment\"", `{"a":"it's","b":"x # not a comment"}`},
		{"url: http://x.example:80/a#b", `{"url":"http://x.example:80/a#b"}`},
		{"\"quoted key\": 1", `{"quoted key":1}`},
		{"long: this carries\n  on over lines", `{"long":"this carries on over lines"}`},
		{"port: ${LF_TEST_PORT}\nhost: ${LF_TEST_NOPE:localhost}\nlit: $${KEEP}", `{"port":3000,"host":"localhost","lit":"${KEEP}"}`},
		{"esc: \"tab\\tdone \\u00e9\"", `{"esc":"tab\tdone é"}`},
		{"---\na: 1\n...\nignored", `{"a":1}`},
		{"", `{}`},
		{"# only a comment\n", `{}`},
	}
	for _, c := range cases {
		if got := configJSON(t, "yaml", c.src); got != c.want {
			t.Errorf("%q\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestYAMLRefusals(t *testing.T) {
	cases := map[string]string{
		"a: &x 1\nb: *x":         "anchors",
		"<<: {a: 1}":             "merge keys",
		"a: !!python/object x":   "tags",
		"a: 1\n---\nb: 2":        "more than one",
		"a:\n\tb: 1":             "tabs",
		"a: 1\na: 2":             "given twice",
		"a: 1\n  b: 2":           "indent",
		"a: \"open":              "isn't closed",
		"a: [1, 2":               "expected , or ]",
		"a: ${LF_TEST_UNSET_X}":  "isn't set",
		"? complex\n: key":       "complex",
		"a: \"bad \\q\"":         "unknown escape",
		strings.Repeat("[", 200): "100 deep",
	}
	for src, want := range cases {
		_, err := parseYAML("t.yaml", "x: "+strings.TrimPrefix(src, "x: "))
		if strings.HasPrefix(src, "a") || strings.HasPrefix(src, "<") || strings.HasPrefix(src, "?") {
			_, err = parseYAML("t.yaml", src)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want a problem about %q", src, err, want)
		}
	}
}

func TestPropertiesSettings(t *testing.T) {
	t.Setenv("LF_TEST_HOST", "db.prod")
	cases := []struct{ src, want string }{
		{"a=1\nb = two\nc:true\nd e", `{"a":1,"b":"two","c":true,"d":"e"}`},
		{"# c\n! c\n\nserver.port=8080\nserver.host=x", `{"server":{"port":8080,"host":"x"}}`},
		{"l[0]=a\nl[1]=b", `{"l":["a","b"]}`},
		{"u[0].name=Ada\nu[1].name=Bo", `{"u":[{"name":"Ada"},{"name":"Bo"}]}`},
		{"long=one \\\n    two", `{"long":"one two"}`},
		{"k\\=ey=v\\tw \\u00e9", `{"k=ey":"v\tw é"}`},
		{"host=${LF_TEST_HOST}\nport=${LF_TEST_NOPE:5432}", `{"host":"db.prod","port":5432}`},
		{"empty=", `{"empty":null}`},
	}
	for _, c := range cases {
		if got := configJSON(t, "properties", c.src); got != c.want {
			t.Errorf("%q\n got %s\nwant %s", c.src, got, c.want)
		}
	}
	for src, want := range map[string]string{
		"a=1\na.b=2":    "both as a value and as a group",
		"=1":            "needs a name",
		"a=\\u12":       "four hex digits",
		"a=${NOPE_LF}":  "isn't set",
		"a[5]=x":        "none missing",
		"a[0]=x\na.b=y": "both as a list",
	} {
		if _, err := parseProperties("t.properties", src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}

func TestConfigMerge(t *testing.T) {
	a, _ := parseYAML("a", "db:\n  host: x\n  port: 1\nname: a")
	b, _ := fromJSON(`{"db": {"host": "y"}, "extra": [1]}`)
	if got, want := Text(toJSON(mergeConfig(a, b))), `{"db":{"host":"y","port":1},"name":"a","extra":[1]}`; got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func FuzzConfig(f *testing.F) {
	for _, s := range []string{"a: 1", "l:\n  - x: [1, {y: 'z'}]\n", "t: |\n  a\n", "a.b[0]=1\n", "- - a\n", "a: \"\\u00e9\""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		parseYAML("f.yaml", src)
		parseProperties("f.properties", src)
	})
}

func TestConfigReadFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	write := func(name, src string) {
		if err := writeFileAtomic(name, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("app.yaml", "name: shop\ndatabase:\n  host: localhost\n  port: 5432\n")
	write("prod.properties", "database.host=db.prod\n")
	write("local.json", `{"database": {"port": 6543}}`)
	write("app.toml", "x = 1")
	var out strings.Builder
	in := NewInterp(&out)
	src := `use config
use json
launch {
    show json.write(config.read("app.yaml", "prod.properties", "local.json"))
    attempt { config.read("app.toml") } fails p { show p }
    attempt { config.read("missing.yaml") } fails p { show "missing" }
}`
	if err := in.Run(src); err != nil {
		t.Fatal(err)
	}
	want := "{\"name\":\"shop\",\"database\":{\"host\":\"db.prod\",\"port\":6543}}\n" +
		"config.read reads .yaml, .yml, .properties and .json files, not app.toml\nmissing\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}
