package auth

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// RFC 7693, appendix A, and other published BLAKE2b vectors.
func TestBlake2b(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 64, "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"},
		{"", 64, "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce"},
		{"", 32, "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8"},
		{"The quick brown fox jumps over the lazy dog", 64, "a8add4bdddfd93e4877d2746e62817b116364a1fa7bc148d95090bc7333b3673f82401cf7aa2e4cb1ecd90296e3f14cb5413f8ed77be73045b13914cdcd6a918"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(Blake2b(c.n, []byte(c.in)))
		if got != c.want {
			t.Errorf("BLAKE2b-%d(%q) = %s, want %s", c.n*8, c.in, got, c.want)
		}
	}
	// input longer than one block, and exactly one block, written in pieces
	long := bytes.Repeat([]byte("a"), 128)
	d := newBlake2b(64)
	d.Write(long[:50])
	d.Write(long[50:])
	if !bytes.Equal(d.Sum(), Blake2b(64, long)) {
		t.Error("writing in pieces must give the same hash")
	}
}

// RFC 9106, section 5.3.
func TestArgon2idRFCVector(t *testing.T) {
	password := bytes.Repeat([]byte{1}, 32)
	salt := bytes.Repeat([]byte{2}, 16)
	secret := bytes.Repeat([]byte{3}, 8)
	data := bytes.Repeat([]byte{4}, 12)
	got := hex.EncodeToString(Argon2id(password, salt, secret, data, 3, 32, 4, 32))
	want := "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659"
	if got != want {
		t.Fatalf("Argon2id = %s\nwant      %s", got, want)
	}
}

// Full-size hashes, checked against the reference implementation (via
// argon2-cffi): many address blocks per segment, and several lanes.
func TestArgon2idFullSize(t *testing.T) {
	cases := []struct {
		pw, salt              string
		passes, memory, lanes uint32
		want                  string
	}{
		{"correct horse", "saltsaltsaltsalt", 2, 19456, 1, "01dc1e04bc1c7e59cd5f61d55bc8b532dbbb7ebae7e0a8bb87fad8486b94ec8b"},
		{"pw", "0123456789abcdef", 3, 65536, 4, "b78385681e73e37278b7a7be8ccc909f883abbbca73062c303f8a5fe1d387e02"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(Argon2id([]byte(c.pw), []byte(c.salt), nil, nil, c.passes, c.memory, c.lanes, 32))
		if got != c.want {
			t.Errorf("Argon2id(%q, m=%d, t=%d, p=%d) = %s, want %s", c.pw, c.memory, c.passes, c.lanes, got, c.want)
		}
	}
}

func TestPasswords(t *testing.T) {
	start := time.Now()
	h := HashPassword("correct horse battery staple")
	t.Logf("hashing took %v", time.Since(start))
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash form: %s", h)
	}
	if ok, err := CheckPassword("correct horse battery staple", h); !ok || err != nil {
		t.Fatalf("the right password must match: %v %v", ok, err)
	}
	if ok, _ := CheckPassword("Correct horse battery staple", h); ok {
		t.Fatal("a different password must not match")
	}
	if HashPassword("x") == HashPassword("x") {
		t.Fatal("each hash must use a new salt")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA"} {
		if _, err := CheckPassword("x", bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if NeedsRehash(h) || !NeedsRehash("$argon2id$v=19$m=4096,t=1,p=1$x$y") {
		t.Error("NeedsRehash is wrong")
	}
	if len(Token(32)) != 43 {
		t.Error("a 32-byte token is 43 characters")
	}
}
