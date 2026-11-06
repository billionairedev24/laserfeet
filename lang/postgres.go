package lang

import (
	"strconv"
	"strings"

	"laserfeet/pg"
)

// Database is a PostgreSQL database opened with postgres.open. Inside
// transaction with db as tx { ... }, tx is a Database tied to one connection.
type Database struct {
	pool *pg.Pool
	conn *pg.Conn // set inside a transaction
}

func postgresModule() *Module {
	return &Module{Name: "postgres", Members: map[string]Value{
		"open": builtin("open", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "postgres.open", a, 1)
			cfg, err := pg.ParseURL(Text(a[0]))
			if err != nil {
				in.fail(line, "postgres.open: %v", err)
			}
			db := &Database{pool: pg.NewPool(cfg, 10)}
			// connect now, so a wrong address or password shows up straight away
			in.gil.Unlock()
			c, err := db.pool.Get()
			if err == nil {
				db.pool.Put(c)
			}
			in.gil.Lock()
			if err != nil {
				in.fail(line, "postgres.open: %v", err)
			}
			return db
		}),
	}}
}

// toParam turns a Laser Feet value into the text PostgreSQL receives for $1, $2 ...
func toParam(v Value) *string {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s = x
	case bool:
		s = "false"
		if x {
			s = "true"
		}
	case int64:
		s = strconv.FormatInt(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'g', -1, 64)
	case *Decimal:
		s = x.String()
	default: // lists, maps and objects go in as JSON, for json and jsonb columns
		s = toJSON(v)
	}
	return &s
}

// fromColumn turns a value from PostgreSQL into the matching Laser Feet type.
func fromColumn(oid uint32, s *string) Value {
	if s == nil {
		return nil
	}
	v := *s
	switch oid {
	case 16: // boolean
		return v == "t"
	case 20, 21, 23, 26: // int8, int2, int4, oid
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return box(i)
		}
	case 700, 701: // float4, float8
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case 1700: // numeric: exact
		if d, err := ParseDecimal(v); err == nil {
			d.literal = true // a value from outside: it may also be used where a Float is wanted
			return d
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil { // NaN, Infinity
			return f
		}
	case 114, 3802: // json, jsonb
		if j, err := fromJSON(v); err == nil {
			return j
		}
	}
	return v // text, dates, times, uuids and everything else arrive as Text
}

func (in *Interp) dbExec(line int, db *Database, what string, a []Value) *pg.Result {
	if len(a) < 1 || len(a) > 2 {
		in.fail(line, "db.%s needs SQL and, optionally, a list of values for $1, $2 ..., like db.%s(\"select * from todos where id = $1\", [id])", what, what)
	}
	sql := Text(a[0])
	var args []*string
	if len(a) == 2 {
		l, ok := a[1].(*List)
		if !ok {
			in.fail(line, "the values for db.%s must be a list, like [id, name]", what)
		}
		for _, v := range l.Items {
			args = append(args, toParam(v))
		}
	}
	in.gil.Unlock() // let other work run while the database answers
	var res *pg.Result
	var err error
	if db.conn != nil {
		res, err = db.conn.Exec(sql, args)
	} else {
		res, err = db.pool.Exec(sql, args)
	}
	in.gil.Lock()
	if err != nil {
		in.fail(line, "database: %v", err)
	}
	return res
}

func rowsOf(res *pg.Result) *List {
	out := &List{Items: make([]Value, 0, len(res.Rows))}
	for _, row := range res.Rows {
		m := NewMap()
		for i, f := range res.Fields {
			if i < len(row) {
				m.Set(f.Name, fromColumn(f.Type, row[i]))
			}
		}
		out.Items = append(out.Items, m)
	}
	return out
}

func databaseMethod(in *Interp, db *Database, name string) *Builtin {
	switch name {
	case "query":
		return builtin("query", func(in *Interp, line int, a []Value) Value {
			return rowsOf(in.dbExec(line, db, "query", a))
		})
	case "first":
		return builtin("first", func(in *Interp, line int, a []Value) Value {
			rows := rowsOf(in.dbExec(line, db, "first", a))
			if len(rows.Items) == 0 {
				return nil
			}
			return rows.Items[0]
		})
	case "run":
		return builtin("run", func(in *Interp, line int, a []Value) Value {
			return box(in.dbExec(line, db, "run", a).Affected)
		})
	case "transaction":
		return builtin("transaction", func(in *Interp, line int, a []Value) Value {
			if len(a) != 1 {
				in.fail(line, "a transaction needs a block, like: transaction with db as tx { tx.run(...) }")
			}
			if db.conn != nil {
				in.fail(line, "a transaction can't start inside another transaction")
			}
			in.gil.Unlock()
			c, err := db.pool.Get()
			in.gil.Lock()
			if err != nil {
				in.fail(line, "database: %v", err)
			}
			tx := &Database{pool: db.pool, conn: c}
			finish := func(sql string) error {
				in.gil.Unlock()
				_, err := c.Exec(sql, nil)
				in.gil.Lock()
				return err
			}
			defer db.pool.Put(c)
			if err := finish("begin"); err != nil {
				in.fail(line, "database: %v", err)
			}
			committed := false
			defer func() {
				if !committed {
					finish("rollback") // any problem undoes everything the block did
				}
			}()
			out := in.call(a[0], []Value{tx}, nil, line, nil)
			if err := finish("commit"); err != nil {
				in.fail(line, "database: %v", err)
			}
			committed = true
			return out
		})
	}
	return nil
}

// isTransactionCall reports whether a call is db.transaction(), the form behind 'transaction with db as tx'.
func isTransactionCall(callee Expr) bool {
	g, ok := callee.(*Get)
	return ok && strings.EqualFold(g.Name, "transaction")
}
