package lang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- use "file.lf" ----------

// loadFile runs another .lf file once and returns its beams, objects and values
// as a module named after the file (use "helpers.lf" gives helpers.greet()).
func (in *Interp) loadFile(p string, line int) *Module {
	ctx := UseContext{}
	if n := len(in.useStack); n > 0 {
		ctx = in.useStack[n-1]
	}
	clean, name, next, err := ResolveUse(ctx, p)
	if err != nil {
		in.fail(line, "use \"%s\": %v", p, err)
	}
	if m := in.modules[clean]; m != nil {
		return m
	}
	if in.loading[clean] {
		in.fail(line, "use \"%s\": these files use each other in a loop", p)
	}
	if in.ReadSource == nil {
		in.fail(line, "use \"%s\": other files can't be used here (try it with lf run on your computer)", p)
	}
	src, err := in.ReadSource(clean)
	if err != nil {
		if IsPackagePath(p) {
			in.fail(line, "use \"%s\": this package isn't installed. Run: lf get %s", p, p)
		}
		in.fail(line, "use \"%s\": I couldn't read it (%v)", p, err)
	}
	prog, perr := Parse(src)
	if perr != nil {
		if se, ok := perr.(*SyntaxError); ok {
			in.fail(line, "in %s, line %d: %s", clean, se.Line, se.Msg)
		}
		in.fail(line, "in %s: %v", clean, perr)
	}
	if in.loading == nil {
		in.loading, in.modules = map[string]bool{}, map[string]*Module{}
	}
	in.loading[clean] = true
	in.useStack = append(in.useStack, next)
	env := newScope(in.builtins)
	in.files.Store(env, clean)
	in.loadInto(prog, env, false)
	in.useStack = in.useStack[:len(in.useStack)-1]
	delete(in.loading, clean)

	m := &Module{Name: name, Members: map[string]Value{}}
	hidden := privateNames(prog)
	for i, n := range env.names {
		if !hidden[n] {
			m.Members[n] = env.binds[i].v
		}
	}
	for n, b := range env.big {
		m.Members[n] = b.v
	}
	in.modules[clean] = m
	return m
}

// UsedFiles lists what a program uses directly (files and packages), as written.
func UsedFiles(prog *Program) []string {
	var out []string
	for _, st := range prog.Stmts {
		if u, ok := st.(*UseStmt); ok && u.Path != "" {
			out = append(out, u.Path)
		}
	}
	return out
}

// ---------- json ----------

func jsonModule() *Module {
	return &Module{Name: "json", Members: map[string]Value{
		"read": builtin("read", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "json.read", a, 1)
			v, err := fromJSON(Text(a[0]))
			if err != nil {
				in.fail(line, "json.read: this isn't valid JSON (%v)", err)
			}
			return v
		}),
		"write": builtin("write", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "json.write", a, 1)
			return toJSON(a[0])
		}),
		"pretty": builtin("pretty", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "json.pretty", a, 1)
			var out bytes.Buffer
			json.Indent(&out, []byte(toJSON(a[0])), "", "  ")
			return out.String()
		}),
	}}
}

// fromJSON reads JSON into Laser Feet values, keeping the order of object keys.
func fromJSON(s string) (Value, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("extra text after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '[':
			l := newList(nil)
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				l.Items = append(l.Items, v)
			}
			_, err := dec.Token()
			return l, err
		case '{':
			m := NewMap()
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				m.Set(k.(string), v)
			}
			_, err := dec.Token()
			return m, err
		}
	case json.Number:
		// whole numbers become Ints, anything else an exact Decimal
		if i, err := t.Int64(); err == nil {
			return box(i), nil
		}
		if d, err := ParseDecimal(t.String()); err == nil {
			return d, nil
		}
		f, err := t.Float64() // numbers like 1e300 that have no short exact form
		return f, err
	case string, bool, nil:
		return t, nil
	}
	return nil, fmt.Errorf("unexpected %v", tok)
}

// ---------- files ----------

