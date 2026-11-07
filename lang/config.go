package lang

// The config library: settings files in YAML, .properties or JSON, read into
// the same values JSON gives (maps, lists, Text, Int, Decimal, Bool,
// nothing), so Settings.from(...) turns them into a typed object with every
// field checked.
//
// ${NAME} in a value is replaced by the setting NAME from the environment (or
// a .env file), and ${NAME:default} gives a default; $${ writes a plain ${.
//
// The YAML read here is the part settings files use: mappings, lists, plain
// and quoted text, | and > blocks, [flow] and {flow} collections, comments.
// Anchors, aliases, tags and several documents in one file are refused with
// a clear message (aliases can make a small file expand without end).
// Numbers with a leading zero (a zip code like 01234) stay Text, and only
// true and false are Bools (so "no" stays the word no).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const maxConfigBytes = 10 << 20

func configModule() *Module {
	return &Module{Name: "config", Members: map[string]Value{
		"read": builtin("read", func(in *Interp, line int, a []Value) Value {
			if len(a) == 0 {
				in.fail(line, "config.read needs a file name, like config.read(\"app.yaml\")")
			}
			var merged Value
			for _, p := range a {
				path := Text(p)
				src, err := readFile(path)
				if err != nil {
					in.fail(line, "config.read: couldn't read %s (%v)", path, err)
				}
				v, err := parseConfig(path, src)
				if err != nil {
					in.fail(line, "%v", err)
				}
				merged = mergeConfig(merged, v)
			}
			return merged
		}),
		"yaml": builtin("yaml", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "config.yaml", a, 1)
			v, err := parseYAML("the YAML", Text(a[0]))
			if err != nil {
				in.fail(line, "%v", err)
			}
			return v
		}),
		"properties": builtin("properties", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "config.properties", a, 1)
			v, err := parseProperties("the properties", Text(a[0]))
			if err != nil {
				in.fail(line, "%v", err)
			}
			return v
		}),
	}}
}

// parseConfig reads a settings file by its kind: .yaml/.yml, .properties,
// .json.
func parseConfig(path, src string) (Value, error) {
	if len(src) > maxConfigBytes {
		return nil, fmt.Errorf("%s is bigger than the 10 MB a settings file may be", path)
	}
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return parseYAML(name, src)
	case ".properties":
		return parseProperties(name, src)
	case ".json":
		v, err := fromJSON(src)
		if err != nil {
			return nil, fmt.Errorf("%s isn't valid JSON (%v)", name, err)
		}
		return expandValue(name, 0, v)
	}
	return nil, fmt.Errorf("config.read reads .yaml, .yml, .properties and .json files, not %s", name)
}

// mergeConfig lays b over a: maps merge key by key (deeply), anything else
// in b replaces a. Later files override earlier ones.
func mergeConfig(a, b Value) Value {
	am, ok1 := a.(*Map)
	bm, ok2 := b.(*Map)
	if !ok1 || !ok2 {
		return b
	}
	out := NewMap()
	for _, k := range am.Keys {
		out.Set(k, am.M[k])
	}
	for _, k := range bm.Keys {
		if old, ok := out.M[k]; ok {
			out.Set(k, mergeConfig(old, bm.M[k]))
		} else {
			out.Set(k, bm.M[k])
		}
	}
	return out
}

// ---------- settings from the environment ----------

var settingRef = regexp.MustCompile(`\$\$\{|\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}`)

// expand replaces ${NAME} and ${NAME:default}.
func expand(file string, line int, s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var problem error
	out := settingRef.ReplaceAllStringFunc(s, func(m string) string {
		if m == "$${" {
			return "${"
		}
		parts := settingRef.FindStringSubmatch(m)
		if v, ok := getenv(parts[1]); ok {
			return v
		}
		if strings.Contains(m, ":") {
			return parts[2]
		}
		if problem == nil {
			problem = fmt.Errorf("%s, line %d: the setting %s isn't set (give a default like ${%s:value})", file, line, parts[1], parts[1])
		}
		return ""
	})
	return out, problem
}

