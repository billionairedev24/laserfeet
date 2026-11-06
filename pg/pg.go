// Package pg is Laser Feet's own PostgreSQL client, written with only Go's
// standard library. It speaks PostgreSQL's wire protocol (version 3) directly.
//
// Security choices:
//   - Login with SCRAM-SHA-256 (and checks the server's proof, so a fake server
//     can't pretend), or MD5 for old servers. Plain-text passwords are only sent
//     over TLS or to the same machine.
//   - TLS that checks the server's certificate by default for any database that
//     isn't on the same machine (sslmode=verify-full).
//   - Values are always sent separately from SQL ($1, $2 ...), never pasted into
//     it, so SQL injection through values isn't possible.
//   - Firm limits: a 10 second connection timeout and a 64 MB message limit.
package pg

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config says how to reach a database.
type Config struct {
	Host, Port, User, Password, Database string
	SSLMode                              string // disable, require, verify-full
}

// ParseURL reads a URL like postgres://user:password@host:5432/dbname?sslmode=verify-full.
func ParseURL(s string) (Config, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return Config{}, errors.New("the database address must look like postgres://user:password@host:5432/database")
	}
	c := Config{Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/")}
	c.Password, _ = u.User.Password()
	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	if c.Database == "" {
		c.Database = c.User
	}
	c.SSLMode = u.Query().Get("sslmode")
	if c.SSLMode == "" {
		if isLocal(c.Host) {
			c.SSLMode = "disable"
		} else {
			c.SSLMode = "verify-full" // safe by default: encrypted, and the certificate is checked
		}
	}
	switch c.SSLMode {
	case "disable", "require", "verify-full":
	case "prefer", "allow":
		return Config{}, fmt.Errorf("sslmode=%s can silently fall back to no encryption; use require or verify-full (or disable, for a database on the same machine)", c.SSLMode)
	default:
		return Config{}, fmt.Errorf("sslmode must be disable, require or verify-full, not %q", c.SSLMode)
	}
	if c.User == "" {
		return Config{}, errors.New("the database address needs a user name, like postgres://user:password@host/database")
	}
	return c, nil
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Error is a problem reported by the database.
type Error struct {
	Code, Message, Detail string
}

func (e *Error) Error() string {
	s := e.Message
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	return s + " [" + e.Code + "]"
}

// Field is one column of a result.
type Field struct {
	Name string
	Type uint32 // PostgreSQL type OID
}

// Result is what a statement gives back. Values are text, or nil for NULL.
type Result struct {
	Fields   []Field
	Rows     [][]*string
	Affected int64
}

const maxMessage = 64 << 20

// Conn is one connection to the database. It is not safe for use by several
// goroutines at once; Pool hands out connections one at a time.
type Conn struct {
	nc     net.Conn
	r      *bufio.Reader
	w      []byte
	broken bool
}

// Connect opens a connection and logs in.
func Connect(c Config) (*Conn, error) {
	nc, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, c.Port), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach the database at %s:%s: %v", c.Host, c.Port, err)
	}
	nc.SetDeadline(time.Now().Add(30 * time.Second))
	cn := &Conn{nc: nc, r: bufio.NewReader(nc)}
	if c.SSLMode != "disable" {
		if err := cn.startTLS(c); err != nil {
			nc.Close()
			return nil, err
		}
	}
	if err := cn.startup(c); err != nil {
		cn.nc.Close()
		return nil, err
	}
	cn.nc.SetDeadline(time.Time{})
	return cn, nil
}

func (cn *Conn) startTLS(c Config) error {
	var req [8]byte
	binary.BigEndian.PutUint32(req[0:], 8)
	binary.BigEndian.PutUint32(req[4:], 80877103)
	if _, err := cn.nc.Write(req[:]); err != nil {
		return err
	}
	answer, err := cn.r.ReadByte()
	if err != nil {
		return err
	}
	if answer != 'S' {
		return errors.New("the database doesn't accept encrypted (TLS) connections. Turn on TLS on the server, or use sslmode=disable for a database on the same machine")
	}
	conf := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.SSLMode == "require" {
		conf.InsecureSkipVerify = true // encrypted, but the certificate isn't checked: the user chose require
	}
	t := tls.Client(cn.nc, conf)
	if err := t.Handshake(); err != nil {
		return fmt.Errorf("TLS with the database failed: %v", err)
	}
	cn.nc, cn.r = t, bufio.NewReader(t)
	return nil
}

