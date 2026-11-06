package lang

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"laserfeet/pg"
)

// Migrations change a database's tables step by step. Each .sql file in a
// folder is one step; they run in name order (001_products.sql,
// 002_add_stock.sql), each once, inside its own transaction. A table called
// lf_migrations records which ran, with a checksum: changing a file that has
// already run is refused, because the database wouldn't match it.

const migrationsTable = `create table if not exists %s (
    name       text primary key,
    checksum   text not null,
    applied_at timestamptz not null default now()
)`

// migrationLock keeps two copies of a program from migrating at the same time.
const migrationLock = "7302619841"

// Migration is one step: its file name and SQL.
type Migration struct {
	Name, SQL string
}

// Migrate applies the steps that haven't run yet and returns their names.
func Migrate(pool *pg.Pool, steps []Migration) ([]string, error) {
	return migrateWith(pool, "lf_migrations", steps)
}

func migrateWith(pool *pg.Pool, table string, steps []Migration) ([]string, error) {
	if _, err := pool.Exec(fmt.Sprintf(migrationsTable, table), nil); err != nil {
		return nil, err
	}
	res, err := pool.Exec("select name, checksum from "+table, nil)
	if err != nil {
		return nil, err
	}
	done := map[string]string{}
	for _, r := range res.Rows {
		if r[0] != nil && r[1] != nil {
			done[*r[0]] = *r[1]
		}
	}
	var applied []string
	for _, m := range steps {
		sum := sha256.Sum256([]byte(m.SQL))
		check := hex.EncodeToString(sum[:])
		if old, ok := done[m.Name]; ok {
			if old != check {
				return applied, fmt.Errorf("%s changed after it ran on this database; add a new migration for the change instead", m.Name)
			}
			continue
		}
		ran, err := applyMigration(pool, table, m, check)
		if err != nil {
			return applied, fmt.Errorf("%s: %v (nothing from this file was kept)", m.Name, err)
		}
		if ran {
			applied = append(applied, m.Name)
		}
	}
	return applied, nil
}

func applyMigration(pool *pg.Pool, table string, m Migration, check string) (ran bool, err error) {
	cn, err := pool.Get()
	if err != nil {
		return false, err
	}
	defer pool.Put(cn)
	if err := cn.Script("begin; select pg_advisory_xact_lock(" + migrationLock + ")"); err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			cn.Script("rollback")
		}
	}()
	name := m.Name
	res, err := cn.Exec("select 1 from "+table+" where name = $1", []*string{&name})
	if err != nil {
		return false, err
	}
	if len(res.Rows) > 0 { // another copy of the program just ran it
		return false, cn.Script("rollback")
	}
	if err := cn.Script(m.SQL); err != nil {
		return false, err
	}
	if _, err := cn.Exec("insert into "+table+" (name, checksum) values ($1, $2)", []*string{&name, &check}); err != nil {
		return false, err
	}
	return true, cn.Script("commit")
}

// readMigrations lists the .sql files of a folder, in name order.
func (in *Interp) readMigrations(line int, dir string) []Migration {
	names, err := in.listAssets(dir)
	if err != nil {
		in.fail(line, "I couldn't read the migrations folder %s", dir)
	}
	sort.Strings(names)
	var out []Migration
	for _, n := range names {
		if !strings.HasSuffix(n, ".sql") {
			continue
		}
		sql, err := in.readAsset(path.Join(dir, n))
		if err != nil {
			in.fail(line, "I couldn't read %s", n)
		}
		out = append(out, Migration{n, sql})
	}
	return out
}

// listAssets lists the files in a folder next to the program.
func (in *Interp) listAssets(dir string) ([]string, error) {
	if in.ListAssets != nil {
		return in.ListAssets(dir)
	}
	full := dir
	if in.Dir != "" && !strings.HasPrefix(dir, "/") {
		full = in.Dir + "/" + dir
	}
	return listDir(full)
}

func (in *Interp) dbMigrate(db *Database) *Builtin {
	return builtin("migrate", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "db.migrate", a, 1)
		if db.conn != nil {
			in.fail(line, "db.migrate can't run inside a transaction")
		}
		steps := in.readMigrations(line, Text(a[0]))
		var applied []string
		var err error
		in.outside(func() {
			applied, err = Migrate(db.pool, steps)
		})
		if err != nil {
			in.fail(line, "migrations: %v", err)
		}
		out := newList(nil)
		for _, n := range applied {
			out.Items = append(out.Items, n)
		}
		return out
	})
}