// expandValue expands settings in every text inside a value (for JSON).
func expandValue(file string, line int, v Value) (Value, error) {
	switch x := v.(type) {
	case string:
		return expand(file, line, x)
	case *List:
		for i, it := range x.Items {
			e, err := expandValue(file, line, it)
			if err != nil {
				return nil, err
			}
			x.Items[i] = e
		}
	case *Map:
		for _, k := range x.Keys {
			e, err := expandValue(file, line, x.M[k])
			if err != nil {
				return nil, err
			}
			x.M[k] = e
		}
	}
	return v, nil
}

// plainValue gives a plain (unquoted) word its type.
func plainValue(s string) Value {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	digits := strings.TrimLeft(s, "+-")
	if digits == "" || len(digits) > 1 && digits[0] == '0' && digits[1] != '.' {
		return s // 01234 is a code, not a number
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return box(i)
	}
	if strings.ContainsAny(s, "0123456789") && !strings.ContainsAny(s, "eE_xX") {
		if d, err := ParseDecimal(strings.TrimPrefix(s, "+")); err == nil {
			return d
		}
	}
	return s
}

// ---------- .properties ----------

// parseProperties reads Java-style properties. Dotted names make nested
// maps (server.port=8080 is {server: {port: 8080}}), and name[0], name[1]
// make a list.
func parseProperties(file, src string) (Value, error) {
	root := NewMap()
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		start := i + 1
		l := strings.TrimLeft(lines[i], " \t\f")
		if l == "" || l[0] == '#' || l[0] == '!' {
			continue
		}
		// a line ending in an odd number of \ carries on
		for endsInEscape(l) && i+1 < len(lines) {
			i++
			l = l[:len(l)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		key, value := splitProperty(l)
		key, err := unescapeProperty(key)
		if err != nil {
			return nil, fmt.Errorf("%s, line %d: %v", file, start, err)
		}
		raw, err := unescapeProperty(value)
		if err != nil {
			return nil, fmt.Errorf("%s, line %d: %v", file, start, err)
		}
		raw, err = expand(file, start, raw)
		if err != nil {
			return nil, err
		}
		if err := setPath(root, key, plainValue(strings.TrimSpace(raw))); err != nil {
			return nil, fmt.Errorf("%s, line %d: %v", file, start, err)
		}
	}
	v, err := lists(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	return v, nil
}

func endsInEscape(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// splitProperty splits at the first unescaped =, : or space.
func splitProperty(l string) (string, string) {
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '\\':
			i++
		case '=', ':':
			return strings.TrimSpace(l[:i]), strings.TrimLeft(l[i+1:], " \t\f")
		case ' ', '\t', '\f':
			rest := strings.TrimLeft(l[i:], " \t\f")
			if rest != "" && (rest[0] == '=' || rest[0] == ':') {
				rest = strings.TrimLeft(rest[1:], " \t\f")
			}
			return l[:i], rest
		}
	}
	return l, ""
}

func unescapeProperty(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+5 > len(s) {
				return "", fmt.Errorf("\\u needs four hex digits")
			}
			n, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
			if err != nil {
				return "", fmt.Errorf("\\u needs four hex digits")
			}
			b.WriteRune(rune(n))
			i += 4
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

// setPath sets a.b.c (and a[0]) in nested maps; list indexes are kept as
// "#n" keys until lists turns them into lists.
func setPath(root *Map, key string, v Value) error {
	if key == "" {
		return fmt.Errorf("a setting needs a name before its =")
	}
	parts := orderParts(key)
	cur := root
	for i, p := range parts {
		old, exists := cur.M[p]
		if i == len(parts)-1 {
			if _, isMap := old.(*Map); exists && isMap {
				return fmt.Errorf("%s is set both as a value and as a group of settings", key)
			}
			cur.Set(p, v)
			return nil
		}
		next, isMap := old.(*Map)
		if exists && !isMap {
			return fmt.Errorf("%s is set both as a value and as a group of settings", strings.Join(parts[:i+1], "."))
		}
		if !exists {
			next = NewMap()
			cur.Set(p, next)
		}
		cur = next
	}
	return nil
}

// orderParts splits a.b[0].c into a, b, #0, c.
func orderParts(key string) []string {
	var out []string
	for _, p := range strings.Split(key, ".") {
		name, rest, _ := strings.Cut(p, "[")
		if name != "" {
			out = append(out, name)
		}
		if rest != "" {
			for _, ix := range strings.Split("["+rest, "[") {
				if ix = strings.TrimSuffix(ix, "]"); ix != "" {
					if n, err := strconv.Atoi(ix); err == nil && strings.Trim(ix, "0123456789") == "" {
						out = append(out, "#"+strconv.Itoa(n))
					} else {
						out = append(out, ix)
					}
				}
			}
		}
	}
	return out
}

// lists turns maps whose keys are all #0, #1, ... into lists, in order. The
// indexes must run 0, 1, 2 with none missing, so a[999999999]=x can't ask
// for a list of a billion items.
func lists(v Value) (Value, error) {
	m, ok := v.(*Map)
	if !ok {
		return v, nil
	}
	for _, k := range m.Keys {
		e, err := lists(m.M[k])
		if err != nil {
			return nil, err
		}
		m.M[k] = e
	}
	if len(m.Keys) == 0 || !strings.HasPrefix(m.Keys[0], "#") {
		for _, k := range m.Keys {
			if strings.HasPrefix(k, "#") {
				return nil, fmt.Errorf("a setting is used both as a list ([%s]) and as a group of named settings", k[1:])
			}
		}
		return m, nil
	}
	l := newList(make([]Value, len(m.Keys)))
	for _, k := range m.Keys {
		n, err := strconv.Atoi(k[1:])
		if !strings.HasPrefix(k, "#") || err != nil {
			return nil, fmt.Errorf("a setting is used both as a list and as a group of named settings (%s)", k)
		}
		if n < 0 || n >= len(m.Keys) {
			return nil, fmt.Errorf("list numbers run 0, 1, 2 with none missing, but [%d] comes with only %d items", n, len(m.Keys))
		}
		l.Items[n] = m.M[k]
	}
	return l, nil
}

// ---------- YAML ----------

type yline struct {
	n      int    // line number
	indent int    // spaces before the text
	text   string // without the indent and any comment
	raw    string // the whole line, for | and > blocks
}

type yamlParser struct {
	file  string
	lines []yline
	pos   int
	depth int
}

func (p *yamlParser) errorf(line int, format string, args ...any) error {
	return fmt.Errorf("%s, line %d: %s", p.file, line, fmt.Sprintf(format, args...))
}

// stripComment takes off a # comment that isn't inside quotes.
func stripComment(s string) string {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			if !(inD && i > 0 && s[i-1] == '\\') {
				inD = !inD
			}
		case c == '#' && !inS && !inD && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return strings.TrimRight(s, " \t")
}

func parseYAML(file, src string) (Value, error) {
	if len(src) > maxConfigBytes {
		return nil, fmt.Errorf("%s is bigger than the 10 MB a settings file may be", file)
	}
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), "\ufeff")
	p := &yamlParser{file: file}
	docs := 0
	for i, raw := range strings.Split(src, "\n") {
		n := i + 1
		body := strings.TrimLeft(raw, " ")
		if strings.HasPrefix(body, "\t") {
			return nil, p.errorf(n, "YAML is indented with spaces, not tabs")
		}
		text := stripComment(body)
		if text == "" {
			p.lines = append(p.lines, yline{n: n, indent: -1, raw: raw})
			continue
		}
		indent := len(raw) - len(body)
		if indent == 0 && (text == "---" || strings.HasPrefix(text, "--- ")) {
			docs++
			if docs > 1 || len(p.contentLines()) > 0 {
				return nil, p.errorf(n, "this file holds more than one YAML document; a settings file has one")
			}
			continue
		}
		if indent == 0 && text == "..." {
			break
		}
		if strings.HasPrefix(text, "%") && indent == 0 {
			return nil, p.errorf(n, "YAML directives (%%) aren't supported")
		}
		p.lines = append(p.lines, yline{n: n, indent: indent, text: text, raw: raw})
	}
	if len(p.contentLines()) == 0 {
		return NewMap(), nil
	}
	p.skipBlank()
	v, err := p.block(p.lines[p.pos].indent)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if p.pos < len(p.lines) {
		l := p.lines[p.pos]
		return nil, p.errorf(l.n, "this line's indent doesn't match the lines above it")
	}
	return v, nil
}

