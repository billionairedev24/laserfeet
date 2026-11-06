package pg

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// fakeServer is a connection whose replies are whatever the fuzzer chooses.
type fakeServer struct{ r *bytes.Reader }

func (f *fakeServer) Read(b []byte) (int, error)         { return f.r.Read(b) }
func (f *fakeServer) Write(b []byte) (int, error)        { return len(b), nil }
func (f *fakeServer) Close() error                       { return nil }
func (f *fakeServer) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeServer) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeServer) SetDeadline(t time.Time) error      { return nil }
func (f *fakeServer) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeServer) SetWriteDeadline(t time.Time) error { return nil }

func msg(kind byte, body []byte) []byte {
	out := []byte{kind, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(len(body)+4))
	return append(out, body...)
}

func fakeConn(reply []byte) *Conn {
	f := &fakeServer{bytes.NewReader(reply)}
	return &Conn{nc: f, r: bufio.NewReader(f)}
}

// A server that sends nonsense must cause an error, never a crash or a hang.
func FuzzServerReplies(f *testing.F) {
	row := []byte{0, 1, 0, 0, 0, 2, 'h', 'i'}
	fields := append([]byte{0, 1, 'n', 0}, make([]byte, 18)...)
	f.Add(append(append(append(msg('1', nil), msg('2', nil)...), msg('T', fields)...), append(msg('D', row), append(msg('C', []byte("SELECT 1\x00")), msg('Z', []byte{'I'})...)...)...))
	f.Add(msg('E', []byte("SERROR\x00C42P01\x00Mno such table\x00\x00")))
	f.Add(msg('D', []byte{0xff, 0xff}))
	f.Fuzz(func(t *testing.T, reply []byte) {
		v := "x"
		fakeConn(reply).Exec("select $1", []*string{&v, nil})
	})
}

// Logging in to a server that answers with nonsense must fail safely.
func FuzzLogin(f *testing.F) {
	f.Add(msg('R', []byte{0, 0, 0, 0}))
	f.Add(msg('R', append([]byte{0, 0, 0, 10}, "SCRAM-SHA-256\x00\x00"...)))
	f.Add(append(msg('R', []byte{0, 0, 0, 11}), "r=abc,s=c2FsdA==,i=4096"...))
	f.Add(msg('R', []byte{0, 0, 0, 5, 1, 2, 3, 4}))
	f.Fuzz(func(t *testing.T, reply []byte) {
		fakeConn(reply).startup(Config{User: "u", Password: "p", Database: "d"})
	})
}

func FuzzParseURL(f *testing.F) {
	f.Add("postgres://user:pass@localhost:5432/db?sslmode=disable")
	f.Add("postgresql://u@[::1]/d")
	f.Fuzz(func(t *testing.T, s string) {
		ParseURL(s)
	})
}
