package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Argon2id settings for new passwords: 19 MiB of memory, 2 passes, 1 lane,
// a 16-byte salt and a 32-byte hash (the OWASP recommendation).
const (
	DefaultMemory  = 19 * 1024 // KiB
	DefaultPasses  = 2
	DefaultLanes   = 1
	saltLen        = 16
	tagLen         = 32
	argonVersion   = 0x13
	argonTypeID    = 2
	blockWords     = 128 // a block is 1 KiB: 128 64-bit words
	syncPoints     = 4
	maxCheckMemory = 256 * 1024 // never spend more than 256 MiB checking a hash
)

type block [blockWords]uint64

// Argon2id computes an Argon2id tag (RFC 9106). secret and data may be nil.
func Argon2id(password, salt, secret, data []byte, passes, memoryKiB, lanes, tagLength uint32) []byte {
	if passes < 1 || lanes < 1 || memoryKiB < 8*lanes || tagLength < 4 {
		panic("argon2id: bad parameters")
	}
	h0 := initialHash(password, salt, secret, data, passes, memoryKiB, lanes, tagLength)
	blocks := memoryKiB / (syncPoints * lanes) * (syncPoints * lanes)
	q := blocks / lanes // blocks per lane
	segment := q / syncPoints
	B := make([]block, blocks)
	var buf [1024]byte
	for l := uint32(0); l < lanes; l++ {
		for j := uint32(0); j < 2; j++ {
			in := make([]byte, 72)
			copy(in, h0)
			binary.LittleEndian.PutUint32(in[64:], j)
			binary.LittleEndian.PutUint32(in[68:], l)
			copy(buf[:], hashLong(in, 1024))
			for k := range B[l*q+j] {
				B[l*q+j][k] = binary.LittleEndian.Uint64(buf[8*k:])
			}
		}
	}
	for pass := uint32(0); pass < passes; pass++ {
		for slice := uint32(0); slice < syncPoints; slice++ {
			for lane := uint32(0); lane < lanes; lane++ {
				fillSegment(B, pass, slice, lane, lanes, q, segment, passes, blocks)
			}
		}
	}
	var final block
	for l := uint32(0); l < lanes; l++ {
		x := &B[l*q+q-1]
		for k := range final {
			final[k] ^= x[k]
		}
	}
	for k, v := range final {
		binary.LittleEndian.PutUint64(buf[8*k:], v)
	}
	return hashLong(buf[:], tagLength)
}

func initialHash(password, salt, secret, data []byte, passes, memory, lanes, tagLength uint32) []byte {
	d := newBlake2b(64)
	le := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		d.Write(b[:])
	}
	le(lanes)
	le(tagLength)
	le(memory)
	le(passes)
	le(argonVersion)
	le(argonTypeID)
	for _, part := range [][]byte{password, salt, secret, data} {
		le(uint32(len(part)))
		d.Write(part)
	}
	return d.Sum()
}

// hashLong is the variable-length hash H' of RFC 9106.
func hashLong(in []byte, n uint32) []byte {
	var lenBytes [4]byte
	binary.LittleEndian.PutUint32(lenBytes[:], n)
	if n <= 64 {
		return Blake2b(int(n), lenBytes[:], in)
	}
	out := make([]byte, 0, n)
	v := Blake2b(64, lenBytes[:], in)
	r := (n+31)/32 - 2
	out = append(out, v[:32]...)
	for i := uint32(1); i < r; i++ {
		v = Blake2b(64, v)
		out = append(out, v[:32]...)
	}
	last := n - 32*r
	out = append(out, Blake2b(int(last), v)...)
	return out
}

func fillSegment(B []block, pass, slice, lane, lanes, q, segment, passes, total uint32) {
	independent := pass == 0 && slice < syncPoints/2 // Argon2id: the first half of the first pass
	var address, input, zero block
	if independent {
		input[0] = uint64(pass)
		input[1] = uint64(lane)
		input[2] = uint64(slice)
		input[3] = uint64(total)
		input[4] = uint64(passes)
		input[5] = argonTypeID
	}
	start := uint32(0)
	if pass == 0 && slice == 0 {
		start = 2
		if independent {
			nextAddresses(&address, &input, &zero)
		}
	}
	for index := start; index < segment; index++ {
		j := slice*segment + index // position in the lane
		cur := lane*q + j
		prev := cur - 1
		if j == 0 {
			prev = lane*q + q - 1
		}
		var rand uint64
		if independent {
			if index%blockWords == 0 {
				nextAddresses(&address, &input, &zero)
			}
			rand = address[index%blockWords]
		} else {
			rand = B[prev][0]
		}
		refLane := uint32(rand>>32) % lanes
		if pass == 0 && slice == 0 {
			refLane = lane
		}
		// how many blocks may be referenced
		var area uint32
		sameLane := refLane == lane
		switch {
		case pass == 0 && sameLane:
			area = slice*segment + index - 1
		case pass == 0:
			area = slice * segment
			if index == 0 {
				area--
			}
		case sameLane:
			area = q - segment + index - 1
		default:
			area = q - segment
			if index == 0 {
				area--
			}
		}
		j1 := rand & 0xffffffff
		x := (j1 * j1) >> 32
		y := (uint64(area) * x) >> 32
		rel := uint64(area) - 1 - y
		startPos := uint64(0)
		if pass != 0 && slice != syncPoints-1 {
			startPos = uint64((slice + 1) * segment)
		}
		ref := refLane*q + uint32((startPos+rel)%uint64(q))
		compress(&B[cur], &B[prev], &B[ref], pass > 0)
	}
}

