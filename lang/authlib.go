package lang

import (
	"strings"
	"time"

	"laserfeet/auth"
)

// authModule is 'use auth': password hashing (Argon2id) and random tokens.
func authModule() *Module {
	return &Module{Name: "auth", Members: map[string]Value{
		"hash": builtin("hash", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "auth.hash", a, 1)
			pw := in.passwordArg(line, a[0])
			in.gil.Unlock() // hashing takes a moment on purpose; let other work run
			h := auth.HashPassword(pw)
			in.gil.Lock()
			return h
		}),
		"check": builtin("check", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "auth.check", a, 2)
			pw := in.passwordArg(line, a[0])
			hash, ok := a[1].(string)
			if !ok {
				return false
			}
			in.gil.Unlock()
			match, err := auth.CheckPassword(pw, hash)
			in.gil.Lock()
			return err == nil && match
		}),
		"token": builtin("token", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "auth.token", a, 0)
			return auth.Token(32)
		}),
	}}
}

func (in *Interp) passwordArg(line int, v Value) string {
	pw, ok := v.(string)
	if !ok {
		in.fail(line, "a password must be Text")
	}
	if len(pw) > 1024 {
		in.fail(line, "a password can be at most 1024 characters")
	}
	return pw
}

// logModule is 'use log': one line of JSON per message, for log tools.
//
//	log.info("order placed", {order: id, total: total})
func logModule() *Module {
	level := func(name string) *Builtin {
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 2 {
				in.fail(line, "log.%s takes a message and optionally details, like log.%s(\"saved\", {id: id})", name, name)
			}
			var b strings.Builder
			b.WriteString(`{"time":`)
			writeJSON(&b, time.Now().UTC().Format(time.RFC3339Nano))
			b.WriteString(`,"level":`)
			writeJSON(&b, name)
			b.WriteString(`,"msg":`)
			writeJSON(&b, Text(a[0]))
			if len(a) == 2 {
				m, ok := a[1].(*Map)
				if !ok {
					in.fail(line, "the details for log.%s must be a map, like {id: id}", name)
				}
				for _, k := range m.Keys {
					if k == "time" || k == "level" || k == "msg" {
						continue
					}
					b.WriteByte(',')
					writeJSON(&b, k)
					b.WriteByte(':')
					writeJSON(&b, m.M[k])
				}
			}
			b.WriteByte('}')
			in.logLine(b.String())
			return nil
		})
	}
	return &Module{Name: "log", Members: map[string]Value{
		"info": level("info"), "warn": level("warn"), "error": level("error"),
	}}
}