func (p *yamlParser) contentLines() []yline {
	var out []yline
	for _, l := range p.lines {
		if l.indent >= 0 {
			out = append(out, l)
		}
	}
	return out
}

func (p *yamlParser) skipBlank() {
	for p.pos < len(p.lines) && p.lines[p.pos].indent < 0 {
		p.pos++
	}
}

// block reads a mapping or a list whose lines start at indent.
func (p *yamlParser) block(indent int) (Value, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 100 {
		return nil, p.errorf(p.lines[p.pos].n, "settings nested more than 100 deep")
	}
	p.skipBlank()
	first := p.lines[p.pos]
	if first.text == "-" || strings.HasPrefix(first.text, "- ") {
		return p.list(indent)
	}
	return p.mapping(indent)
}

func (p *yamlParser) list(indent int) (Value, error) {
	out := newList(nil)
	for {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			break
		}
		l := p.lines[p.pos]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, p.errorf(l.n, "this line's indent doesn't match the list above it")
		}
		if !(l.text == "-" || strings.HasPrefix(l.text, "- ")) {
			return nil, p.errorf(l.n, "a list item starts with \"- \"")
		}
		rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		if rest == "" { // the item is the block below
			p.pos++
			p.skipBlank()
			if p.pos >= len(p.lines) || p.lines[p.pos].indent <= indent {
				out.Items = append(out.Items, nil)
				continue
			}
			v, err := p.block(p.lines[p.pos].indent)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, v)
			continue
		}
		// "- key: value" starts a mapping whose keys line up after "- ",
		// and "- - x" a list inside the list
		inner := l.indent + (len(l.text) - len(rest))
		if rest == "-" || strings.HasPrefix(rest, "- ") {
			p.lines[p.pos] = yline{n: l.n, indent: inner, text: rest, raw: l.raw}
			v, err := p.block(inner)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, v)
			continue
		}
		if _, _, isKey := splitKey(rest); isKey && !startsFlowOrQuote(rest) {
			p.lines[p.pos] = yline{n: l.n, indent: inner, text: rest, raw: l.raw}
			v, err := p.mapping(inner)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, v)
			continue
		}
		p.pos++
		v, err := p.inlineValue(l.n, rest, inner)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