// nextAddresses makes the next block of reference positions for the
// password-independent part (G(0, G(0, input))).
func nextAddresses(address, input, zero *block) {
	input[6]++
	compress(address, zero, input, false)
	compress(address, zero, address, false)
}

// compress is the function G of RFC 9106: out = P(x XOR y) XOR (x XOR y),
// XORed into out instead when xorInto is set (passes after the first).
func compress(out, x, y *block, xorInto bool) {
	var r, z block
	for i := range r {
		r[i] = x[i] ^ y[i]
	}
	z = r
	for i := 0; i < 8; i++ { // rows: 16 words each
		p(&z, 16*i, 16*i+1, 16*i+2, 16*i+3, 16*i+4, 16*i+5, 16*i+6, 16*i+7,
			16*i+8, 16*i+9, 16*i+10, 16*i+11, 16*i+12, 16*i+13, 16*i+14, 16*i+15)
	}
	for i := 0; i < 8; i++ { // columns: pairs of words
		b := 2 * i
		p(&z, b, b+1, b+16, b+17, b+32, b+33, b+48, b+49,
			b+64, b+65, b+80, b+81, b+96, b+97, b+112, b+113)
	}
	if xorInto {
		for i := range out {
			out[i] ^= z[i] ^ r[i]
		}
	} else {
		for i := range out {
			out[i] = z[i] ^ r[i]
		}
	}
}

func fBlaMka(a, b uint64) uint64 {
	return a + b + 2*(a&0xffffffff)*(b&0xffffffff)
}

func gb(v *block, a, b, c, d int) {
	v[a] = fBlaMka(v[a], v[b])
	v[d] = rotr(v[d]^v[a], 32)
	v[c] = fBlaMka(v[c], v[d])
	v[b] = rotr(v[b]^v[c], 24)
	v[a] = fBlaMka(v[a], v[b])
	v[d] = rotr(v[d]^v[a], 16)
	v[c] = fBlaMka(v[c], v[d])
	v[b] = rotr(v[b]^v[c], 63)
}

func rotr(x uint64, n uint) uint64 { return x>>n | x<<(64-n) }

// p is the BLAKE2b round on 16 words of a block, given by position.
func p(v *block, i0, i1, i2, i3, i4, i5, i6, i7, i8, i9, i10, i11, i12, i13, i14, i15 int) {
	gb(v, i0, i4, i8, i12)
	gb(v, i1, i5, i9, i13)
	gb(v, i2, i6, i10, i14)
	gb(v, i3, i7, i11, i15)
	gb(v, i0, i5, i10, i15)
	gb(v, i1, i6, i11, i12)
	gb(v, i2, i7, i8, i13)
	gb(v, i3, i4, i9, i14)
}

var b64 = base64.RawStdEncoding

// HashPassword hashes a password for storing, in the standard text form:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func HashPassword(password string) string {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	tag := Argon2id([]byte(password), salt, nil, nil, DefaultPasses, DefaultMemory, DefaultLanes, tagLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", DefaultMemory, DefaultPasses, DefaultLanes, b64.EncodeToString(salt), b64.EncodeToString(tag))
}

// ErrBadHash means a stored hash isn't one this program can check.
var ErrBadHash = errors.New("this isn't an Argon2id password hash")

// CheckPassword reports whether password matches a hash made by HashPassword
// (or any standard Argon2id hash), comparing in constant time.
func CheckPassword(password, hash string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrBadHash
	}
	var m, t, lanes uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &lanes); err != nil {
		return false, ErrBadHash
	}
	// a hash can't make checking take huge time or memory
	if t < 1 || t > 10 || lanes < 1 || lanes > 16 || m < 8*lanes || m > maxCheckMemory {
		return false, ErrBadHash
	}
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(want) < 16 || len(want) > 64 {
		return false, ErrBadHash
	}
	got := Argon2id([]byte(password), salt, nil, nil, t, m, lanes, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether a hash uses weaker settings than new hashes do.
func NeedsRehash(hash string) bool {
	var m, t, lanes uint32
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return true
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &lanes); err != nil {
		return true
	}
	return m < DefaultMemory || t < DefaultPasses
}

// Token makes a random, URL-safe token with n bytes of randomness.
func Token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