func filesModule() *Module {
	return &Module{Name: "files", Members: map[string]Value{
		"read": builtin("read", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "files.read", a, 1)
			data, err := readFile(Text(a[0]))
			if err != nil {
				in.fail(line, "could not read %s (%v)", Text(a[0]), err)
			}
			return data
		}),
		"write": builtin("write", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "files.write", a, 2)
			if err := writeFileAtomic(Text(a[0]), []byte(Text(a[1])), 0o644); err != nil {
				in.fail(line, "could not write %s (%v)", Text(a[0]), err)
			}
			return nil
		}),
		"exists": builtin("exists", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "files.exists", a, 1)
			return fileExists(Text(a[0]))
		}),
		"list": builtin("list", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "files.list", a, 1)
			names, err := listDir(Text(a[0]))
			if err != nil {
				in.fail(line, "could not list %s (%v)", Text(a[0]), err)
			}
			out := newList(nil)
			for _, n := range names {
				out.Items = append(out.Items, n)
			}
			return out
		}),
	}}
}

// ---------- time ----------

func timeModule() *Module {
	return &Module{Name: "time", Members: map[string]Value{
		"now": builtin("now", func(in *Interp, line int, a []Value) Value {
			return Moment{time.Now().UTC()}
		}),
		"today": builtin("today", func(in *Interp, line int, a []Value) Value {
			y, m, d := time.Now().Date() // the date where the program runs
			return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
		}),
		"at": builtin("at", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "time.at", a, 1)
			if m, ok := a[0].(Moment); ok {
				return m
			}
			m, err := parseMoment(Text(a[0]))
			if err != nil {
				in.fail(line, "%v", err)
			}
			return m
		}),
		"seconds": builtin("seconds", func(in *Interp, line int, a []Value) Value {
			return float64(time.Now().UnixMilli()) / 1000
		}),
	}}
}

// ---------- store: a small built-in database saved to a JSON file ----------

type Store struct {
	mu    sync.Mutex // fast each items and web requests may use a store at once
	path  string     // empty means kept in memory only
	data  map[string]*List
	next  map[string]int64
	names []string
}

type storeFile struct {
	Collections map[string]any   `json:"collections"`
	Next        map[string]int64 `json:"next"`
}

func storeModule() *Module {
	return &Module{Name: "store", Members: map[string]Value{
		"open": builtin("open", func(in *Interp, line int, a []Value) Value {
			if len(a) > 1 {
				in.fail(line, "store.open takes a file name, like store.open(\"data.json\"), or nothing to keep data in memory")
			}
			st := &Store{data: map[string]*List{}, next: map[string]int64{}}
			if len(a) == 1 {
				st.path = Text(a[0])
				if err := st.load(); err != nil {
					in.fail(line, "store.open: %v", err)
				}
			}
			return st
		}),
		"memory": builtin("memory", func(in *Interp, line int, a []Value) Value {
			return &Store{data: map[string]*List{}, next: map[string]int64{}}
		}),
	}}
}

func (st *Store) load() error {
	if !fileExists(st.path) {
		return nil
	}
	text, err := readFile(st.path)
	if err != nil {
		return fmt.Errorf("could not read %s (%v)", st.path, err)
	}
	var f storeFile
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return fmt.Errorf("%s is not a Laser Feet store file (%v)", st.path, err)
	}
	for name, raw := range f.Collections {
		b, _ := json.Marshal(raw)
		v, err := fromJSON(string(b))
		l, ok := v.(*List)
		if err != nil || !ok {
			return fmt.Errorf("%s: collection %s is damaged", st.path, name)
		}
		st.data[name] = l
		st.names = append(st.names, name)
	}
	sort.Strings(st.names)
	for k, v := range f.Next {
		st.next[k] = v
	}
	return nil
}