func startsFlowOrQuote(s string) bool {
	return s != "" && strings.ContainsRune(`[{"'`, rune(s[0]))
}

// splitKey splits "key: value" (or "key:") at the colon that ends the key.
func splitKey(s string) (string, string, bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' || s[0] == '\'' { // a quoted key
		end := closingQuote(s)
		if end < 0 || end+1 >= len(s) || s[end+1] != ':' || end+2 < len(s) && s[end+2] != ' ' {
			return "", "", false
		}
		k, err := unquote(s[:end+1])
		if err != nil {
			return "", "", false
		}
		return k, strings.TrimLeft(s[end+2:], " "), true
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ') {
			return strings.TrimRight(s[:i], " "), strings.TrimLeft(s[i+1:], " "), true
		}
	}
	return "", "", false
}

func closingQuote(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		if q == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			return i
		}
	}
	return -1
}

func (p *yamlParser) mapping(indent int) (Value, error) {
	out := NewMap()
	for {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			break
		}
		l := p.lines[p.pos]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, p.errorf(l.n, "this line's indent doesn't match the settings above it")
		}
		if strings.HasPrefix(l.text, "- ") || l.text == "-" {
			return nil, p.errorf(l.n, "a list item here needs a name above it, at less indent")
		}
		if strings.HasPrefix(l.text, "? ") {
			return nil, p.errorf(l.n, "complex YAML keys (?) aren't supported")
		}
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, p.errorf(l.n, "expected \"name: value\"")
		}
		if strings.HasPrefix(key, "&") || strings.HasPrefix(key, "*") || strings.HasPrefix(key, "<<") {
			return nil, p.errorf(l.n, "YAML anchors, aliases and merge keys aren't supported: write the settings out")
		}
		if _, dup := out.M[key]; dup {
			return nil, p.errorf(l.n, "%q is given twice", key)
		}
		p.pos++
		if rest == "" { // the value is the block below, or nothing
			p.skipBlank()
			if p.pos < len(p.lines) && (p.lines[p.pos].indent > indent ||
				p.lines[p.pos].indent == indent && (strings.HasPrefix(p.lines[p.pos].text, "- ") || p.lines[p.pos].text == "-")) {
				v, err := p.block(p.lines[p.pos].indent)
				if err != nil {
					return nil, err
				}
				out.Set(key, v)
			} else {
				out.Set(key, nil)
			}
			continue
		}
		v, err := p.inlineValue(l.n, rest, indent)
		if err != nil {
			return nil, err
		}
		out.Set(key, v)
	}
	return out, nil
}

