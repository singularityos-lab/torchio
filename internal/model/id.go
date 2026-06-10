package model

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// ID is a 128-bit, lexicographically sortable identifier in the ULID layout:
// a 48-bit millisecond timestamp followed by 80 bits of randomness, rendered
// as 26 Crockford base32 characters. Sortability matters because jobs and
// printers are listed in creation order straight from their primary key, with
// no extra timestamp column to sort on. Implemented in-tree to keep the
// dependency surface minimal (the zero-dep ethos of the ecosystem).
type ID [16]byte

// crockford is the ULID base32 alphabet (no I, L, O, U to avoid ambiguity).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID mints a fresh ID stamped with the current time.
func NewID() ID {
	return newIDAt(time.Now())
}

// newIDAt builds an ID for a specific instant (separated for testing).
func newIDAt(t time.Time) ID {
	var id ID
	ms := uint64(t.UnixMilli())
	// 48-bit big-endian timestamp in the first 6 bytes.
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], ms)
	copy(id[0:6], b[2:8])
	// 80 bits of randomness in the last 10 bytes.
	if _, err := rand.Read(id[6:16]); err != nil {
		// crypto/rand failing is catastrophic and not something a print
		// system can paper over; surface it loudly.
		panic(fmt.Sprintf("torchio: crypto/rand unavailable: %v", err))
	}
	return id
}

// Time returns the timestamp encoded in the ID.
func (id ID) Time() time.Time {
	var b [8]byte
	copy(b[2:8], id[0:6])
	ms := binary.BigEndian.Uint64(b[:])
	return time.UnixMilli(int64(ms))
}

// String renders the ID as 26 Crockford base32 characters.
func (id ID) String() string {
	var dst [26]byte
	// Encode 128 bits, 5 bits per output character, MSB first. The first
	// character only carries 2 significant bits (130 bits of output capacity
	// for 128 bits of input), matching the canonical ULID encoding.
	dst[0] = crockford[(id[0]&0xE0)>>5]
	dst[1] = crockford[id[0]&0x1F]
	dst[2] = crockford[(id[1]&0xF8)>>3]
	dst[3] = crockford[((id[1]&0x07)<<2)|((id[2]&0xC0)>>6)]
	dst[4] = crockford[(id[2]&0x3E)>>1]
	dst[5] = crockford[((id[2]&0x01)<<4)|((id[3]&0xF0)>>4)]
	dst[6] = crockford[((id[3]&0x0F)<<1)|((id[4]&0x80)>>7)]
	dst[7] = crockford[(id[4]&0x7C)>>2]
	dst[8] = crockford[((id[4]&0x03)<<3)|((id[5]&0xE0)>>5)]
	dst[9] = crockford[id[5]&0x1F]
	dst[10] = crockford[(id[6]&0xF8)>>3]
	dst[11] = crockford[((id[6]&0x07)<<2)|((id[7]&0xC0)>>6)]
	dst[12] = crockford[(id[7]&0x3E)>>1]
	dst[13] = crockford[((id[7]&0x01)<<4)|((id[8]&0xF0)>>4)]
	dst[14] = crockford[((id[8]&0x0F)<<1)|((id[9]&0x80)>>7)]
	dst[15] = crockford[(id[9]&0x7C)>>2]
	dst[16] = crockford[((id[9]&0x03)<<3)|((id[10]&0xE0)>>5)]
	dst[17] = crockford[id[10]&0x1F]
	dst[18] = crockford[(id[11]&0xF8)>>3]
	dst[19] = crockford[((id[11]&0x07)<<2)|((id[12]&0xC0)>>6)]
	dst[20] = crockford[(id[12]&0x3E)>>1]
	dst[21] = crockford[((id[12]&0x01)<<4)|((id[13]&0xF0)>>4)]
	dst[22] = crockford[((id[13]&0x0F)<<1)|((id[14]&0x80)>>7)]
	dst[23] = crockford[(id[14]&0x7C)>>2]
	dst[24] = crockford[((id[14]&0x03)<<3)|((id[15]&0xE0)>>5)]
	dst[25] = crockford[id[15]&0x1F]
	return string(dst[:])
}

// IsZero reports whether the ID is the zero value.
func (id ID) IsZero() bool {
	return id == ID{}
}

// dec maps a Crockford base32 character to its 5-bit value, or 0xFF if invalid.
// Built once at init from the encoding alphabet.
var dec = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = 0xFF
	}
	for i := 0; i < len(crockford); i++ {
		t[crockford[i]] = byte(i)
	}
	return t
}()

// ParseID decodes the 26-character Crockford base32 form produced by String
// back into an ID. It is the inverse of String and accepts that exact format.
func ParseID(s string) (ID, error) {
	var id ID
	if len(s) != 26 {
		return id, fmt.Errorf("invalid ID %q: length %d, want 26", s, len(s))
	}
	// Accumulate the 26 base32 digits into a 128-bit value (hi, lo). Encoding
	// is MSB-first with a 2-bit zero pad, so shifting in 5 bits per digit and
	// keeping the low 128 bits reproduces the original big-endian bytes.
	var hi, lo uint64
	for i := 0; i < 26; i++ {
		v := dec[s[i]]
		if v == 0xFF {
			return id, fmt.Errorf("invalid ID %q: bad character %q", s, s[i])
		}
		hi = (hi << 5) | (lo >> 59)
		lo = (lo << 5) | uint64(v)
	}
	binary.BigEndian.PutUint64(id[0:8], hi)
	binary.BigEndian.PutUint64(id[8:16], lo)
	return id, nil
}