// ---------- messages ----------

func (cn *Conn) begin(kind byte) {
	cn.w = append(cn.w, kind, 0, 0, 0, 0)
}

func (cn *Conn) str(s string) { cn.w = append(append(cn.w, s...), 0) }

func (cn *Conn) i32(n int32) { cn.w = binary.BigEndian.AppendUint32(cn.w, uint32(n)) }

func (cn *Conn) i16(n int16) { cn.w = binary.BigEndian.AppendUint16(cn.w, uint16(n)) }

// end fills in the length of the message started at offset start.
func (cn *Conn) end(start int) {
	binary.BigEndian.PutUint32(cn.w[start+1:], uint32(len(cn.w)-start-1))
}

func (cn *Conn) flush() error {
	_, err := cn.nc.Write(cn.w)
	cn.w = cn.w[:0]
	if err != nil {
		cn.broken = true
	}
	return err
}

func (cn *Conn) read() (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(cn.r, head[:]); err != nil {
		cn.broken = true
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(head[1:])) - 4
	if n < 0 || n > maxMessage {
		cn.broken = true
		return 0, nil, errors.New("the database sent a message that is too large")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(cn.r, body); err != nil {
		cn.broken = true
		return 0, nil, err
	}
	return head[0], body, nil
}

func parseError(body []byte) *Error {
	e := &Error{}
	for len(body) > 1 {
		code := body[0]
		i := 1
		for i < len(body) && body[i] != 0 {
			i++
		}
		v := string(body[1:i])
		switch code {
		case 'C':
			e.Code = v
		case 'M':
			e.Message = v
		case 'D':
			e.Detail = v
		}
		if i+1 > len(body) {
			break
		}
		body = body[i+1:]
	}
	return e
}

// ---------- login ----------

func (cn *Conn) startup(c Config) error {
	cn.w = cn.w[:0]
	cn.i32(0)
	cn.i32(196608) // protocol 3.0
	for _, kv := range [][2]string{{"user", c.User}, {"database", c.Database}, {"application_name", "laserfeet"}, {"client_encoding", "UTF8"}, {"DateStyle", "ISO, YMD"}, {"TimeZone", "UTC"}} {
		cn.str(kv[0])
		cn.str(kv[1])
	}
	cn.w = append(cn.w, 0)
	binary.BigEndian.PutUint32(cn.w, uint32(len(cn.w)))
	if err := cn.flush(); err != nil {
		return err
	}
	var scram *scramState
	for {
		kind, body, err := cn.read()
		if err != nil {
			return err
		}
		switch kind {
		case 'E':
			return parseError(body)
		case 'R':
			if len(body) < 4 {
				return errors.New("bad login message from the database")
			}
			switch code := binary.BigEndian.Uint32(body); code {
			case 0: // logged in
			case 3: // plain-text password
				if c.SSLMode == "disable" && !isLocal(c.Host) {
					return errors.New("the database asked for the password in plain text over an unencrypted connection; refusing. Use TLS (sslmode=verify-full)")
				}
				cn.password(c.Password)
			case 5: // MD5
				if len(body) < 8 {
					return errors.New("bad login message from the database")
				}
				inner := md5.Sum([]byte(c.Password + c.User))
				outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), body[4:8]...))
				cn.password("md5" + hex.EncodeToString(outer[:]))
			case 10: // SASL
				if !strings.Contains(string(body[4:]), "SCRAM-SHA-256\x00") {
					return errors.New("the database wants a login method Laser Feet doesn't support (it supports SCRAM-SHA-256 and MD5)")
				}
				scram = newScram(c.Password)
				first := scram.clientFirst()
				start := len(cn.w)
				cn.begin('p')
				cn.str("SCRAM-SHA-256")
				cn.i32(int32(len(first)))
				cn.w = append(cn.w, first...)
				cn.end(start)
			case 11: // SASL continue
				if scram == nil {
					return errors.New("unexpected login message from the database")
				}
				final, err := scram.clientFinal(string(body[4:]))
				if err != nil {
					return err
				}
				start := len(cn.w)
				cn.begin('p')
				cn.w = append(cn.w, final...)
				cn.end(start)
			case 12: // SASL final: check the server knows the password too
				if scram == nil || !scram.verifyServer(string(body[4:])) {
					return errors.New("the database's proof of identity was wrong; refusing to continue")
				}
			default:
				return fmt.Errorf("the database wants a login method Laser Feet doesn't support (code %d)", code)
			}
			if len(cn.w) > 0 {
				if err := cn.flush(); err != nil {
					return err
				}
			}
		case 'Z':
			return nil
		case 'S', 'K', 'N':
			// settings, the cancel key, notices: nothing to do
		default:
			return fmt.Errorf("unexpected message %q from the database while logging in", kind)
		}
	}
}