// inlineValue reads the value after "key: " or "- ": a block scalar, a flow
// collection, quoted or plain text. A plain value may carry on over more
// indented lines.
func (p *yamlParser) inlineValue(line int, s string, indent int) (Value, error) {
	switch {
	case strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*"):
		return nil, p.errorf(line, "YAML anchors and aliases aren't supported: write the settings out")
	case strings.HasPrefix(s, "!"):
		return nil, p.errorf(line, "YAML tags (!) aren't supported")
	case s[0] == '|' || s[0] == '>':
		return p.blockScalar(line, s, indent)
	case s[0] == '[' || s[0] == '{':
		// a flow collection may span lines until its brackets close
		text := s
		for !flowClosed(text) && p.pos < len(p.lines) {
			text += " " + strings.TrimSpace(p.lines[p.pos].text)
			p.pos++
		}
		f := &flowParser{p: p, line: line, s: text}
		v, err := f.value()
		if err != nil {
			return nil, err
		}
		f.space()
		if f.i != len(f.s) {
			return nil, p.errorf(line, "unexpected %q after the closing bracket", f.s[f.i:])
		}
		return v, nil
	case s[0] == '"' || s[0] == '\'':
		end := closingQuote(s)
		if end < 0 {
			return nil, p.errorf(line, "this quoted text isn't closed")
		}
		if strings.TrimSpace(s[end+1:]) != "" {
			return nil, p.errorf(line, "unexpected %q after the quoted text", s[end+1:])
		}
		t, err := unquote(s)
		if err != nil {
			return nil, p.errorf(line, "%v", err)
		}
		if s[0] == '"' {
			return expand(p.file, line, t)
		}
		return t, nil
	}
	// plain text, maybe folded over more-indented lines
	text := s
	for p.pos < len(p.lines) {
		next := p.lines[p.pos]
		if next.indent < 0 {
			break
		}
		if next.indent <= indent {
			break
		}
		if _, _, isKey := splitKey(next.text); isKey {
			break
		}
		text += " " + next.text
		p.pos++
	}
	t, err := expand(p.file, line, text)
	if err != nil {
		return nil, err
	}
	return plainValue(t), nil
}

func flowClosed(s string) bool {
	depth, inS, inD := 0, false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS && (i == 0 || s[i-1] != '\\'):
			inD = !inD
		case inS || inD:
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		}
	}
	return depth <= 0
}

// blockScalar reads | (lines kept) and > (lines folded) text.
func (p *yamlParser) blockScalar(line int, head string, indent int) (Value, error) {
	style, chomp := head[0], byte(0)
	for _, c := range []byte(strings.TrimSpace(head[1:])) {
		switch {
		case c == '-' || c == '+':
			chomp = c
		case c >= '1' && c <= '9':
		default:
			return nil, p.errorf(line, "after %c only - or + may follow", style)
		}
	}
	var body []string
	blockIndent := -1
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		raw := l.raw
		trimmed := strings.TrimLeft(raw, " ")
		if trimmed == "" {
			body = append(body, "")
			p.pos++
			continue
		}
		ind := len(raw) - len(trimmed)
		if ind <= indent {
			break
		}
		if blockIndent < 0 {
			blockIndent = ind
		}
		if ind < blockIndent {
			break
		}
		body = append(body, raw[blockIndent:])
		p.pos++
	}
	// trailing blank lines belong to the chomping rule, not the text
	end := len(body)
	for end > 0 && body[end-1] == "" {
		end--
	}
	trailing := len(body) - end
	lines := body[:end]
	var text string
	if style == '|' {
		text = strings.Join(lines, "\n")
	} else {
		var b strings.Builder
		for i, l := range lines {
			if i > 0 {
				switch {
				case l == "":
					b.WriteString("\n")
				case lines[i-1] != "":
					b.WriteString(" ")
				}
			}
			b.WriteString(l)
		}
		text = b.String()
	}
	switch chomp {
	case '-':
	case '+':
		text += "\n" + strings.Repeat("\n", trailing)
	default:
		if len(lines) > 0 {
			text += "\n"
		}
	}
	return expand(p.file, line, text)
}

