package model

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fixedEntropy repeats b, so a test can mint ids that differ only by time.
func fixedEntropy(b byte) io.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{b}, 64))
}

func TestNewJobIDIsAValidULID(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
	}{
		{name: "today", at: testNow},
		{name: "unix epoch", at: time.UnixMilli(0).UTC()},
		{name: "last encodable instant", at: maxULIDTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := NewJobID(tt.at, fixedEntropy(0xAB))
			if err != nil {
				t.Fatalf("NewJobID() error = %v", err)
			}
			if !ValidJobID(id) {
				t.Errorf("NewJobID() = %q, which ValidJobID rejects", id)
			}
			if len(id) != ulidLen {
				t.Errorf("len = %d, want %d", len(id), ulidLen)
			}
		})
	}
}

func TestNewJobIDSortsByTime(t *testing.T) {
	// The store lists jobs newest first by reading sort keys descending, so
	// lexical order must follow creation order.
	var ids []string
	for i := range 5 {
		id, err := NewJobID(testNow.Add(time.Duration(i)*time.Second), fixedEntropy(0x00))
		if err != nil {
			t.Fatalf("NewJobID() error = %v", err)
		}
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Errorf("id %d (%s) does not sort before id %d (%s)", i-1, ids[i-1], i, ids[i])
		}
	}
}

func TestNewJobIDOneMillisecondApartDiffers(t *testing.T) {
	earlier, err := NewJobID(testNow, fixedEntropy(0x00))
	if err != nil {
		t.Fatalf("NewJobID() error = %v", err)
	}
	later, err := NewJobID(testNow.Add(time.Millisecond), fixedEntropy(0x00))
	if err != nil {
		t.Fatalf("NewJobID() error = %v", err)
	}
	if earlier >= later {
		t.Errorf("one millisecond later sorted no higher: %s then %s", earlier, later)
	}
}

func TestNewJobIDEntropyVaries(t *testing.T) {
	a, err := NewJobID(testNow, fixedEntropy(0x00))
	if err != nil {
		t.Fatalf("NewJobID() error = %v", err)
	}
	b, err := NewJobID(testNow, fixedEntropy(0xFF))
	if err != nil {
		t.Fatalf("NewJobID() error = %v", err)
	}
	if a == b {
		t.Fatal("different entropy produced the same id")
	}
	// Same millisecond, so the timestamp characters must still match.
	if a[:10] != b[:10] {
		t.Errorf("timestamp prefixes differ for the same instant: %s vs %s", a[:10], b[:10])
	}
}

func TestNewJobIDRejectsUnencodableTimes(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
	}{
		{name: "before the epoch", at: time.UnixMilli(-1).UTC()},
		{name: "past the 48-bit timestamp", at: maxULIDTime.Add(time.Millisecond)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewJobID(tt.at, fixedEntropy(0x00)); err == nil {
				t.Error("NewJobID() = nil error, want one")
			}
		})
	}
}

func TestNewJobIDReportsEntropyFailure(t *testing.T) {
	tests := []struct {
		name    string
		entropy io.Reader
	}{
		{name: "empty", entropy: bytes.NewReader(nil)},
		{name: "short", entropy: bytes.NewReader(make([]byte, 9))},
		{name: "broken", entropy: brokenReader{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := NewJobID(testNow, tt.entropy)
			if err == nil {
				t.Fatalf("NewJobID() = %q, want an error", id)
			}
			if id != "" {
				t.Errorf("NewJobID() returned %q alongside an error", id)
			}
		})
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestNewJobIDNow(t *testing.T) {
	id, err := NewJobIDNow()
	if err != nil {
		t.Fatalf("NewJobIDNow() error = %v", err)
	}
	if !ValidJobID(id) {
		t.Errorf("NewJobIDNow() = %q, which ValidJobID rejects", id)
	}

	// Two calls must differ even if they land in the same millisecond.
	other, err := NewJobIDNow()
	if err != nil {
		t.Fatalf("NewJobIDNow() error = %v", err)
	}
	if id == other {
		t.Error("two calls produced the same id")
	}
}

func TestEncodeULIDUsesCrockfordOnly(t *testing.T) {
	id, err := NewJobID(testNow, fixedEntropy(0xFF))
	if err != nil {
		t.Fatalf("NewJobID() error = %v", err)
	}
	for _, r := range id {
		if !strings.ContainsRune(crockfordAlphabet, r) {
			t.Errorf("id %q contains %q, which is outside Crockford base32", id, r)
		}
	}
}

func TestEncodeULIDBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  [16]byte
		want string
	}{
		{name: "all zero", raw: [16]byte{}, want: strings.Repeat("0", ulidLen)},
		{
			name: "all ones",
			raw:  [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			// 128 bits of ones padded to 130: the leading digit is 7, the
			// rest are Z.
			want: "7" + strings.Repeat("Z", ulidLen-1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeULID(tt.raw); got != tt.want {
				t.Errorf("encodeULID() = %q, want %q", got, tt.want)
			}
		})
	}
}
