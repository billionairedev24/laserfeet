package lang

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const sessionCookie = "lf_session"

// sessionKeys signs session cookies, so visitors can read their own session
// but can't change it or make one up.
type sessionKeys struct {
	key []byte
	age time.Duration
}

// Session is what a server remembers about one visitor between requests:
// request.session.set("user", id). It lives in a signed cookie.
type Session struct {
	data    map[string]string
	changed bool
}

type sessionWire struct {
	D map[string]string `json:"d"`
	E int64             `json:"e"` // expires, in seconds since 1970
}

func (k *sessionKeys) sign(payload string) string {
	m := hmac.New(sha256.New, k.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// read checks a session cookie; anything changed, expired or made up is an empty session.
func (k *sessionKeys) read(cookie string, now time.Time) *Session {
	s := &Session{data: map[string]string{}}
	payload, sig, ok := strings.Cut(cookie, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(k.sign(payload))) {
		return s
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return s
	}
	var w sessionWire
	if json.Unmarshal(raw, &w) != nil || now.Unix() > w.E || w.D == nil {
		return s
	}
	s.data = w.D
	return s
}

// cookie writes the session as a Set-Cookie value (or removes it, when empty).
func (k *sessionKeys) cookie(s *Session) string {
	if len(s.data) == 0 {
		return cookieHeader(sessionCookie, "", -1, false)
	}
	raw, _ := json.Marshal(sessionWire{D: s.data, E: time.Now().Add(k.age).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return cookieHeader(sessionCookie, payload+"."+k.sign(payload), int(k.age.Seconds()), false)
}

func (in *Interp) sessionMember(s *Session, name string, line int) Value {
	switch name {
	case "get":
		return builtin("get", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "session.get", a, 1)
			if v, ok := s.data[Text(a[0])]; ok {
				return v
			}
			return nil
		})
	case "has":
		return builtin("has", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "session.has", a, 1)
			_, ok := s.data[Text(a[0])]
			return ok
		})
	case "set":
		return builtin("set", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "session.set", a, 2)
			s.data[Text(a[0])] = Text(a[1])
			if size := len(jsonString(s.data)); size > 3000 {
				in.fail(line, "a session must stay small (it's kept in a cookie); store an id in it and keep the rest in a database")
			}
			s.changed = true
			return nil
		})
	case "remove":
		return builtin("remove", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "session.remove", a, 1)
			delete(s.data, Text(a[0]))
			s.changed = true
			return nil
		})
	case "clear":
		return builtin("clear", func(in *Interp, line int, a []Value) Value {
			s.data = map[string]string{}
			s.changed = true
			return nil
		})
	}
	in.fail(line, "a session has get, set, has, remove and clear, but nothing called '%s'", name)
	return nil
}
