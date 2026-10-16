package model

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// maxULIDTime is the last instant a ULID can encode: its timestamp field is
// 48 bits of milliseconds.
var maxULIDTime = time.UnixMilli(1<<48 - 1).UTC()

// NewJobID returns a ULID for a job created at t, drawing 80 bits of
// randomness from entropy.
//
// A ULID is 48 bits of millisecond timestamp followed by 80 random bits, in
// Crockford base32. Ids therefore sort by creation time, which is what lets
// the store list a user's jobs newest first without a secondary index.
//
// Two ids minted in the same millisecond are ordered arbitrarily between
// themselves. The pipeline never depends on that, and an 80-bit random suffix
// makes a collision within one millisecond not worth guarding against.
func NewJobID(t time.Time, entropy io.Reader) (string, error) {
	ms := t.UTC().UnixMilli()
	if ms < 0 || t.UTC().After(maxULIDTime) {
		return "", fmt.Errorf("job id for %s: outside the range a ULID can encode", t.UTC().Format(time.RFC3339))
	}

	var raw [16]byte
	// The timestamp occupies the top 48 bits, big-endian, so lexical order
	// matches chronological order.
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := io.ReadFull(entropy, raw[6:]); err != nil {
		return "", fmt.Errorf("read job id entropy: %w", err)
	}
	return encodeULID(raw), nil
}

// NewJobIDNow returns a ULID for the current instant, using crypto/rand.
func NewJobIDNow() (string, error) {
	return NewJobID(time.Now(), rand.Reader)
}

// encodeULID renders 16 bytes as 26 Crockford base32 characters.
//
// 26 characters hold 130 bits, so the value is the 128-bit number padded with
// two leading zero bits. That padding, plus a timestamp below 2^48, is why
// the first character never exceeds '7'.
func encodeULID(raw [16]byte) string {
	hi := binary.BigEndian.Uint64(raw[0:8])
	lo := binary.BigEndian.Uint64(raw[8:16])

	out := make([]byte, ulidLen)
	for i := ulidLen - 1; i >= 0; i-- {
		out[i] = crockfordAlphabet[lo&0x1f]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}