func (cn *Conn) password(p string) {
	start := len(cn.w)
	cn.begin('p')
	cn.str(p)
	cn.end(start)
}

type scramState struct {
	password, nonce, firstBare, authMessage string
	salted                                  []byte
}

func newScram(password string) *scramState {
	b := make([]byte, 18)
	rand.Read(b)
	return &scramState{password: password, nonce: base64.RawStdEncoding.EncodeToString(b)}
}

func (s *scramState) clientFirst() string {
	s.firstBare = "n=,r=" + s.nonce
	return "n,," + s.firstBare
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func (s *scramState) clientFinal(serverFirst string) (string, error) {
	var nonce, salt string
	var iter int
	for _, part := range strings.Split(serverFirst, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "r":
			nonce = v
		case "s":
			salt = v
		case "i":
			iter, _ = strconv.Atoi(v)
		}
	}
	// PostgreSQL uses 4096; far more would only let a hostile server stall the login
	if !strings.HasPrefix(nonce, s.nonce) || salt == "" || iter < 4096 || iter > 1_000_000 {
		return "", errors.New("the database sent an unsafe or invalid SCRAM challenge; refusing")
	}
	saltBytes, err := base64.StdEncoding.DecodeString(salt)
	if err != nil {
		return "", errors.New("the database sent an invalid SCRAM salt")
	}
	s.salted, err = pbkdf2.Key(sha256.New, s.password, saltBytes, iter, 32)
	if err != nil {
		return "", err
	}
	clientKey := hmacSHA256(s.salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	withoutProof := "c=biws,r=" + nonce
	s.authMessage = s.firstBare + "," + serverFirst + "," + withoutProof
	sig := hmacSHA256(stored[:], s.authMessage)
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ sig[i]
	}
	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

func (s *scramState) verifyServer(final string) bool {
	v, ok := strings.CutPrefix(final, "v=")
	if !ok {
		return false
	}
	got, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return false
	}
	want := hmacSHA256(hmacSHA256(s.salted, "Server Key"), s.authMessage)
	return hmac.Equal(got, want)
}

// ---------- queries ----------