func unquote(s string) (string, error) {
	if s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	var b strings.Builder
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("a \\ at the end of quoted text")
		}
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case '"', '\\', '/', ' ':
			b.WriteByte(body[i])
		case 'u', 'x', 'U':
			size := map[byte]int{'x': 2, 'u': 4, 'U': 8}[body[i]]
			if i+1+size > len(body) {
				return "", fmt.Errorf("\\%c needs %d hex digits", body[i], size)
			}
			n, err := strconv.ParseUint(body[i+1:i+1+size], 16, 32)
			if err != nil {
				return "", fmt.Errorf("\\%c needs %d hex digits", body[i], size)
			}
			b.WriteRune(rune(n))
			i += size
		default:
			return "", fmt.Errorf("unknown escape \\%c in quoted text", body[i])
		}
	}
	return b.String(), nil
}

// flowParser reads [a, b] and {k: v} on one (joined) line.
type flowParser struct {
	p    *yamlParser
	line int
	s    string
	i    int
	deep int
}

func (f *flowParser) space() {
	for f.i < len(f.s) && f.s[f.i] == ' ' {
		f.i++
	}
}

func (f *flowParser) value() (Value, error) {
	f.space()
	if f.i >= len(f.s) {
		return nil, f.p.errorf(f.line, "a value is missing before the end of the line")
	}
	f.deep++
	defer func() { f.deep-- }()
	if f.deep > 100 {
		return nil, f.p.errorf(f.line, "settings nested more than 100 deep")
	}
	switch f.s[f.i] {
	case '[':
		f.i++
		out := newList(nil)
		for {
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ']' {
				f.i++
				return out, nil
			}
			v, err := f.value()
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, v)
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ',' {
				f.i++
				continue
			}
			if f.i < len(f.s) && f.s[f.i] == ']' {
				f.i++
				return out, nil
			}
			return nil, f.p.errorf(f.line, "expected , or ] in the list")
		}
	case '{':
		f.i++
		out := NewMap()
		for {
			f.space()
			if f.i < len(f.s) && f.s[f.i] == '}' {
				f.i++
				return out, nil
			}
			k, err := f.scalar(true)
			if err != nil {
				return nil, err
			}
			key := Text(k)
			f.space()
			if f.i >= len(f.s) || f.s[f.i] != ':' {
				return nil, f.p.errorf(f.line, "expected : after %q", key)
			}
			f.i++
			v, err := f.value()
			if err != nil {
				return nil, err
			}
			if _, dup := out.M[key]; dup {
				return nil, f.p.errorf(f.line, "%q is given twice", key)
			}
			out.Set(key, v)
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ',' {
				f.i++
				continue
			}
			if f.i < len(f.s) && f.s[f.i] == '}' {
				f.i++
				return out, nil
			}
			return nil, f.p.errorf(f.line, "expected , or } in the map")
		}
	case '&', '*':
		return nil, f.p.errorf(f.line, "YAML anchors and aliases aren't supported: write the settings out")
	}
	return f.scalar(false)
}

// scalar reads quoted text, or plain text up to , ] } (or : for a key).
func (f *flowParser) scalar(key bool) (Value, error) {
	f.space()
	if f.i < len(f.s) && (f.s[f.i] == '"' || f.s[f.i] == '\'') {
		end := closingQuote(f.s[f.i:])
		if end < 0 {
			return nil, f.p.errorf(f.line, "this quoted text isn't closed")
		}
		t, err := unquote(f.s[f.i : f.i+end+1])
		if err != nil {
			return nil, f.p.errorf(f.line, "%v", err)
		}
		quoted := f.s[f.i] == '"'
		f.i += end + 1
		if quoted && !key {
			return expand(f.p.file, f.line, t)
		}
		return t, nil
	}
	start := f.i
	for f.i < len(f.s) {
		c := f.s[f.i]
		if c == ',' || c == ']' || c == '}' || key && c == ':' && (f.i+1 == len(f.s) || f.s[f.i+1] == ' ') {
			break
		}
		if !key && c == ':' && f.i+1 < len(f.s) && f.s[f.i+1] == ' ' {
			break
		}
		f.i++
	}
	t := strings.TrimSpace(f.s[start:f.i])
	if key {
		return t, nil
	}
	t, err := expand(f.p.file, f.line, t)
	if err != nil {
		return nil, err
	}
	return plainValue(t), nil
}
