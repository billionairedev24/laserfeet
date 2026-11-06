package lang

import "laserfeet/auth"

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
