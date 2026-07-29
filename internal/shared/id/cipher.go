package id

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"

	"shopnexus/internal/shared/errx"
)

// rounds is the Feistel round count. Four rounds of a keyed PRF is the standard
// Luby-Rackoff construction for a strong pseudorandom permutation.
const rounds = 4

const (
	// alphabet is Crockford base32, lowercase: no i, l, o or u, so a
	// hand-transcribed id stays unambiguous.
	alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	// encodedLen is ceil(64/5): 13 characters cover a 64-bit value exactly.
	encodedLen = 13
)

// codec holds the AES block used as the Feistel round function.
type codec struct {
	block cipher.Block
}

// current is the process-wide codec. It has to be global: json.Marshaler takes
// no arguments, so an ID cannot carry a dependency. Set it once at startup.
var current atomic.Pointer[codec]

// SetCipher installs the id cipher from a 16, 24 or 32-byte key. Call it once at
// startup (an fx.Invoke in cmd/gateway) before the HTTP server accepts traffic.
// Rotating the key invalidates every id already handed out.
func SetCipher(key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("build id cipher: %w", err)
	}
	current.Store(&codec{block: block})
	return nil
}

// load returns the installed codec, or panics: marshalling an id before startup
// finished wiring is a programming error, not a request-time condition.
func load() *codec {
	c := current.Load()
	if c == nil {
		panic("id: cipher not installed — call id.SetCipher at startup")
	}
	return c
}

func encode(prefix string, v uint64) string {
	return encodeBase32(load().encrypt(tweakOf(prefix), v))
}

func decode(prefix, s string) (uint64, error) {
	raw, err := decodeBase32(s)
	if err != nil {
		return 0, err
	}
	return load().decrypt(tweakOf(prefix), raw), nil
}

// encrypt applies the Feistel network: (l, r) -> (r, l ^ F(r)) per round.
func (c *codec) encrypt(tweak [8]byte, v uint64) uint64 {
	l, r := uint32(v>>32), uint32(v)
	for round := range rounds {
		l, r = r, l^c.f(tweak, byte(round), r)
	}
	return uint64(l)<<32 | uint64(r)
}

// decrypt runs the same rounds backwards: (l, r) -> (r ^ F(l), l).
func (c *codec) decrypt(tweak [8]byte, v uint64) uint64 {
	l, r := uint32(v>>32), uint32(v)
	for round := rounds - 1; round >= 0; round-- {
		l, r = r^c.f(tweak, byte(round), l), l
	}
	return uint64(l)<<32 | uint64(r)
}

// f is the round function: one AES block over the round number, the kind tweak
// and the half being mixed. Keyed by the cipher, so the permutation is only
// invertible with the key.
func (c *codec) f(tweak [8]byte, round byte, x uint32) uint32 {
	var in, out [aes.BlockSize]byte
	in[0] = round
	copy(in[1:9], tweak[:])
	binary.BigEndian.PutUint32(in[9:13], x)
	c.block.Encrypt(out[:], in[:])
	return binary.BigEndian.Uint32(out[:4])
}

// tweaks memoizes prefix -> tweak; every encode and decode needs one.
var tweaks sync.Map // map[string][8]byte

// tweakOf derives a per-kind tweak, so the same number encodes differently for
// each entity.
func tweakOf(prefix string) [8]byte {
	if t, ok := tweaks.Load(prefix); ok {
		return t.([8]byte)
	}
	sum := sha256.Sum256([]byte(prefix))
	var t [8]byte
	copy(t[:], sum[:8])
	tweaks.Store(prefix, t)
	return t
}

func encodeBase32(v uint64) string {
	var out [encodedLen]byte
	for i := encodedLen - 1; i >= 0; i-- {
		out[i] = alphabet[v&31]
		v >>= 5
	}
	return string(out[:])
}

// decodeTable maps a byte to its alphabet value, or -1. Built once: it folds
// case and accepts the Crockford aliases i/l -> 1 and o -> 0.
var decodeTable = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i, c := range []byte(alphabet) {
		t[c] = int8(i)
		if c >= 'a' && c <= 'z' {
			t[c-'a'+'A'] = int8(i)
		}
	}
	for _, c := range []byte("iIlL") {
		t[c] = 1
	}
	for _, c := range []byte("oO") {
		t[c] = 0
	}
	return t
}()

func decodeBase32(s string) (uint64, error) {
	if len(s) != encodedLen {
		return 0, errx.ErrInvalidID
	}
	var v uint64
	for i := range len(s) {
		d := decodeTable[s[i]]
		if d < 0 {
			return 0, errx.ErrInvalidID
		}
		// 13 characters carry 65 bits, so the leading one may only spend 4 of
		// them: a bigger value would not fit in a uint64.
		if i == 0 && d > 15 {
			return 0, errx.ErrInvalidID
		}
		v = v<<5 | uint64(d)
	}
	return v, nil
}