// Exec runs one SQL statement with its values sent separately ($1, $2 ...).
// A nil value is NULL.
func (cn *Conn) Exec(sql string, args []*string) (*Result, error) {
	if cn.broken {
		return nil, errors.New("this database connection is broken")
	}
	cn.nc.SetDeadline(time.Now().Add(5 * time.Minute))
	defer cn.nc.SetDeadline(time.Time{})

	start := len(cn.w)
	cn.begin('P') // Parse
	cn.str("")
	cn.str(sql)
	cn.i16(0)
	cn.end(start)

	start = len(cn.w)
	cn.begin('B') // Bind
	cn.str("")
	cn.str("")
	cn.i16(0) // all values as text
	cn.i16(int16(len(args)))
	for _, a := range args {
		if a == nil {
			cn.i32(-1)
		} else {
			cn.i32(int32(len(*a)))
			cn.w = append(cn.w, *a...)
		}
	}
	cn.i16(0) // all results as text
	cn.end(start)

	start = len(cn.w)
	cn.begin('D') // Describe the portal
	cn.w = append(cn.w, 'P')
	cn.str("")
	cn.end(start)

	start = len(cn.w)
	cn.begin('E') // Execute
	cn.str("")
	cn.i32(0)
	cn.end(start)

	start = len(cn.w)
	cn.begin('S') // Sync
	cn.end(start)
	if len(args) > 32767 {
		return nil, errors.New("too many values for one statement (at most 32767)")
	}
	if err := cn.flush(); err != nil {
		return nil, err
	}

	res := &Result{}
	var failed error
	for {
		kind, body, err := cn.read()
		if err != nil {
			return nil, err
		}
		switch kind {
		case '1', '2', 'n', 's', 'I', 'N', 'S':
		case 'T':
			res.Fields = parseFields(body)
		case 'D':
			row, err := parseRow(body, len(res.Fields))
			if err != nil {
				cn.broken = true
				return nil, err
			}
			res.Rows = append(res.Rows, row)
		case 'C':
			tag := strings.TrimRight(string(body), "\x00")
			if i := strings.LastIndex(tag, " "); i >= 0 {
				res.Affected, _ = strconv.ParseInt(tag[i+1:], 10, 64)
			}
		case 'E':
			failed = parseError(body)
		case 'Z':
			if failed != nil {
				return nil, failed
			}
			return res, nil
		default:
			return nil, fmt.Errorf("unexpected message %q from the database", kind)
		}
	}
}

func parseFields(b []byte) []Field {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	out := make([]Field, 0, n)
	for i := 0; i < n; i++ {
		end := 0
		for end < len(b) && b[end] != 0 {
			end++
		}
		if end+19 > len(b) {
			break
		}
		name := string(b[:end])
		b = b[end+1:]
		typ := binary.BigEndian.Uint32(b[6:10])
		b = b[18:]
		out = append(out, Field{Name: name, Type: typ})
	}
	return out
}

func parseRow(b []byte, n int) ([]*string, error) {
	if len(b) < 2 {
		return nil, errors.New("bad row from the database")
	}
	cols := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	row := make([]*string, cols)
	for i := 0; i < cols; i++ {
		if len(b) < 4 {
			return nil, errors.New("bad row from the database")
		}
		l := int32(binary.BigEndian.Uint32(b))
		b = b[4:]
		if l < 0 {
			continue // NULL
		}
		if int(l) > len(b) {
			return nil, errors.New("bad row from the database")
		}
		v := string(b[:l])
		row[i] = &v
		b = b[l:]
	}
	return row, nil
}

// Close ends the connection politely.
func (cn *Conn) Close() error {
	cn.w = cn.w[:0]
	start := len(cn.w)
	cn.begin('X')
	cn.end(start)
	cn.flush()
	return cn.nc.Close()
}

// ---------- pool ----------

// Pool shares a few connections between everyone who needs one.
type Pool struct {
	cfg  Config
	mu   sync.Mutex
	idle []*Conn
	max  int
	open int
	cond *sync.Cond
}

// NewPool makes a pool that opens at most max connections.
func NewPool(cfg Config, max int) *Pool {
	p := &Pool{cfg: cfg, max: max}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Get takes a connection from the pool, opening one if needed.
func (p *Pool) Get() (*Conn, error) {
	p.mu.Lock()
	for len(p.idle) == 0 && p.open >= p.max {
		p.cond.Wait()
	}
	if n := len(p.idle); n > 0 {
		c := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return c, nil
	}
	p.open++
	p.mu.Unlock()
	c, err := Connect(p.cfg)
	if err != nil {
		p.mu.Lock()
		p.open--
		p.cond.Signal()
		p.mu.Unlock()
		return nil, err
	}
	return c, nil
}

// Put returns a connection; broken ones are closed.
func (p *Pool) Put(c *Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.broken {
		c.nc.Close()
		p.open--
	} else {
		p.idle = append(p.idle, c)
	}
	p.cond.Signal()
}

// Exec runs one statement on a pooled connection.
func (p *Pool) Exec(sql string, args []*string) (*Result, error) {
	c, err := p.Get()
	if err != nil {
		return nil, err
	}
	defer p.Put(c)
	return c.Exec(sql, args)
}