func (st *Store) save(in *Interp, line int) {
	if st.path == "" || !filesAvailable {
		return // kept in memory only (also in the browser playground)
	}
	f := storeFile{Collections: map[string]any{}, Next: st.next}
	for name, l := range st.data {
		f.Collections[name] = toPlain(l)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(f)
	// Written to a temporary file first and then renamed, so a crash never leaves
	// half a file behind. Only the owner can read it.
	if err := writeFileAtomic(st.path, buf.Bytes(), 0o600); err != nil {
		in.fail(line, "could not save %s (%v)", st.path, err)
	}
}

func (st *Store) collection(name string) *List {
	l := st.data[name]
	if l == nil {
		l = newList(nil)
		st.data[name] = l
		st.names = append(st.names, name)
	}
	return l
}

func copyRecord(in *Interp, line int, v Value) *Map {
	m := NewMap()
	switch x := v.(type) {
	case *Map:
		for _, k := range x.Keys {
			m.Set(k, x.M[k])
		}
	case *Instance:
		for i, f := range x.Class.Fields {
			m.Set(f.Name, x.Slots[i])
		}
	default:
		in.fail(line, "a store can only save maps and objects, not %s", typeName(v))
	}
	return m
}

func (st *Store) indexOf(l *List, id Value) int {
	for i, it := range l.Items {
		if m, ok := it.(*Map); ok && equal(m.M["id"], id) {
			return i
		}
	}
	return -1
}

func storeMethod(in *Interp, st *Store, name string) *Builtin {
	b := storeMethodUnlocked(in, st, name)
	if b == nil {
		return nil
	}
	fn := b.Fn
	b.Fn = func(in *Interp, line int, a []Value) Value {
		st.mu.Lock()
		defer st.mu.Unlock()
		return fn(in, line, a)
	}
	return b
}

func storeMethodUnlocked(in *Interp, st *Store, name string) *Builtin {
	need := func(a []Value, n int, usage string) {
		if len(a) != n {
			in.fail(0, "%s", usage)
		}
	}
	switch name {
	case "add":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 2, "store.add needs a collection name and a record, like db.add(\"todos\", {title: \"Milk\"})")
			l := st.collection(Text(a[0]))
			rec := copyRecord(in, line, a[1])
			st.next[Text(a[0])]++
			id := box(st.next[Text(a[0])])
			out := NewMap()
			out.Set("id", id)
			for _, k := range rec.Keys {
				if k != "id" {
					out.Set(k, rec.M[k])
				}
			}
			l.Items = append(l.Items, out)
			st.save(in, line)
			return copyRecord(in, line, out)
		})
	case "all":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 1, "store.all needs a collection name, like db.all(\"todos\")")
			out := newList(nil)
			for _, it := range st.collection(Text(a[0])).Items {
				out.Items = append(out.Items, copyRecord(in, line, it))
			}
			return out
		})
	case "find":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 2, "store.find needs a collection name and an id, like db.find(\"todos\", 3)")
			l := st.collection(Text(a[0]))
			if i := st.indexOf(l, a[1]); i >= 0 {
				return copyRecord(in, line, l.Items[i])
			}
			return nil
		})
	case "update":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 3, "store.update needs a collection, an id and the changes, like db.update(\"todos\", 3, {done: yes})")
			l := st.collection(Text(a[0]))
			i := st.indexOf(l, a[1])
			if i < 0 {
				return nil
			}
			rec := l.Items[i].(*Map)
			changes := copyRecord(in, line, a[2])
			for _, k := range changes.Keys {
				if k != "id" {
					rec.Set(k, changes.M[k])
				}
			}
			st.save(in, line)
			return copyRecord(in, line, rec)
		})
	case "remove":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 2, "store.remove needs a collection name and an id, like db.remove(\"todos\", 3)")
			l := st.collection(Text(a[0]))
			i := st.indexOf(l, a[1])
			if i < 0 {
				return false
			}
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
			st.save(in, line)
			return true
		})
	case "count":
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			need(a, 1, "store.count needs a collection name")
			return box(int64(len(st.collection(Text(a[0])).Items)))
		})
	}
	return nil
}

// ---------- env ----------

func envBuiltin() *Builtin {
	return builtin("env", func(in *Interp, line int, a []Value) Value {
		if len(a) < 1 || len(a) > 2 {
			in.fail(line, "env takes a name and an optional default, like env(\"PORT\", \"8080\")")
		}
		if v, ok := getenv(Text(a[0])); ok {
			return v
		}
		if len(a) == 2 {
			return a[1]
		}
		return nil
	})
}
